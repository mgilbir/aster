package scene

import (
	"strings"
	"unicode/utf16"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// TextMeasurer measures the advance width of text set in a CSS font.
//
// cssFont is the CSS font shorthand vega builds with its font() helper, e.g.
// `bold 11px "Helvetica Neue", sans-serif`; see CSSFont.
type TextMeasurer interface {
	MeasureText(text, cssFont string) float64
}

// Metrics is vega's textMetrics: text width and the text helpers that depend on
// it (truncation to `limit`). A nil Measurer selects vega's estimate,
// ~~(0.8 * length * fontSize), which is what upstream uses when no canvas is
// available.
type Metrics struct {
	Measurer TextMeasurer
}

// FontSize is `item.fontSize != null ? (+item.fontSize || 0) : 11`.
func FontSize(it *Item) float64 {
	if it.FontSize().Set() {
		return it.FontSize().Zero()
	}
	return 11
}

// LineHeight is `item.lineHeight != null ? item.lineHeight : fontSize + 2`.
func LineHeight(it *Item) float64 {
	if it.LineHeight().Set() {
		return it.LineHeight().Val()
	}
	return FontSize(it) + 2
}

// FontFamily is the item's font, defaulting to sans-serif. With quote, double
// quotes inside the family are swapped for single quotes (for embedding in a
// quoted attribute).
func FontFamily(it *Item, quote bool) string {
	if v, ok := it.Raw["font"]; ok && !quote && v.IsTruthy() {
		return v.AsString() // `font || 'sans-serif'` keeps a truthy array
	}
	f := it.Font()
	if f == "" {
		return "sans-serif"
	}
	if quote {
		return strings.ReplaceAll(f, `"`, `'`)
	}
	return f
}

// CSSFont builds the CSS font shorthand for an item: optional style, variant
// and weight, the size in px, then the family.
func CSSFont(it *Item, quote bool) string {
	var b strings.Builder
	if it.FontStyle() != "" {
		b.WriteString(it.FontStyle())
		b.WriteByte(' ')
	}
	if it.FontVariant() != "" {
		b.WriteString(it.FontVariant())
		b.WriteByte(' ')
	}
	if it.FontWeight() != "" {
		b.WriteString(it.FontWeight())
		b.WriteByte(' ')
	}
	var nb [32]byte
	b.Write(jsval.AppendJSNumber(nb[:0], FontSize(it)))
	b.WriteString("px ")
	b.WriteString(FontFamily(it, quote))
	return b.String()
}

// BaselineOffset is the vertical shift vega applies for a text baseline, done
// by hand because SVG's alignment-baseline is not consistently supported.
func BaselineOffset(it *Item) float64 {
	h := FontSize(it)
	switch it.Baseline {
	case "top":
		return jsRound(0.79 * h)
	case "middle":
		return jsRound(0.30 * h)
	case "bottom":
		return jsRound(-0.21 * h)
	case "line-top":
		return jsRound(float64(0.29*h) + float64(0.5*LineHeight(it)))
	case "line-bottom":
		return jsRound(float64(0.29*h) - float64(0.5*LineHeight(it)))
	}
	return 0
}

// Lines are the lines of an item's text, upstream's textLines(item): the
// entries of an array of two or more, or the text split on lineBreak, else
// the text as one line. They are read in place, one at a time, never made
// into a list: a text of a million line breaks is a million lines, and the
// bounds, the layout and the writer each go through them (C7, audit
// 2026-10-18). Null entries are "" and other values are stringified, as
// textValue does with `line + ”`.
type Lines struct {
	// N is the number of lines; Multi is true for an array of two or more
	// lines or a text split in two or more (drawn as one <tspan> a line).
	N     int
	Multi bool

	arr  []jsval.Value // the lines of an array
	s    string        // the text left to split, or the one line
	sep  string        // the lineBreak s splits on, "" for one line
	next int           // the index of the next line
}

// TextLines returns the lines of an item's text, ready to read with Next.
func TextLines(it *Item) Lines {
	t := it.Text()
	switch {
	case t.IsArr():
		items := t.Items()
		switch len(items) {
		case 0:
			return Lines{N: 1}
		case 1:
			return Lines{N: 1, s: lineString(items[0])}
		}
		return Lines{N: len(items), Multi: true, arr: items}
	case it.LineBreak() != "" && t.IsTruthy():
		text, sep := t.AsString(), it.LineBreak()
		// text.split(lineBreak) has one more part than it has separators.
		if n := strings.Count(text, sep) + 1; n > 1 {
			return Lines{N: n, Multi: true, s: text, sep: sep}
		}
		return Lines{N: 1, s: text}
	}
	return Lines{N: 1, s: lineString(t)}
}

// Line is the text of a single line (when not Multi).
func (l *Lines) Line() string { return l.s }

// Next returns the next line, and false once every line has been read.
func (l *Lines) Next() (line string, ok bool) {
	if l.next >= l.N {
		return "", false
	}
	l.next++
	switch {
	case l.arr != nil:
		return lineString(l.arr[l.next-1]), true
	case l.sep != "" && l.next < l.N:
		i := strings.Index(l.s, l.sep)
		line, l.s = l.s[:i], l.s[i+len(l.sep):]
		return line, true
	}
	return l.s, true
}

// lineString is `line == null ? ” : line + ”` without the trim.
func lineString(v jsval.Value) string {
	if v.IsNullish() {
		return ""
	}
	if v.IsStr() {
		return v.StrValue()
	}
	return v.AsString()
}

// MultiLineOffset is the extra height of a multi-line text block.
func MultiLineOffset(it *Item) float64 {
	if l := TextLines(it); l.Multi {
		return float64(l.N-1) * LineHeight(it)
	}
	return 0
}

// TextValue returns the text to display for one line: trimmed, and truncated
// with an ellipsis to fit `limit` when one is set.
func (m Metrics) TextValue(it *Item, line string) string {
	text := trimJS(line)
	if it.Limit().Val() > 0 && len(text) > 0 {
		return m.truncate(it, text)
	}
	return text
}

// Width is textMetrics.width(item, text): the width of the displayed text (after
// trim and truncation). Zero for a non-positive font size or empty text when a
// Measurer is in use.
func (m Metrics) Width(it *Item, text string) float64 {
	if m.Measurer == nil {
		return estimateWidth(m.TextValue(it, text), FontSize(it))
	}
	if FontSize(it) <= 0 {
		return 0
	}
	text = m.TextValue(it, text)
	if text == "" {
		return 0
	}
	return m.Measurer.MeasureText(text, CSSFont(it, false))
}

// Height is textMetrics.height(item), the font size.
func (m Metrics) Height(it *Item) float64 { return FontSize(it) }

// utf16Len is JavaScript's String length.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// estimateWidth is vega's fallback: ~~(0.8 * length * fontSize), JavaScript's
// ToInt32 of the product.
func estimateWidth(text string, fontSize float64) float64 {
	return float64(jsmath.ToInt32(0.8 * float64(utf16Len(text)) * fontSize))
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// truncate shortens text to fit the item's limit, appending (or, for right-to-
// left text, prepending) the ellipsis. It binary-searches over UTF-16 code unit
// prefixes, as upstream does over JavaScript string slices.
func (m Metrics) truncate(it *Item, text string) string {
	limit := it.Limit().Val()
	width := func(s string) float64 {
		if m.Measurer == nil {
			return estimateWidth(s, FontSize(it))
		}
		// Measured directly: the truncation search uses the measurer
		// without re-applying the limit.
		return m.Measurer.MeasureText(s, CSSFont(it, false))
	}
	if width(text) < limit {
		return text
	}

	ellipsis := it.Ellipsis()
	if ellipsis == "" {
		ellipsis = "…"
	}
	rtl := it.Dir() == "rtl"
	limit -= width(ellipsis)

	var slice func(lo, hi int) string
	n := 0
	if isASCII(text) {
		n = len(text)
		slice = func(lo, hi int) string { return text[lo:hi] }
	} else {
		u := utf16.Encode([]rune(text))
		n = len(u)
		slice = func(lo, hi int) string { return string(utf16.Decode(u[lo:hi])) }
	}

	lo, hi := 0, n
	if rtl {
		for lo < hi {
			mid := (lo + hi) >> 1
			if width(slice(mid, n)) > limit {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return ellipsis + slice(lo, n)
	}
	for lo < hi {
		mid := 1 + (lo+hi)>>1
		if width(slice(0, mid)) < limit {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return slice(0, lo) + ellipsis
}
