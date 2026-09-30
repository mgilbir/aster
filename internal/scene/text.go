package scene

import (
	"math"
	"strings"
	"unicode/utf16"

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
	if it.FontSize.Set() {
		return it.FontSize.Zero()
	}
	return 11
}

// LineHeight is `item.lineHeight != null ? item.lineHeight : fontSize + 2`.
func LineHeight(it *Item) float64 {
	if it.LineHeight.Set() {
		return it.LineHeight.Val()
	}
	return FontSize(it) + 2
}

// FontFamily is the item's font, defaulting to sans-serif. With quote, double
// quotes inside the family are swapped for single quotes (for embedding in a
// quoted attribute).
func FontFamily(it *Item, quote bool) string {
	f := it.Font
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
	if it.FontStyle != "" {
		b.WriteString(it.FontStyle)
		b.WriteByte(' ')
	}
	if it.FontVariant != "" {
		b.WriteString(it.FontVariant)
		b.WriteByte(' ')
	}
	if it.FontWeight != "" {
		b.WriteString(it.FontWeight)
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

// TextLines returns the lines of an item's text. multi is true when the text is
// an array of two or more lines (or a string split on lineBreak); otherwise
// lines holds one entry. Null entries become "" and other values are
// stringified, as textValue does with `line + ”`.
func TextLines(it *Item) (lines []string, multi bool) {
	t := it.Text
	switch {
	case t.IsArr():
		items := t.Items()
		if len(items) > 1 {
			out := make([]string, len(items))
			for i, v := range items {
				out[i] = lineString(v)
			}
			return out, true
		}
		if len(items) == 1 {
			return []string{lineString(items[0])}, false
		}
		return []string{""}, false
	case it.LineBreak != "" && t.IsTruthy():
		parts := strings.Split(t.AsString(), it.LineBreak)
		if len(parts) > 1 {
			return parts, true
		}
		return parts, false
	}
	return []string{lineString(t)}, false
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

// TextLine returns the item's text as a single line when it is not multi-line
// (lines is nil), or the lines otherwise (line is empty). It avoids the slice
// TextLines allocates for the common single-line case.
func TextLine(it *Item) (line string, lines []string) {
	t := it.Text
	if !t.IsArr() && (it.LineBreak == "" || !t.IsTruthy()) {
		return lineString(t), nil
	}
	l, multi := TextLines(it)
	if multi {
		return "", l
	}
	return l[0], nil
}

// MultiLineOffset is the extra height of a multi-line text block.
func MultiLineOffset(it *Item) float64 {
	lines, multi := TextLines(it)
	if multi {
		return float64(len(lines)-1) * LineHeight(it)
	}
	return 0
}

// TextValue returns the text to display for one line: trimmed, and truncated
// with an ellipsis to fit `limit` when one is set.
func (m Metrics) TextValue(it *Item, line string) string {
	text := trimJS(line)
	if it.Limit.Val() > 0 && len(text) > 0 {
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

// estimateWidth is vega's fallback: ~~(0.8 * length * fontSize), truncating
// toward zero like a 32-bit integer conversion.
func estimateWidth(text string, fontSize float64) float64 {
	v := 0.8 * float64(utf16Len(text)) * fontSize
	if v != v || math.IsInf(v, 0) {
		return 0
	}
	return float64(int32(int64(v)))
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
	limit := it.Limit.Val()
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

	ellipsis := it.Ellipsis
	if ellipsis == "" {
		ellipsis = "…"
	}
	rtl := it.Dir == "rtl"
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
