package text

import (
	"regexp"
	"strconv"
	"strings"
)

// canvasFontFamilyRe splits a font into what precedes the family list, up to
// the first size with a unit (and an optional line-height), and the list.
var canvasFontFamilyRe = regexp.MustCompile(`^(.*?\d[\d.]*(?:px|pt|em|rem|%)(?:\s*/\s*\S+)?\s+)(.+)$`)

// ValidCanvasFont reports whether a canvas 2D context accepts s as its font:
// node-canvas parses the CSS2 font shorthand strictly and, for a string it
// cannot parse, leaves the context's font as it was (the HTML canvas
// specification says the same). It is a port of node-canvas's FontParser
// (src/FontParser.cc): up to three of font-style, font-variant and
// font-weight in any order, a font-size with a unit, an optional line-height
// and a family list.
//
// The family list never makes a font invalid here: the engine resolves
// families itself (an unknown or unusable name falls back to the default
// family), so what precedes the family decides. The oracle that tests the
// engine resolves families the same way (testdata/oracle-node/oracle.mjs,
// mapFonts) and swaps a registered family in before the context sees the
// string; the family is replaced the same way for this check.
func ValidCanvasFont(s string) bool {
	if m := canvasFontFamilyRe.FindStringSubmatch(s); m != nil {
		s = m[1] + `"DejaVu Sans"`
	}
	p := &fontParser{tk: tokenizer{in: s}}
	p.cur = p.tk.next()
	p.nxt = p.tk.next()
	return p.parse()
}

type tokType int

const (
	tokInvalid tokType = iota
	tokEnd
	tokWhitespace
	tokNumber
	tokIdent
	tokString
	tokSlash
	tokComma
	tokPercent
)

type token struct {
	typ tokType
	str string
	num float64
}

const (
	cdSpace = 1 << iota
	cdNewline
	cdHex
	cdNmstart
	cdNmchar
	cdSign
	cdDigit
	cdNumStart
)

// charData classifies a byte as node-canvas's CharData.h does.
func charData(c byte) uint8 {
	switch {
	case c == '\t':
		return cdSpace
	case c == '\n', c == '\f', c == '\r':
		return cdSpace | cdNewline
	case c == ' ':
		return cdSpace
	case c == '+':
		return cdSign | cdNumStart
	case c == '-':
		return cdNmchar | cdSign | cdNumStart
	case c >= '0' && c <= '9':
		return cdNmchar | cdDigit | cdNumStart | cdHex
	case c >= 'A' && c <= 'F', c >= 'a' && c <= 'f':
		return cdNmstart | cdNmchar | cdHex
	case c >= 'G' && c <= 'Z', c >= 'g' && c <= 'z', c == '_':
		return cdNmstart | cdNmchar
	case c == '\\':
		return cdNmstart
	case c >= 128:
		return cdNmstart | cdNmchar
	}
	return 0
}

type tokenizer struct {
	in  string
	pos int
}

func (t *tokenizer) peek() byte {
	if t.pos < len(t.in) {
		return t.in[t.pos]
	}
	return 0
}

func (t *tokenizer) advance() byte {
	if t.pos < len(t.in) {
		c := t.in[t.pos]
		t.pos++
		return c
	}
	return 0
}

