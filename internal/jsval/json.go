package jsval

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// MaxJSONDepth bounds how deeply a parsed document may nest. Real charts nest
// about a dozen levels; the bound keeps hostile input from driving unbounded
// recursion in every consumer that walks the result.
const MaxJSONDepth = 256

// MaxValueDepth bounds how deeply the functions that walk a Value recursively
// (Equal, AsString, AppendJSON) descend. A parsed document is already limited
// to MaxJSONDepth, but a value built at run time (a formula wrapping its own
// field in an array, once per transform) can nest as deep as the
// specification is long. V8 throws a RangeError at about 10000 frames; here a
// walk that reaches the bound stops (Equal reports false, AsString and
// AppendJSON write nothing for the too-deep value) instead of recursing until
// the goroutine stack overflows, which Go cannot recover from. Each frame is
// under 200 bytes, so the bound costs a few hundred KB of stack.
const MaxValueDepth = 1024

// ErrJSONDepth is returned (wrapped) when a document nests past MaxJSONDepth.
var ErrJSONDepth = errors.New("json: document nests too deeply")

// SyntaxError reports malformed JSON and where it was found.
type SyntaxError struct {
	Offset int
	Msg    string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("json: %s at offset %d", e.Msg, e.Offset)
}

// ParseJSON parses text with JSON.parse semantics: strict RFC 8259 grammar,
// object keys kept in document order, a repeated key overwriting the value at
// its first position. Invalid UTF-8 and lone surrogate escapes become U+FFFD.
func ParseJSON(text []byte) (Value, error) {
	p := parser{s: text}
	if len(text) >= slabMinDoc {
		p.sl = new(slabs)
	}
	p.ws()
	v, err := p.value(0)
	if err != nil {
		return Undefined, err
	}
	p.ws()
	if p.i != len(p.s) {
		return Undefined, p.errf("unexpected trailing data")
	}
	return v, nil
}

// ParseJSONString is ParseJSON over a string.
func ParseJSONString(text string) (Value, error) { return ParseJSON([]byte(text)) }

// slabs holds the chunks small containers are carved from, one allocation per
// chunk: a data file is millions of coordinate pairs and rows.
type slabs struct {
	pa1  slab[arrayBox1]
	pa2  slab[arrayBox2]
	pa3  slab[arrayBox3]
	pa4  slab[arrayBox4]
	po1  slab[objectN1]
	po2  slab[objectN2]
	po3  slab[objectN3]
	po4  slab[objectN4]
	po5  slab[objectN5]
	po6  slab[objectN6]
	po7  slab[objectN7]
	po8  slab[objectN8]
	po9  slab[objectN9]
	po10 slab[objectN10]
	po11 slab[objectN11]
	po12 slab[objectN12]
}

// slabMinDoc is the smallest document that uses chunks; a chart specification
// has a few hundred containers and is better served by exact allocations.
const slabMinDoc = 16 << 10

type parser struct {
	s []byte
	i int

	// Elements and members of the containers being parsed are collected on
	// these stacks and copied into one exactly sized allocation when the
	// container closes, instead of growing a slice per array.
	vstk []Value
	kstk []string
	// keys interns object keys: rows of a data file repeat the same few names.
	keys map[string]string

	// sl carves small arrays and objects out of chunks; only documents big
	// enough to repay it (data files) get one.
	sl *slabs
}

func (p *parser) errf(format string, args ...any) error {
	return &SyntaxError{Offset: p.i, Msg: fmt.Sprintf(format, args...)}
}

func (p *parser) ws() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) value(depth int) (Value, error) {
	if p.i >= len(p.s) {
		return Undefined, p.errf("unexpected end of input")
	}
	switch c := p.s[p.i]; c {
	case '{':
		if depth >= MaxJSONDepth {
			return Undefined, fmt.Errorf("%w (more than %d levels)", ErrJSONDepth, MaxJSONDepth)
		}
		return p.object(depth + 1)
	case '[':
		if depth >= MaxJSONDepth {
			return Undefined, fmt.Errorf("%w (more than %d levels)", ErrJSONDepth, MaxJSONDepth)
		}
		return p.array(depth + 1)
	case '"':
		s, err := p.str()
		if err != nil {
			return Undefined, err
		}
		return Str(s), nil
	case 't':
		return p.literal("true", True)
	case 'f':
		return p.literal("false", False)
	case 'n':
		return p.literal("null", Null)
	default:
		if c == '-' || (c >= '0' && c <= '9') {
			return p.number()
		}
		return Undefined, p.errf("unexpected character %q", c)
	}
}

