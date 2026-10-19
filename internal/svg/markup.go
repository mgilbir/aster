package svg

import (
	"context"
	"math"
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
//
// The writer owns the bounds of the output (C7, audit 2026-10-18): every
// method that appends checks the size where it writes, so no caller has to
// remember to, and an item that expands inside itself (a line break into a
// <tspan>, a character into an entity, a path command into its numbers) is
// stopped as it grows. Past max bytes, or once ctx is done (polled every
// pollBytes of output), the writer records the error and every later call
// writes nothing: the error is sticky, and the renderer returns it. The check
// on the common path is one integer comparison (len(buf) > next).
type writer struct {
	buf   []byte
	open  bool // a start tag's attributes are being written; '>' is pending
	stack []lit
	// skel, when set, records the structure written (see svgcheck.Skeleton),
	// for tests that check the output parses back to exactly that.
	skel *[]string

	ctx  context.Context
	max  int   // the bound on len(buf), 0 for none
	poll int   // the size past which ctx is polled next
	next int   // min(max, poll): the size past which slow runs
	err  error // sticky: ErrTooLarge or ctx's error
}

// pollBytes is how much output passes between two polls of the context: a
// poll every few tens of microseconds of writing.
const pollBytes = 64 << 10

// escapeChunk is how much input one escaping step reads before the size is
// checked: an entity is at most six bytes for one, so a step writes at most
// six times this past the bound.
const escapeChunk = 1 << 10

// reset makes the writer append to dst, failing past max bytes in all (0:
// unbounded) and when ctx is done.
func (w *writer) reset(ctx context.Context, dst []byte, max int) {
	w.buf, w.ctx, w.max, w.err = dst, ctx, max, nil
	w.poll = len(dst) + pollBytes
	w.limits()
}

// limits sets next from max and poll.
func (w *writer) limits() {
	w.next = w.poll
	if w.max > 0 && w.max < w.next {
		w.next = w.max
	}
}

// wrote checks the size after an append.
func (w *writer) wrote() {
	if len(w.buf) > w.next {
		w.slow()
	}
}

// slow is wrote past next: the bound, or a poll of the context.
func (w *writer) slow() {
	if w.err != nil {
		return
	}
	if w.max > 0 && len(w.buf) > w.max {
		w.fail(ErrTooLarge)
		return
	}
	if len(w.buf) > w.poll {
		if err := w.ctx.Err(); err != nil {
			w.fail(err)
			return
		}
		w.poll = len(w.buf) + pollBytes
	}
	w.limits()
}

// fail records err; the writer writes nothing more.
func (w *writer) fail(err error) {
	if w.err == nil {
		w.err = err
	}
	w.next = math.MaxInt
}

// room is how many more bytes the document may take, -1 for any number.
func (w *writer) room() int {
	if w.max <= 0 {
		return -1
	}
	return max(w.max-len(w.buf), 0)
}

// bytes is the output written so far.
func (w *writer) bytes() []byte { return w.buf }

func (w *writer) start(tag lit) {
	if w.err != nil {
		return
	}
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
	w.wrote()
}

// end closes the innermost element.
func (w *writer) end() {
	if w.err != nil {
		return
	}
	tag := w.stack[len(w.stack)-1]
	w.stack = w.stack[:len(w.stack)-1]
	if w.skel != nil {
		*w.skel = append(*w.skel, "/")
	}
	if w.open {
		w.buf = append(w.buf, '/', '>')
		w.open = false
	} else {
		w.buf = append(w.buf, '<', '/')
		w.buf = append(w.buf, tag...)
		w.buf = append(w.buf, '>')
	}
	w.wrote()
}

// attrOpen starts the attribute name="…; the value is written with the val
// methods and ended with attrClose.
func (w *writer) attrOpen(name lit) {
	if w.err != nil {
		return
	}
	if w.skel != nil {
		*w.skel = append(*w.skel, "@"+string(name))
	}
	w.buf = append(w.buf, ' ')
	w.buf = append(w.buf, name...)
	w.buf = append(w.buf, '=', '"')
	w.wrote()
}

// attrClose ends the attribute value attrOpen started.
func (w *writer) attrClose() {
	if w.err != nil {
		return
	}
	w.buf = append(w.buf, '"')
	w.wrote()
}

// valLit appends a constant part of an attribute value.
func (w *writer) valLit(s lit) {
	if w.err != nil {
		return
	}
	w.buf = append(w.buf, s...)
	w.wrote()
}

// valNum appends a number to an attribute value, as JavaScript prints it.
func (w *writer) valNum(f float64) {
	if w.err != nil {
		return
	}
	w.buf = scene.AppendNumber(w.buf, f)
	w.wrote()
}

// valStr appends text to an attribute value, escaped.
func (w *writer) valStr(s string) { w.escaped(s, true) }

// escaped appends s escaped, a chunk at a time, checking the size after
// each: one value cannot take the document past the bound by more than a
// chunk's entities, however long it is.
func (w *writer) escaped(s string, attr bool) {
	for w.err == nil && len(s) > escapeChunk {
		// Cut before a byte that does not continue a UTF-8 sequence, so no
		// character is split: each part decodes (and escapes) as the whole
		// does. A run of continuation bytes is invalid UTF-8 whose bytes
		// decode one by one wherever it is cut.
		i := escapeChunk
		for i > 0 && s[i]&0xC0 == 0x80 {
			i--
		}
		if i == 0 {
			i = escapeChunk
		}
		w.buf = appendEscaped(w.buf, s[:i], attr)
		s = s[i:]
		w.wrote()
	}
	if w.err != nil {
		return
	}
	w.buf = appendEscaped(w.buf, s, attr)
	w.wrote()
}

// valJS appends a JavaScript value to an attribute value as a template
// string prints it: a number in JavaScript's formatting, anything else as
// String(v), escaped. An array is joined as it is written, element by
// element, so its text is bounded by the writer as it grows, where String(v)
// would build all of it first (an array that holds another many times over
// prints that one as many times).
func (w *writer) valJS(v jsval.Value) {
	switch {
	case v.IsNum():
		w.valNum(v.NumValue())
	case v.IsArr():
		w.valArray(v, 0)
	default:
		w.valStr(v.AsString())
	}
}

// valArray is valJS of an array at the given nesting depth: what
// jsval.Value.AsString gives, Array.prototype.join's text with nullish
// elements empty and nothing past jsval.MaxValueDepth.
func (w *writer) valArray(v jsval.Value, depth int) {
	if depth > jsval.MaxValueDepth {
		return
	}
	for i, it := range v.Items() {
		if w.err != nil {
			return
		}
		if i > 0 {
			w.valLit(",")
		}
		switch {
		case it.IsNullish():
		case it.IsArr():
			w.valArray(it, depth+1)
		case depth+1 > jsval.MaxValueDepth:
		case it.IsNum():
			w.valNum(it.NumValue())
		default:
			w.valStr(it.AsString())
		}
	}
}

// attr writes name="value" with attribute escaping.
func (w *writer) attr(name lit, value string) {
	w.attrOpen(name)
	w.valStr(value)
	w.attrClose()
}

// attrJS writes name="String(value)" (see valJS).
func (w *writer) attrJS(name lit, value jsval.Value) {
	w.attrOpen(name)
	w.valJS(value)
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
	if len(value) > 0 {
		w.escaped(unsafe.String(unsafe.SliceData(value), len(value)), true)
	}
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
	if s == "" || w.err != nil {
		return
	}
	if w.open {
		w.buf = append(w.buf, '>')
		w.open = false
	}
	n := len(w.buf)
	w.escaped(s, false)
	if w.skel != nil && len(w.buf) > n && (len(*w.skel) == 0 || (*w.skel)[len(*w.skel)-1] != "#") {
		*w.skel = append(*w.skel, "#")
	}
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
