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

type parser struct {
	s []byte
	i int
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
	o := NewObject(4)
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		return Obj(o), nil
	}
	for {
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			return Undefined, p.errf("expected object key")
		}
		k, err := p.str()
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
		o.Set(k, v)
		p.ws()
		if p.i >= len(p.s) {
			return Undefined, p.errf("unterminated object")
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			return Obj(o), nil
		default:
			return Undefined, p.errf("expected ',' or '}'")
		}
	}
}

func (p *parser) array(depth int) (Value, error) {
	p.i++ // [
	var items []Value
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return Arr(items), nil
	}
	for {
		p.ws()
		v, err := p.value(depth)
		if err != nil {
			return Undefined, err
		}
		items = append(items, v)
		p.ws()
		if p.i >= len(p.s) {
			return Undefined, p.errf("unterminated array")
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			return Arr(items), nil
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
	return appendJSON(dst, v, "", "")
}

// AppendJSONIndent is JSON.stringify(v, null, indent).
func AppendJSONIndent(dst []byte, v Value, indent string) []byte {
	return appendJSON(dst, v, indent, "\n")
}

// MarshalJSON writes compact JSON.stringify output.
func (v Value) MarshalJSON() ([]byte, error) {
	if v.k == KindUndefined {
		return []byte("null"), nil
	}
	return AppendJSON(nil, v), nil
}

func appendJSON(dst []byte, v Value, indent, prefix string) []byte {
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
			dst = appendJSON(dst, it, indent, inner)
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
			dst = appendJSON(dst, val, indent, inner)
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
