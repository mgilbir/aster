package svg

import (
	"github.com/mgilbir/aster/purego/internal/scene"
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
	start := 0
	for i := 0; i < len(s); i++ {
		var rep string
		switch s[i] {
		case '&':
			rep = "&amp;"
		case '<':
			rep = "&lt;"
		case '>':
			rep = "&gt;"
		case '"':
			rep = "&quot;"
		case '\t':
			rep = "&#x9;"
		case '\n':
			rep = "&#xA;"
		case '\r':
			rep = "&#xD;"
		default:
			continue
		}
		dst = append(dst, s[start:i]...)
		dst = append(dst, rep...)
		start = i + 1
	}
	return append(dst, s[start:]...)
}

// appendEscaped escapes & < > and, in attribute values, also " tab, LF and CR,
// in that order of precedence, exactly like vega's innerText/attrText.
func appendEscaped(dst []byte, s string, attr bool) []byte {
	start := 0
	for i := 0; i < len(s); i++ {
		var rep string
		switch s[i] {
		case '&':
			rep = "&amp;"
		case '<':
			rep = "&lt;"
		case '>':
			rep = "&gt;"
		case '"':
			if !attr {
				continue
			}
			rep = "&quot;"
		case '\t':
			if !attr {
				continue
			}
			rep = "&#x9;"
		case '\n':
			if !attr {
				continue
			}
			rep = "&#xA;"
		case '\r':
			if !attr {
				continue
			}
			rep = "&#xD;"
		default:
			continue
		}
		dst = append(dst, s[start:i]...)
		dst = append(dst, rep...)
		start = i + 1
	}
	return append(dst, s[start:]...)
}