func (t *tokenizer) number() token {
	const (
		start = iota
		afterSign
		digits
		afterDecimal
		afterE
		afterESign
		expDigits
	)
	begin, ePos, state, valid := t.pos, 0, start, false
loop:
	for t.pos < len(t.in) {
		c := t.peek()
		fl := charData(c)
		switch state {
		case start:
			switch {
			case fl&cdSign != 0:
				t.pos++
				state = afterSign
			case fl&cdDigit != 0:
				t.pos++
				state, valid = digits, true
			case c == '.':
				t.pos++
				state = afterDecimal
			default:
				break loop
			}
		case afterSign:
			switch {
			case fl&cdDigit != 0:
				t.pos++
				state, valid = digits, true
			case c == '.':
				t.pos++
				state = afterDecimal
			default:
				break loop
			}
		case digits:
			switch {
			case fl&cdDigit != 0:
				t.pos++
			case c == '.':
				t.pos++
				state = afterDecimal
			case c == 'e' || c == 'E':
				ePos = t.pos
				t.pos++
				state, valid = afterE, false
			default:
				break loop
			}
		case afterDecimal:
			if fl&cdDigit != 0 {
				t.pos++
				state, valid = digits, true
			} else {
				break loop
			}
		case afterE:
			switch {
			case fl&cdSign != 0:
				t.pos++
				state = afterESign
			case fl&cdDigit != 0:
				t.pos++
				state, valid = expDigits, true
			default:
				t.pos, valid = ePos, true
				break loop
			}
		case afterESign:
			if fl&cdDigit != 0 {
				t.pos++
				state, valid = expDigits, true
			} else {
				t.pos, valid = ePos, true
				break loop
			}
		case expDigits:
			if fl&cdDigit != 0 {
				t.pos++
			} else {
				break loop
			}
		}
	}
	if !valid {
		t.pos = begin
		return token{typ: tokInvalid}
	}
	v, err := strconv.ParseFloat(t.in[begin:t.pos], 64)
	if err != nil && v == 0 {
		return token{typ: tokInvalid}
	}
	return token{typ: tokNumber, num: v}
}

func (t *tokenizer) ident() token {
	var b strings.Builder
	begin := t.pos
	flags := uint8(cdNmstart)
	for t.pos < len(t.in) {
		c := t.peek()
		switch {
		case c == '\\':
			t.advance()
			if !t.escape(&b) {
				t.pos = begin
				return token{typ: tokInvalid}
			}
			flags = cdNmchar
		case charData(c)&flags != 0:
			t.advance()
			if c >= 'A' && c <= 'Z' {
				c += 32
			}
			b.WriteByte(c)
			flags = cdNmchar
		default:
			return token{typ: tokIdent, str: b.String()}
		}
	}
	return token{typ: tokIdent, str: b.String()}
}

func (t *tokenizer) unicode() {
	for n := 0; t.pos < len(t.in) && n < 6; n++ {
		c := t.peek()
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			break
		}
		t.advance()
	}
	c := t.peek()
	if c == '\r' {
		t.advance()
		if t.peek() == '\n' {
			t.advance()
		}
	} else if charData(c)&cdSpace != 0 {
		t.advance()
	}
}

func (t *tokenizer) escape(b *strings.Builder) bool {
	c := t.peek()
	fl := charData(c)
	switch {
	case fl&cdHex != 0:
		start := t.pos
		t.unicode()
		b.WriteString(t.in[start:t.pos]) // the code point itself does not matter here
		return true
	case fl&cdNewline == 0:
		b.WriteByte(t.advance())
		return true
	}
	return false
}

func (t *tokenizer) str(quote byte) token {
	t.advance()
	var b strings.Builder
	begin := t.pos
	for t.pos < len(t.in) {
		c := t.peek()
		switch {
		case c == quote:
			t.advance()
			return token{typ: tokString, str: b.String()}
		case c == '\\':
			t.advance()
			c = t.peek()
			switch {
			case c == '\r':
				t.advance()
				if t.peek() == '\n' {
					t.advance()
				}
			case charData(c)&cdNewline != 0:
				t.advance()
			default:
				if !t.escape(&b) {
					t.pos = begin
					return token{typ: tokInvalid}
				}
			}
		default:
			b.WriteByte(t.advance())
		}
	}
	t.pos = begin
	return token{typ: tokInvalid}
}