func (p *parser) literal(word string, v Value) (Value, error) {
	if len(p.s)-p.i < len(word) || string(p.s[p.i:p.i+len(word)]) != word {
		return Undefined, p.errf("invalid literal")
	}
	p.i += len(word)
	return v, nil
}

func (p *parser) number() (Value, error) {
	start := p.i
	if p.s[p.i] == '-' {
		p.i++
	}
	if p.i >= len(p.s) {
		return Undefined, p.errf("invalid number")
	}
	if p.s[p.i] == '0' {
		p.i++
	} else if p.s[p.i] >= '1' && p.s[p.i] <= '9' {
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
	} else {
		return Undefined, p.errf("invalid number")
	}
	if p.i < len(p.s) && p.s[p.i] == '.' {
		p.i++
		n := 0
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
			n++
		}
		if n == 0 {
			return Undefined, p.errf("invalid number")
		}
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		n := 0
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
			n++
		}
		if n == 0 {
			return Undefined, p.errf("invalid number")
		}
	}
	if f, ok := fastNumber(p.s[start:p.i]); ok {
		return Num(f), nil
	}
	f, err := strconv.ParseFloat(string(p.s[start:p.i]), 64)
	if err != nil {
		var ne *strconv.NumError
		if !errors.As(err, &ne) || ne.Err != strconv.ErrRange {
			return Undefined, p.errf("invalid number")
		}
		// JSON.parse rounds out-of-range literals to ±Infinity or 0.
	}
	return Num(f), nil
}

var pow10tab = [...]float64{1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11, 1e12, 1e13, 1e14, 1e15}

// fastNumber converts a validated JSON number of at most 15 significant digits
// and no exponent exactly: the digits are an integer below 2^53 and a power of
// ten up to 1e15 is exact, so one division rounds correctly (Clinger's fast
// path), the same result ParseFloat gives. ok is false for anything else.
func fastNumber(b []byte) (float64, bool) {
	i := 0
	neg := false
	if b[0] == '-' {
		neg = true
		i = 1
	}
	var m uint64
	digits, frac := 0, -1
	for ; i < len(b); i++ {
		c := b[i]
		switch {
		case c == '.':
			frac = 0
		case c >= '0' && c <= '9':
			m = m*10 + uint64(c-'0')
			if m != 0 {
				digits++
			}
			if frac >= 0 {
				frac++
			}
		default: // exponent
			return 0, false
		}
		if digits > 15 || frac > 15 {
			return 0, false
		}
	}
	f := float64(m)
	if frac > 0 {
		f /= pow10tab[frac]
	}
	if neg {
		f = -f
	}
	return f, true
}

func (p *parser) str() (string, error) {
	p.i++ // opening quote
	start := p.i
	// Fast path: no escapes, valid UTF-8, no control characters.
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == '"' {
			raw := p.s[start:p.i]
			p.i++
			if utf8.Valid(raw) {
				return string(raw), nil
			}
			return string([]rune(string(raw))), nil
		}
		if c == '\\' || c < 0x20 {
			break
		}
		p.i++
	}
	buf := append([]byte(nil), p.s[start:p.i]...)
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			if !utf8.Valid(buf) {
				return string([]rune(string(buf))), nil
			}
			return string(buf), nil
		case c < 0x20:
			return "", p.errf("control character in string")
		case c == '\\':
			p.i++
			if p.i >= len(p.s) {
				return "", p.errf("unterminated escape")
			}
			e := p.s[p.i]
			p.i++
			switch e {
			case '"', '\\', '/':
				buf = append(buf, e)
			case 'b':
				buf = append(buf, '\b')
			case 'f':
				buf = append(buf, '\f')
			case 'n':
				buf = append(buf, '\n')
			case 'r':
				buf = append(buf, '\r')
			case 't':
				buf = append(buf, '\t')
			case 'u':
				r, err := p.hex4()
				if err != nil {
					return "", err
				}
				if utf16.IsSurrogate(r) {
					if r < 0xDC00 && p.i+6 <= len(p.s) && p.s[p.i] == '\\' && p.s[p.i+1] == 'u' {
						save := p.i
						p.i += 2
						r2, err := p.hex4()
						if err == nil && r2 >= 0xDC00 && r2 <= 0xDFFF {
							r = utf16.DecodeRune(r, r2)
						} else {
							p.i = save
							r = utf8.RuneError
						}
					} else {
						r = utf8.RuneError
					}
				}
				buf = utf8.AppendRune(buf, r)
			default:
				return "", p.errf("invalid escape \\%c", e)
			}
		default:
			buf = append(buf, c)
			p.i++
		}
	}
	return "", p.errf("unterminated string")
}

