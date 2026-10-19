package svg

import (
	"unicode/utf8"
	"unsafe"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// lit is markup the renderer itself chooses: element and attribute names,
// and the constant parts of attribute values. A string constant converts to
// it implicitly; a string variable would need an explicit conversion, which
// TestNoRawMarkup forbids. So text that comes from a specification or its
// data (a value, a datum, a config, a name) can reach the output only through
// the escaping methods (attr, attrBytes, valStr, valJS, text), the way
// upstream's markup() escapes every attribute value and text it is given.
type lit string

// writer builds markup with vega's `markup()` semantics: an element whose
// content is empty is written as a self-closing tag (`<path .../>`), text is
// escaped, and attributes are written in the order they are given.
//
// Only this file touches buf (TestNoRawMarkup checks it): every byte the
// renderer writes is a lit, a number in JavaScript's formatting, or escaped.
type writer struct {
	buf   []byte
	open  bool // a start tag's attributes are being written; '>' is pending
	stack []lit
	// skel, when set, records the structure written (see svgcheck.Skeleton),
	// for tests that check the output parses back to exactly that.
	skel *[]string
	// walk is the call's Walker, with which a value is written as text.
	walk jsval.Walker
}

// reset makes the writer append to dst.
func (w *writer) reset(dst []byte) { w.buf = dst }

// bytes is the output written so far.
func (w *writer) bytes() []byte { return w.buf }

// size is the length of the output written so far.
func (w *writer) size() int { return len(w.buf) }

func (w *writer) start(tag lit) {
	if w.open {
		w.buf = append(w.buf, '>')
	}
	w.stack = append(w.stack, tag)
	if w.skel != nil {
		*w.skel = append(*w.skel, "<"+string(tag))
	}
	w.buf = append(w.buf, '<')
	w.buf = append(w.buf, tag...)
	w.open = true
}

// end closes the innermost element.
func (w *writer) end() {
	tag := w.stack[len(w.stack)-1]
	w.stack = w.stack[:len(w.stack)-1]
	if w.skel != nil {
		*w.skel = append(*w.skel, "/")
	}
	if w.open {
		w.buf = append(w.buf, '/', '>')
		w.open = false
		return
	}
	w.buf = append(w.buf, '<', '/')
	w.buf = append(w.buf, tag...)
	w.buf = append(w.buf, '>')
}

// attrOpen starts the attribute name="…; the value is written with the val
// methods and ended with attrClose.
func (w *writer) attrOpen(name lit) {
	if w.skel != nil {
		*w.skel = append(*w.skel, "@"+string(name))
	}
	w.buf = append(w.buf, ' ')
	w.buf = append(w.buf, name...)
	w.buf = append(w.buf, '=', '"')
}

// attrClose ends the attribute value attrOpen started.
func (w *writer) attrClose() { w.buf = append(w.buf, '"') }

// valLit appends a constant part of an attribute value.
func (w *writer) valLit(s lit) { w.buf = append(w.buf, s...) }

// valNum appends a number to an attribute value, as JavaScript prints it.
func (w *writer) valNum(f float64) { w.buf = scene.AppendNumber(w.buf, f) }

// valStr appends text to an attribute value, escaped.
func (w *writer) valStr(s string) { w.buf = appendEscaped(w.buf, s, true) }

// valJS appends a JavaScript value to an attribute value as a template
// string prints it: a number in JavaScript's formatting, anything else as
// String(v), escaped.
func (w *writer) valJS(v jsval.Value) {
	if v.IsNum() {
		w.valNum(v.NumValue())
		return
	}
	w.valStr(w.walk.String(v))
}

// attr writes name="value" with attribute escaping.
func (w *writer) attr(name lit, value string) {
	w.attrOpen(name)
	w.valStr(value)
	w.attrClose()
}

// attrNum writes name="number" using JavaScript number formatting.
func (w *writer) attrNum(name lit, f float64) {
	w.attrOpen(name)
	w.valNum(f)
	w.attrClose()
}

// attrBytes writes name="value" for value bytes, with attribute escaping.
func (w *writer) attrBytes(name lit, value []byte) {
	w.attrOpen(name)
	w.buf = appendEscapedBytes(w.buf, value)
	w.attrClose()
}

// attrLit writes an attribute whose value is a constant.
func (w *writer) attrLit(name, value lit) {
	w.attrOpen(name)
	w.valLit(value)
	w.attrClose()
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
	n := len(w.buf)
	w.buf = appendEscaped(w.buf, s, false)
	if w.skel != nil && len(w.buf) > n && (len(*w.skel) == 0 || (*w.skel)[len(*w.skel)-1] != "#") {
		*w.skel = append(*w.skel, "#")
	}
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
// dropped and invalid UTF-8 becomes U+FFFD, a byte at a time, so the output
// is always well-formed XML whatever the data holds. A lone surrogate, which
// a string holds as three WTF-8 bytes, becomes one U+FFFD, as JavaScript's
// UTF-8 encoding of the document writes it.
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
				if c == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2]&0xC0 == 0x80 {
					n = 3 // a lone surrogate
				}
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
var blendModes = map[string]lit{
	"normal": "normal", "multiply": "multiply", "screen": "screen", "overlay": "overlay",
	"darken": "darken", "lighten": "lighten", "color-dodge": "color-dodge", "color-burn": "color-burn",
	"hard-light": "hard-light", "soft-light": "soft-light", "difference": "difference",
	"exclusion": "exclusion", "hue": "hue", "saturation": "saturation", "color": "color",
	"luminosity": "luminosity",
}

// blendMode is the CSS keyword for a blend value, "" for one that is not a
// keyword. The style attribute is CSS, so the keyword written is the table's
// own constant, never the spec's text.
func blendMode(blend string) lit { return blendModes[blend] }