func (t *tokenizer) next() token {
	if t.pos >= len(t.in) {
		return token{typ: tokEnd}
	}
	c := t.peek()
	fl := charData(c)
	if fl&cdSpace != 0 {
		for t.pos < len(t.in) && charData(t.peek())&cdSpace != 0 {
			t.pos++
		}
		return token{typ: tokWhitespace}
	}
	if fl&cdNumStart != 0 {
		if tk := t.number(); tk.typ != tokInvalid {
			return tk
		}
	}
	if fl&cdNmstart != 0 {
		if tk := t.ident(); tk.typ != tokInvalid {
			return tk
		}
	}
	if c == '"' {
		if tk := t.str('"'); tk.typ != tokInvalid {
			return tk
		}
	}
	if c == '\'' {
		if tk := t.str('\''); tk.typ != tokInvalid {
			return tk
		}
	}
	switch t.advance() {
	case '/':
		return token{typ: tokSlash}
	case ',':
		return token{typ: tokComma}
	case '%':
		return token{typ: tokPercent}
	}
	return token{typ: tokInvalid}
}

type fontParser struct {
	tk       tokenizer
	cur, nxt token
}

func (p *fontParser) advance() { p.cur, p.nxt = p.nxt, p.tk.next() }

func (p *fontParser) skipWS() {
	for p.cur.typ == tokWhitespace {
		p.advance()
	}
}

func (p *fontParser) checkWS() bool { return p.nxt.typ == tokWhitespace || p.nxt.typ == tokEnd }

func (p *fontParser) ident(names ...string) bool {
	if p.cur.typ != tokIdent {
		return false
	}
	for _, n := range names {
		if p.cur.str == n {
			p.advance()
			return true
		}
	}
	return false
}

func (p *fontParser) weight() bool {
	if p.cur.typ == tokNumber {
		w := int(p.cur.num)
		if w < 1 || w > 1000 {
			return false
		}
		p.advance()
		return true
	}
	return p.ident("normal", "bold", "lighter", "bolder")
}

func (p *fontParser) size() bool {
	if p.cur.typ != tokNumber {
		return false
	}
	p.advance()
	switch p.cur.typ {
	case tokIdent:
		switch p.cur.str {
		case "cm", "mm", "in", "pt", "pc", "em", "px":
			p.advance()
			return true
		}
		return false
	case tokPercent:
		p.advance()
		return true
	}
	return false
}

func (p *fontParser) lineHeight() bool {
	if p.cur.typ != tokSlash {
		return true
	}
	p.advance()
	p.skipWS()
	switch {
	case p.cur.typ == tokNumber:
		p.advance()
		switch p.cur.typ {
		case tokPercent:
			p.advance()
		case tokIdent:
			switch p.cur.str {
			case "cm", "mm", "in", "pt", "pc", "em", "px":
				p.advance()
			default:
				return false
			}
		default:
			return false
		}
	case p.cur.typ == tokIdent && p.cur.str == "normal":
		p.advance()
	default:
		return false
	}
	return true
}

func (p *fontParser) family() bool {
	for p.cur.typ != tokEnd {
		found := false
		for p.cur.typ == tokString || p.cur.typ == tokIdent || p.cur.typ == tokWhitespace {
			if p.cur.typ != tokWhitespace {
				found = true
			}
			p.advance()
		}
		if !found {
			return false
		}
		if p.cur.typ == tokComma {
			p.advance()
		}
	}
	return true
}

func (p *fontParser) parse() bool {
	state := 0b111
	p.skipWS()
	for i := 0; i < 3 && p.checkWS(); i++ {
		switch {
		case state&0b001 != 0 && p.ident("italic", "oblique", "normal"):
			state &= 0b110
		case state&0b010 != 0 && p.ident("small-caps", "normal"):
			state &= 0b101
		case state&0b100 != 0 && p.weight():
			state &= 0b011
		default:
			i = 3
			continue
		}
		p.skipWS()
	}
	if p.size() {
		p.skipWS()
		return p.lineHeight() && p.family()
	}
	return false
}