func (p *parser) hex4() (rune, error) {
	if p.i+4 > len(p.s) {
		return 0, p.errf("invalid unicode escape")
	}
	var r rune
	for _, c := range p.s[p.i : p.i+4] {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			r |= rune(c-'A') + 10
		default:
			return 0, p.errf("invalid unicode escape")
		}
	}
	p.i += 4
	return r, nil
}

func (p *parser) object(depth int) (Value, error) {
	p.i++ // {
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		return Obj(NewObject(0)), nil
	}
	base := len(p.kstk)
	for {
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			return Undefined, p.errf("expected object key")
		}
		k, err := p.key()
		if err != nil {
			return Undefined, err
		}
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return Undefined, p.errf("expected ':'")
		}
		p.i++
		p.ws()
		v, err := p.value(depth)
		if err != nil {
			return Undefined, err
		}
		p.kstk = append(p.kstk, k)
		p.vstk = append(p.vstk, v)
		p.ws()
		if p.i >= len(p.s) {
			return Undefined, p.errf("unterminated object")
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			n := len(p.kstk) - base
			o := p.newObject(n)
			vbase := len(p.vstk) - n
			for i := 0; i < n; i++ {
				o.Set(p.kstk[base+i], p.vstk[vbase+i])
			}
			clear(p.vstk[vbase:])
			p.kstk = p.kstk[:base]
			p.vstk = p.vstk[:vbase]
			return Obj(o), nil
		default:
			return Undefined, p.errf("expected ',' or '}'")
		}
	}
}

// key reads an object key, sharing the string between occurrences.
func (p *parser) key() (string, error) {
	if p.sl == nil { // a small document repeats few keys; skip the table
		return p.str()
	}
	start := p.i + 1
	for j := start; j < len(p.s); j++ {
		c := p.s[j]
		if c == '"' {
			raw := p.s[start:j]
			if !utf8.Valid(raw) {
				break
			}
			p.i = j + 1
			if k, ok := p.keys[string(raw)]; ok {
				return k, nil
			}
			k := string(raw)
			if p.keys == nil {
				p.keys = make(map[string]string)
			}
			if len(p.keys) < 256 {
				p.keys[k] = k
			}
			return k, nil
		}
		if c == '\\' || c < 0x20 {
			break
		}
	}
	return p.str()
}

func (p *parser) array(depth int) (Value, error) {
	p.i++ // [
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return Arr(nil), nil
	}
	base := len(p.vstk)
	for {
		p.ws()
		v, err := p.value(depth)
		if err != nil {
			return Undefined, err
		}
		p.vstk = append(p.vstk, v)
		p.ws()
		if p.i >= len(p.s) {
			return Undefined, p.errf("unterminated array")
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			arr, items := p.makeArr(len(p.vstk) - base)
			copy(items, p.vstk[base:])
			clear(p.vstk[base:])
			p.vstk = p.vstk[:base]
			return arr, nil
		default:
			return Undefined, p.errf("expected ',' or ']'")
		}
	}
}

// AppendJSON appends JSON.stringify(v) (compact) to dst. Undefined object
// members are omitted and undefined array elements written as null; non-finite
// numbers are null; a Timestamp is written as its epoch number and a Pattern
// as {} (it has no enumerable properties).
func AppendJSON(dst []byte, v Value) []byte {
	return appendJSON(dst, v, "", "", 0)
}

// AppendJSONIndent is JSON.stringify(v, null, indent).
func AppendJSONIndent(dst []byte, v Value, indent string) []byte {
	return appendJSON(dst, v, indent, "\n", 0)
}

// MarshalJSON writes compact JSON.stringify output.
func (v Value) MarshalJSON() ([]byte, error) {
	if v.k == KindUndefined {
		return []byte("null"), nil
	}
	return AppendJSON(nil, v), nil
}

