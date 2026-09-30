package svg

import (
	"unicode/utf8"
	"unsafe"

	"github.com/mgilbir/aster/internal/scene"
)

// writer builds markup with vega's `markup()` semantics: an element whose
// content is empty is written as a self-closing tag (`<path .../>`), text is
// escaped, and attributes are written in the order they are given.
type writer struct {
	buf   []byte
	open  bool // a start tag's attributes are being written; '>' is pending
	stack []string
}

func (w *writer) start(tag string) {
	if w.open {
		w.buf = append(w.buf, '>')
	}
	w.stack = append(w.stack, tag)
	w.buf = append(w.buf, '<')
	w.buf = append(w.buf, tag...)
	w.open = true
}

// end closes the innermost element.
func (w *writer) end() {
	tag := w.stack[len(w.stack)-1]
	w.stack = w.stack[:len(w.stack)-1]
	if w.open {
		w.buf = append(w.buf, '/', '>')
		w.open = false
		return
	}
	w.buf = append(w.buf, '<', '/')
	w.buf = append(w.buf, tag...)
	w.buf = append(w.buf, '>')
}

func (w *writer) attrName(name string) {
	w.buf = append(w.buf, ' ')
	w.buf = append(w.buf, name...)
	w.buf = append(w.buf, '=', '"')
}

// attr writes name="value" with attribute escaping.
func (w *writer) attr(name, value string) {
	w.attrName(name)
	w.buf = appendEscaped(w.buf, value, true)
	w.buf = append(w.buf, '"')
}

// attrNum writes name="number" using JavaScript number formatting.
func (w *writer) attrNum(name string, f float64) {
	w.attrName(name)
	w.buf = scene.AppendNumber(w.buf, f)
	w.buf = append(w.buf, '"')
}

// attrBytes writes name="value" for value bytes, with attribute escaping.
func (w *writer) attrBytes(name string, value []byte) {
	w.attrName(name)
	w.buf = appendEscapedBytes(w.buf, value)
	w.buf = append(w.buf, '"')
}

// attrRaw writes a value that cannot contain characters needing escapes.
func (w *writer) attrRaw(name, value string) {
	w.attrName(name)
	w.buf = append(w.buf, value...)
	w.buf = append(w.buf, '"')
}

// text appends escaped text content. Empty text leaves the element empty.
func (w *writer) text(s string) {
	if s == "" {
		return
	}
	if w.open {
		w.buf = append(w.buf, '>')
		w.open = false
	}
	w.buf = appendEscaped(w.buf, s, false)
}

// appendEscapedBytes is appendEscaped(attr=true) for a byte slice.
func appendEscapedBytes(dst, s []byte) []byte {
	if len(s) == 0 {
		return dst
	}
	return appendEscaped(dst, unsafe.String(unsafe.SliceData(s), len(s)), true)
}

// appendEscaped escapes & < > and, in attribute values, also " tab, LF and CR,
// in that order of precedence, exactly like vega's innerText/attrText.
//
// Deliberate divergence from upstream, which escapes nothing else: characters
// XML 1.0 forbids (C0 controls other than tab, LF and CR, U+FFFE, U+FFFF) are
// dropped and invalid UTF-8 (including lone surrogates) becomes U+FFFD, so the
// output is always well-formed XML whatever the data holds.
func appendEscaped(dst []byte, s string, attr bool) []byte {
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		var rep string
		n := 1
		switch {
		case c == '&':
			rep = "&amp;"
		case c == '<':
			rep = "&lt;"
		case c == '>':
			rep = "&gt;"
		case c == '"':
			if !attr {
				i++
				continue
			}
			rep = "&quot;"
		case c == '\t':
			if !attr {
				i++
				continue
			}
			rep = "&#x9;"
		case c == '\n':
			if !attr {
				i++
				continue
			}
			rep = "&#xA;"
		case c == '\r':
			if !attr {
				i++
				continue
			}
			rep = "&#xD;"
		case c < 0x20:
			rep = ""
		case c < utf8.RuneSelf:
			i++
			continue
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			switch {
			case r == utf8.RuneError && size == 1:
				rep = "\uFFFD"
			case r == 0xFFFE || r == 0xFFFF:
				rep = ""
				n = size
			default:
				i += size
				continue
			}
		}
		dst = append(dst, s[start:i]...)
		dst = append(dst, rep...)
		i += n
		start = i
	}
	return append(dst, s[start:]...)
}

// blendModes are the CSS mix-blend-mode keywords; any other blend value is
// dropped rather than written into the style attribute.
var blendModes = map[string]bool{
	"normal": true, "multiply": true, "screen": true, "overlay": true,
	"darken": true, "lighten": true, "color-dodge": true, "color-burn": true,
	"hard-light": true, "soft-light": true, "difference": true,
	"exclusion": true, "hue": true, "saturation": true, "color": true,
	"luminosity": true,
}
