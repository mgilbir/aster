package scene

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"

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
	if v, ok := it.Raw["font"]; ok {
		// `font || 'sans-serif'` keeps a truthy array or number, and a falsy
		// value of any kind (0, false, "") falls back.
		if !v.IsTruthy() {
			return "sans-serif"
		}
		if !quote {
			return it.Walker().String(v)
		}
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
	// (item.fontStyle ? item.fontStyle + ' ' : ''), ...: truthiness.
	if s := it.TruthyStr("fontStyle", it.FontStyle()); s != "" {
		b.WriteString(s)
		b.WriteByte(' ')
	}
	if s := it.TruthyStr("fontVariant", it.FontVariant()); s != "" {
		b.WriteString(s)
		b.WriteByte(' ')
	}
	if s := it.TruthyStr("fontWeight", it.FontWeight()); s != "" {
		b.WriteString(s)
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
	// baseline === 'top' ...: strict, so ['top'] is no baseline.
	switch it.StrictStr("baseline", it.Baseline) {
	case "top":
		return jsmath.Round(0.79 * h)
	case "middle":
		return jsmath.Round(0.30 * h)
	case "bottom":
		return jsmath.Round(-0.21 * h)
	case "line-top":
		return jsmath.Round(float64(0.29*h) + float64(0.5*LineHeight(it)))
	case "line-bottom":
		return jsmath.Round(float64(0.29*h) - float64(0.5*LineHeight(it)))
	}
	return 0
}

// Lines are the lines of an item's text, upstream's textLines(item): the
// entries of an array of two or more (or of the one array an array holds),
// or the text split on lineBreak, else the text as one line. They are read in
// place, one at a time, never made into a list: a text of a million line
// breaks is a million lines, and the bounds, the layout and the writer each
// go through them (C7, audit 2026-10-18). Null entries are "" and other
// values are stringified, as textValue does with `line + ”`, with the item's
// Walker.
//
// A line leaves JavaScript as UTF-8 (to the canvas that measures it, in the
// SVG document), which writes each lone surrogate as U+FFFD: the lines are
// well formed, one U+FFFD a surrogate, which is also one code unit, so
// truncation counts the same.
type Lines struct {
	// N is the number of lines (an array of lines can hold none); Multi is
	// true for an array of two or more lines, the lines of an array an array
	// holds, or a text split in two or more (drawn as one <tspan> a line).
	N     int
	Multi bool

	it    *Item
	arr   []jsval.Value // the lines of an array
	s     string        // the text left to split, or the one line
	sep   string        // the lineBreak s splits on (empty: between characters)
	split bool          // s is split on sep
	next  int           // the index of the next line
}

// lineBreak is the item's line break when it is truthy (`item.lineBreak &&
// ...`), else "".
func (it *Item) lineBreak() string {
	if it.Raw != nil {
		if v, ok := it.Raw["lineBreak"]; ok && v.IsTruthy() {
			return it.LineBreak()
		}
	}
	if it.Raw != nil {
		if v, ok := it.Raw["lineBreak"]; ok && !v.IsTruthy() {
			return ""
		}
	}
	return it.LineBreak()
}

// hasLineBreak is the truthiness of `item.lineBreak`: a break that prints
// empty (an empty array) is truthy, and splits the text between characters.
func (it *Item) hasLineBreak() bool { return it.StrTruthy("lineBreak", it.LineBreak()) }

// TextLines returns the lines of an item's text, ready to read with Next.
func TextLines(it *Item) Lines {
	l, _ := TextLinesErr(it)
	return l
}

// TextLinesErr is TextLines with the error upstream's textLines raises: a
// truthy text that is neither a string nor an array has no split method.
func TextLinesErr(it *Item) (Lines, error) {
	t := it.Text()
	switch {
	case t.IsArr():
		items := t.Items()
		switch len(items) {
		case 0:
			return Lines{N: 1}, nil
		case 1:
			// lineArray: a one-element array is its element, and when that
			// is an array it is the list of lines, however long.
			if inner := items[0]; inner.IsArr() {
				in := inner.Items()
				return Lines{N: len(in), Multi: true, it: it, arr: in}, nil
			}
			return Lines{N: 1, s: lineString(it, items[0])}, nil
		}
		return Lines{N: len(items), Multi: true, it: it, arr: items}, nil
	case it.hasLineBreak() && t.IsTruthy():
		if !t.IsStr() {
			return Lines{N: 1, s: lineString(it, t)}, ErrTextSplit
		}
		text, sep := lineString(it, t), it.lineBreak()
		// text.split(lineBreak) has one more part than it has separators;
		// split("") cuts between characters.
		n := utf8.RuneCountInString(text)
		if sep != "" {
			n = strings.Count(text, sep) + 1
		}
		if n > 1 {
			return Lines{N: n, Multi: true, s: text, sep: sep, split: true}, nil
		}
		return Lines{N: 1, s: text}, nil
	}
	return Lines{N: 1, s: lineString(it, t)}, nil
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
		return lineString(l.it, l.arr[l.next-1]), true
	case l.split && l.next < l.N:
		i := strings.Index(l.s, l.sep)
		if l.sep == "" { // between characters
			_, i = utf8.DecodeRuneInString(l.s)
		}
		line, l.s = l.s[:i], l.s[i+len(l.sep):]
		return line, true
	}
	return l.s, true
}

// lineString is `line == null ? ” : line + ”` without the trim, well formed.
func lineString(it *Item, v jsval.Value) string {
	if v.IsNullish() {
		return ""
	}
	if v.IsStr() {
		return jsval.ToWellFormed(v.StrValue())
	}
	return jsval.ToWellFormed(it.Walker().String(v))
}

// MultiLineOffset is the extra height of a multi-line text block. A single
// line is 0 * lineHeight, which is NaN when the line height is infinite (a
// font size of Infinity), as upstream's is.
func MultiLineOffset(it *Item) float64 {
	n := 0.0
	if l := TextLines(it); l.Multi {
		n = float64(l.N - 1)
	}
	return n * LineHeight(it)
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

	// item.ellipsis || '\u2026', and the strict item.dir === 'rtl'.
	// a truthy value that prints empty ([null]) is the ellipsis, an empty one
	ellipsis := it.Ellipsis()
	if !it.StrTruthy("ellipsis", ellipsis) {
		ellipsis = "…"
	}
	rtl := it.StrictStr("dir", it.Dir()) == "rtl"
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