func appendJSON(dst []byte, v Value, indent, prefix string, depth int) []byte {
	if depth > MaxValueDepth {
		return append(dst, "null"...)
	}
	switch v.k {
	case KindUndefined, KindNull:
		return append(dst, "null"...)
	case KindBool:
		if v.n != 0 {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case KindNum, KindTimestamp:
		if math.IsNaN(v.n) || math.IsInf(v.n, 0) {
			return append(dst, "null"...)
		}
		return AppendJSNumber(dst, v.n)
	case KindStr:
		return appendQuoted(dst, v.s)
	case KindPattern:
		return append(dst, "{}"...)
	case KindArr:
		items := v.Items()
		if len(items) == 0 {
			return append(dst, "[]"...)
		}
		inner := prefix + indent
		dst = append(dst, '[')
		for i, it := range items {
			if i > 0 {
				dst = append(dst, ',')
			}
			if indent != "" {
				dst = append(dst, inner...)
			}
			dst = appendJSON(dst, it, indent, inner, depth+1)
		}
		if indent != "" {
			dst = append(dst, prefix...)
		}
		return append(dst, ']')
	case KindObj:
		o := v.ObjValue()
		inner := prefix + indent
		dst = append(dst, '{')
		n := 0
		for i := 0; i < o.Len(); i++ {
			val := o.ValueAt(i)
			if val.k == KindUndefined {
				continue
			}
			if n > 0 {
				dst = append(dst, ',')
			}
			n++
			if indent != "" {
				dst = append(dst, inner...)
			}
			dst = appendQuoted(dst, o.KeyAt(i))
			dst = append(dst, ':')
			if indent != "" {
				dst = append(dst, ' ')
			}
			dst = appendJSON(dst, val, indent, inner, depth+1)
		}
		if n > 0 && indent != "" {
			dst = append(dst, prefix...)
		}
		return append(dst, '}')
	}
	return dst
}

func quoteJSON(s string) string { return string(appendQuoted(nil, s)) }

const hexDigits = "0123456789abcdef"

// appendQuoted writes s as JSON.stringify does: only ", \ and control
// characters are escaped (never <, > or &, unlike encoding/json).
func appendQuoted(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		dst = append(dst, s[start:i]...)
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		default:
			dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
		}
		start = i + 1
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

// slab hands out elements from chunks, one allocation per chunk.
type slab[T any] struct {
	rest []T
	next int
}

// slabNext takes the next element; chunks start small and double up to limit, so
// a small document does not pay for a full chunk of every size.
func slabNext[T any](s *slab[T], limit int) *T {
	if len(s.rest) == 0 {
		s.next = min(limit, max(1, s.next*2))
		s.rest = make([]T, s.next)
	}
	x := &s.rest[0]
	s.rest = s.rest[1:]
	return x
}

// makeArr is MakeArr drawing small arrays from the parser's chunks.
func (p *parser) makeArr(n int) (Value, []Value) {
	if p.sl == nil {
		return MakeArr(n)
	}
	var b *arrayBox
	switch n {
	case 1:
		x := slabNext(&p.sl.pa1, 64)
		x.items, b = x.buf[:], &x.arrayBox
	case 2:
		x := slabNext(&p.sl.pa2, 64)
		x.items, b = x.buf[:], &x.arrayBox
	case 3:
		x := slabNext(&p.sl.pa3, 32)
		x.items, b = x.buf[:], &x.arrayBox
	case 4:
		x := slabNext(&p.sl.pa4, 32)
		x.items, b = x.buf[:], &x.arrayBox
	default:
		return MakeArr(n)
	}
	return Value{k: KindArr, r: b}, b.items
}

// newObject is NewObject drawing small objects from the parser's chunks.
func (p *parser) newObject(n int) *Object {
	if p.sl == nil {
		return NewObject(n)
	}
	switch n {
	case 1:
		x := slabNext(&p.sl.po1, 32)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 2:
		x := slabNext(&p.sl.po2, 16)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 3:
		x := slabNext(&p.sl.po3, 10)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 4:
		x := slabNext(&p.sl.po4, 8)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 5:
		x := slabNext(&p.sl.po5, 6)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 6:
		x := slabNext(&p.sl.po6, 5)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 7:
		x := slabNext(&p.sl.po7, 4)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 8:
		x := slabNext(&p.sl.po8, 4)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 9:
		x := slabNext(&p.sl.po9, 8)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 10:
		x := slabNext(&p.sl.po10, 8)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 11:
		x := slabNext(&p.sl.po11, 8)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 12:
		x := slabNext(&p.sl.po12, 8)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	}
	return NewObject(n)
}
