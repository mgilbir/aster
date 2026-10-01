package expr

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/jsval"
)

// Limits on untrusted expression text. They are far above anything a real
// chart needs; they exist so a hostile specification cannot exhaust the stack
// or make compilation arbitrarily slow.
const (
	// MaxLength is the longest expression source, in bytes, that Parse accepts.
	MaxLength = 1 << 18
	// MaxDepth is the deepest nesting of the syntax tree Parse accepts. Left
	// associative chains (a+b+c+...) count one level per operator.
	MaxDepth = 512
)

// SyntaxError is a parse failure. Msg follows V8's wording, as upstream's
// parser does; Pos is the byte offset where scanning stopped.
type SyntaxError struct {
	Msg string
	Pos int
}

func (e *SyntaxError) Error() string { return e.Msg }

type tokKind uint8

const (
	tBool tokKind = iota + 1
	tEOF
	tIdent
	tKeyword
	tNull
	tNum
	tPunct
	tString
)

type token struct {
	kind   tokKind
	val    string  // identifier/keyword/punctuator text, string literal value
	num    float64 // numeric literal value
	octal  bool
	start  int
	end    int
	prec   int
	regexp bool
}

// keywords are the reserved words vega-expression's tokenizer recognises. Only
// `if` may be used as an identifier (the `if(test, a, b)` function); the rest
// make the parser fail with "Disabled." when they appear as a primary
// expression, but are still valid property names (`datum.in`).
var keywords = map[string]bool{
	"if": true, "in": true, "do": true,
	"var": true, "for": true, "new": true, "try": true, "let": true,
	"this": true, "else": true, "case": true, "void": true, "with": true, "enum": true,
	"while": true, "break": true, "catch": true, "throw": true, "const": true, "yield": true, "class": true, "super": true,
	"return": true, "typeof": true, "delete": true, "switch": true, "export": true, "import": true, "public": true, "static": true,
	"default": true, "finally": true, "extends": true, "package": true, "private": true,
	"function": true, "continue": true, "debugger": true,
	"interface": true, "protected": true,
	"instanceof": true, "implements": true,
}

type parser struct {
	src   string
	idx   int
	look  token
	depth int
}

// Parse parses a Vega expression. It accepts exactly what vega-expression
// accepts: literals (including regular expression literals), identifiers,
// member access, calls, array and object literals, the conditional operator,
// and the unary and binary operators of ECMAScript except ++, --, typeof, void,
// delete, assignment and the comma operator, which are rejected. `this` and all
// reserved words other than `if` are rejected.
func Parse(src string) (n *Node, err error) {
	if len(src) > MaxLength {
		return nil, &SyntaxError{Msg: "Expression too long"}
	}
	p := &parser{src: src}
	defer func() {
		if r := recover(); r != nil {
			se, ok := r.(*SyntaxError)
			if !ok {
				panic(r)
			}
			n, err = nil, se
		}
	}()
	p.look = p.advance()
	expr := p.parseExpression()
	if p.look.kind != tEOF {
		p.fail("Unexpect token after expression.")
	}
	return expr, nil
}

func (p *parser) fail(msg string) {
	panic(&SyntaxError{Msg: msg, Pos: p.idx})
}

func (p *parser) failf(format string, args ...any) {
	p.fail(fmt.Sprintf(format, args...))
}

func (p *parser) illegal() { p.fail("Unexpected token ILLEGAL") }

// ---- character classes ----

func isWhiteSpace(r rune) bool {
	switch r {
	case 0x20, 0x09, 0x0B, 0x0C, 0xA0, 0x1680, 0x180E, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

func isLineTerminator(r rune) bool {
	return r == 0x0A || r == 0x0D || r == 0x2028 || r == 0x2029
}

func isDecimalDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func isOctalDigit(c byte) bool { return c >= '0' && c <= '7' }

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	}
	return int(c-'A') + 10
}

// isIdentifierStart is upstream's: $ _ letters, backslash (an escape), and
// non-ASCII letters.
func isIdentifierStart(r rune) bool {
	if r < 0x80 {
		return r == '$' || r == '_' || r == '\\' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
	}
	return unicode.In(r, unicode.Lu, unicode.Ll, unicode.Lt, unicode.Lm, unicode.Lo, unicode.Nl)
}

func isIdentifierPart(r rune) bool {
	if r < 0x80 {
		return r == '$' || r == '_' || r == '\\' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
	}
	return r == 0x200C || r == 0x200D ||
		unicode.In(r, unicode.Lu, unicode.Ll, unicode.Lt, unicode.Lm, unicode.Lo, unicode.Nl, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc)
}

func (p *parser) runeAt(i int) (rune, int) {
	if i >= len(p.src) {
		return utf8.RuneError, 0
	}
	c := p.src[i]
	if c < utf8.RuneSelf {
		return rune(c), 1
	}
	return utf8.DecodeRuneInString(p.src[i:])
}

func (p *parser) byteAt(i int) byte {
	if i < len(p.src) {
		return p.src[i]
	}
	return 0
}

// ---- tokenizer ----

func (p *parser) skipSpace() {
	for p.idx < len(p.src) {
		r, w := p.runeAt(p.idx)
		if isWhiteSpace(r) || isLineTerminator(r) {
			p.idx += w
		} else {
			break
		}
	}
}

// scanHexEscape reads the 2 (\x) or 4 (\u) hex digits after the escape letter.
func (p *parser) scanHexEscape(prefix byte) rune {
	n := 4
	if prefix != 'u' {
		n = 2
	}
	code := 0
	for i := 0; i < n; i++ {
		if p.idx < len(p.src) && isHexDigit(p.src[p.idx]) {
			code = code*16 + hexVal(p.src[p.idx])
			p.idx++
		} else {
			p.illegal()
		}
	}
	return rune(code)
}

func (p *parser) scanCodePointEscape() rune {
	if p.byteAt(p.idx) == '}' {
		p.illegal()
	}
	code := 0
	var ch byte
	for p.idx < len(p.src) {
		ch = p.src[p.idx]
		p.idx++
		if !isHexDigit(ch) {
			break
		}
		code = code*16 + hexVal(ch)
		if code > 0x10FFFF {
			p.illegal()
		}
	}
	if ch != '}' {
		p.illegal()
	}
	return rune(code)
}

func (p *parser) escapedIdentifier() string {
	var b strings.Builder
	r, w := p.runeAt(p.idx)
	p.idx += w
	if r == '\\' {
		if p.byteAt(p.idx) != 'u' {
			p.illegal()
		}
		p.idx++
		r = p.scanHexEscape('u')
		if r == '\\' || !isIdentifierStart(r) {
			p.illegal()
		}
	}
	b.WriteRune(r)
	for p.idx < len(p.src) {
		r, w = p.runeAt(p.idx)
		if !isIdentifierPart(r) {
			break
		}
		p.idx += w
		if r == '\\' {
			if p.byteAt(p.idx) != 'u' {
				p.illegal()
			}
			p.idx++
			r = p.scanHexEscape('u')
			if r == '\\' || !isIdentifierPart(r) {
				p.illegal()
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (p *parser) scanIdentifier() token {
	start := p.idx
	var id string
	if p.src[p.idx] == '\\' {
		id = p.escapedIdentifier()
	} else {
		_, w0 := p.runeAt(p.idx) // the start character was validated by the caller
		p.idx += w0
		for p.idx < len(p.src) {
			r, w := p.runeAt(p.idx)
			if r == '\\' {
				p.idx = start
				id = p.escapedIdentifier()
				goto done
			}
			if !isIdentifierPart(r) {
				break
			}
			p.idx += w
		}
		id = p.src[start:p.idx]
	}
done:
	t := token{val: id, start: start, end: p.idx}
	switch {
	case utf8.RuneCountInString(id) == 1:
		t.kind = tIdent
	case keywords[id]:
		t.kind = tKeyword
	case id == "null":
		t.kind = tNull
	case id == "true" || id == "false":
		t.kind = tBool
	default:
		t.kind = tIdent
	}
	return t
}

func (p *parser) scanPunctuator() token {
	start := p.idx
	c := p.src[p.idx]
	mk := func(n int) token {
		p.idx += n
		return token{kind: tPunct, val: p.src[start:p.idx], start: start, end: p.idx}
	}
	switch c {
	case '.', '(', ')', ';', ',', '{', '}', '[', ']', ':', '?', '~':
		return mk(1)
	}
	rest := p.src[p.idx:]
	c2 := p.byteAt(p.idx + 1)
	if c2 == '=' {
		switch c {
		case '+', '-', '/', '<', '>', '^', '|', '%', '&', '*':
			return mk(2)
		case '!', '=':
			if p.byteAt(p.idx+2) == '=' {
				return mk(3)
			}
			return mk(2)
		}
	}
	switch {
	case strings.HasPrefix(rest, ">>>="):
		return mk(4)
	case strings.HasPrefix(rest, ">>>"), strings.HasPrefix(rest, "<<="), strings.HasPrefix(rest, ">>="):
		return mk(3)
	}
	if c == c2 && strings.IndexByte("+-<>&|", c) >= 0 || c == '=' && c2 == '>' {
		return mk(2)
	}
	if c == '/' && c2 == '/' {
		p.illegal()
	}
	if strings.IndexByte("<>=!+-*%&|^/", c) >= 0 {
		return mk(1)
	}
	p.illegal()
	return token{}
}

func (p *parser) scanNumber() token {
	start := p.idx
	ch := p.byteAt(p.idx)
	if ch != '.' {
		p.idx++
		ch = p.byteAt(p.idx)
		if p.src[start] == '0' {
			if ch == 'x' || ch == 'X' {
				p.idx++
				return p.scanHex(start)
			}
			if isOctalDigit(ch) {
				return p.scanOctal(start)
			}
			if isDecimalDigit(ch) {
				p.illegal() // "09" is illegal
			}
		}
		for isDecimalDigit(p.byteAt(p.idx)) {
			p.idx++
		}
		ch = p.byteAt(p.idx)
	}
	if ch == '.' {
		p.idx++
		for isDecimalDigit(p.byteAt(p.idx)) {
			p.idx++
		}
		ch = p.byteAt(p.idx)
	}
	if ch == 'e' || ch == 'E' {
		p.idx++
		ch = p.byteAt(p.idx)
		if ch == '+' || ch == '-' {
			p.idx++
		}
		if !isDecimalDigit(p.byteAt(p.idx)) {
			p.illegal()
		}
		for isDecimalDigit(p.byteAt(p.idx)) {
			p.idx++
		}
	}
	if r, _ := p.runeAt(p.idx); p.idx < len(p.src) && isIdentifierStart(r) {
		p.illegal()
	}
	v, err := strconv.ParseFloat(p.src[start:p.idx], 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); !ok || ne.Err != strconv.ErrRange {
			p.illegal()
		}
	}
	return token{kind: tNum, num: v, start: start, end: p.idx}
}

func (p *parser) scanHex(start int) token {
	b := p.idx
	for p.idx < len(p.src) && isHexDigit(p.src[p.idx]) {
		p.idx++
	}
	if p.idx == b {
		p.illegal()
	}
	if r, _ := p.runeAt(p.idx); p.idx < len(p.src) && isIdentifierStart(r) {
		p.illegal()
	}
	var z big.Int
	z.SetString(p.src[b:p.idx], 16)
	f, _ := new(big.Float).SetInt(&z).Float64()
	return token{kind: tNum, num: f, start: start, end: p.idx}
}

// scanOctal consumes a legacy octal literal (0755). Upstream tokenizes it and
// then rejects it in the parser ("Octal literals are not allowed in strict
// mode."), so the value is never used.
func (p *parser) scanOctal(start int) token {
	p.idx++
	for p.idx < len(p.src) && isOctalDigit(p.src[p.idx]) {
		p.idx++
	}
	if r, _ := p.runeAt(p.idx); p.idx < len(p.src) && (isIdentifierStart(r) || isDecimalDigit(p.src[p.idx])) {
		p.illegal()
	}
	return token{kind: tNum, octal: true, start: start, end: p.idx}
}

func (p *parser) scanString() token {
	quote := p.src[p.idx]
	start := p.idx
	p.idx++
	var units []rune // UTF-16 code units held as runes so 😀 recombines
	octal := false
	closed := false
	for p.idx < len(p.src) {
		r, w := p.runeAt(p.idx)
		p.idx += w
		if r == rune(quote) {
			closed = true
			break
		}
		if r == '\\' {
			if p.idx >= len(p.src) {
				break
			}
			ch, cw := p.runeAt(p.idx)
			p.idx += cw
			if isLineTerminator(ch) {
				if ch == '\r' && p.byteAt(p.idx) == '\n' {
					p.idx++
				}
				continue
			}
			switch ch {
			case 'u', 'x':
				if p.byteAt(p.idx) == '{' {
					p.idx++
					units = appendCodePoint(units, p.scanCodePointEscape())
				} else {
					units = append(units, p.scanHexEscape(byte(ch)))
				}
			case 'n':
				units = append(units, '\n')
			case 'r':
				units = append(units, '\r')
			case 't':
				units = append(units, '\t')
			case 'b':
				units = append(units, '\b')
			case 'f':
				units = append(units, '\f')
			case 'v':
				units = append(units, 0x0B)
			default:
				if ch < 0x80 && isOctalDigit(byte(ch)) {
					code := int(ch - '0')
					if code != 0 {
						octal = true
					}
					if p.idx < len(p.src) && isOctalDigit(p.src[p.idx]) {
						octal = true
						code = code*8 + int(p.src[p.idx]-'0')
						p.idx++
						if ch <= '3' && p.idx < len(p.src) && isOctalDigit(p.src[p.idx]) {
							code = code*8 + int(p.src[p.idx]-'0')
							p.idx++
						}
					}
					units = append(units, rune(code))
				} else {
					units = append(units, ch)
				}
			}
			continue
		}
		if isLineTerminator(r) {
			break
		}
		units = appendCodePoint(units, r)
	}
	if !closed {
		p.illegal()
	}
	return token{kind: tString, val: unitsToString(units), octal: octal, start: start, end: p.idx}
}

// appendCodePoint appends r as UTF-16 code units (held in a []rune).
func appendCodePoint(units []rune, r rune) []rune {
	if r >= 0x10000 {
		r -= 0x10000
		return append(units, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
	}
	return append(units, r)
}

// unitsToString converts UTF-16 code units to a Go string. A surrogate with no
// partner is kept as three WTF-8 bytes (see fromUTF16) so that string functions
// see the same code units JavaScript does.
func unitsToString(units []rune) string {
	u := make([]uint16, len(units))
	for i, r := range units {
		u[i] = uint16(r)
	}
	return fromUTF16(u)
}

func (p *parser) advance() token {
	p.skipSpace()
	if p.idx >= len(p.src) {
		return token{kind: tEOF, start: p.idx, end: p.idx}
	}
	r, _ := p.runeAt(p.idx)
	if isIdentifierStart(r) {
		return p.scanIdentifier()
	}
	c := p.src[p.idx]
	switch {
	case c == '(' || c == ')' || c == ';':
		return p.scanPunctuator()
	case c == '\'' || c == '"':
		return p.scanString()
	case c == '.':
		if isDecimalDigit(p.byteAt(p.idx + 1)) {
			return p.scanNumber()
		}
		return p.scanPunctuator()
	case isDecimalDigit(c):
		return p.scanNumber()
	}
	return p.scanPunctuator()
}

func (p *parser) lex() token {
	t := p.look
	p.idx = t.end
	p.look = p.advance()
	p.idx = t.end
	return t
}

// peekAt re-scans the lookahead from the current position (after a regular
// expression literal was read by hand).
func (p *parser) peekAgain() {
	pos := p.idx
	p.look = p.advance()
	p.idx = pos
}

func (p *parser) match(v string) bool { return p.look.kind == tPunct && p.look.val == v }

func (p *parser) matchKeyword(k string) bool { return p.look.kind == tKeyword && p.look.val == k }

func (p *parser) unexpected(t token) {
	switch t.kind {
	case tEOF:
		p.fail("Unexpected end of input")
	case tNum:
		p.fail("Unexpected number")
	case tString:
		p.fail("Unexpected string")
	case tIdent:
		p.fail("Unexpected identifier")
	case tKeyword:
		p.fail("Unexpected reserved word")
	}
	p.failf("Unexpected token %s", t.val)
}

func (p *parser) expect(v string) {
	t := p.lex()
	if t.kind != tPunct || t.val != v {
		p.unexpected(t)
	}
}

// ---- regular expression literals ----

func (p *parser) scanRegExp() (*jsval.Pattern, string) {
	p.skipSpace()
	start := p.idx
	if p.byteAt(p.idx) != '/' {
		p.illegal()
	}
	p.idx++
	inClass := false
	terminated := false
	bodyStart := p.idx
	bodyEnd := p.idx
	for p.idx < len(p.src) {
		r, w := p.runeAt(p.idx)
		p.idx += w
		if r == '\\' {
			if p.idx >= len(p.src) {
				break
			}
			c, cw := p.runeAt(p.idx)
			p.idx += cw
			if isLineTerminator(c) {
				p.fail("Invalid regular expression: missing /")
			}
		} else if isLineTerminator(r) {
			p.fail("Invalid regular expression: missing /")
		} else if inClass {
			if r == ']' {
				inClass = false
			}
		} else if r == '/' {
			terminated = true
			bodyEnd = p.idx - 1
			break
		} else if r == '[' {
			inClass = true
		}
	}
	if !terminated {
		p.fail("Invalid regular expression: missing /")
	}
	body := p.src[bodyStart:bodyEnd]
	flagStart := p.idx
	for p.idx < len(p.src) {
		r, w := p.runeAt(p.idx)
		if !isIdentifierPart(r) {
			break
		}
		if r == '\\' {
			p.illegal()
		}
		p.idx += w
	}
	flags := p.src[flagStart:p.idx]
	// Vega 6's tokenizer only accepts these flags.
	for i := 0; i < len(flags); i++ {
		if strings.IndexByte("gimuy", flags[i]) < 0 {
			p.fail("Invalid regular expression")
		}
	}
	pat, err := jsval.NewPattern(body, canonicalFlags(flags))
	if err != nil {
		p.fail("Invalid regular expression")
	}
	return pat, p.src[start:p.idx]
}

// ---- tree constructors ----

func (p *parser) node(n *Node, kids ...*Node) *Node {
	d := 0
	for _, k := range kids {
		if k != nil && k.depth > d {
			d = k.depth
		}
	}
	for _, k := range n.Elems {
		if k.depth > d {
			d = k.depth
		}
	}
	n.depth = d + 1
	if n.depth > MaxDepth {
		p.fail("Expression is nested too deeply")
	}
	return n
}

func identNode(name string) *Node { return &Node{Kind: KindIdentifier, Name: name, depth: 1} }

func (p *parser) literalNode(t token, src string) *Node {
	n := &Node{Kind: KindLiteral, Raw: src[t.start:t.end], depth: 1}
	switch t.kind {
	case tString:
		n.Value = jsval.Str(t.val)
	case tNum:
		n.Value = jsval.Num(t.num)
	case tBool:
		n.Value = jsval.Bool(t.val == "true")
	case tNull:
		n.Value = jsval.Null
	}
	return n
}

// ---- grammar ----

func (p *parser) parseArray() *Node {
	p.idx = p.look.start
	p.expect("[")
	var elems []*Node
	for !p.match("]") {
		if p.match(",") {
			// An elision. Upstream parses it as a null element and then fails
			// in code generation; either way the expression is rejected.
			p.fail("Unexpected token ,")
		}
		elems = append(elems, p.parseConditional())
		if !p.match("]") {
			p.expect(",")
		}
	}
	p.lex()
	return p.node(&Node{Kind: KindArray, Elems: elems})
}

func (p *parser) parseObjectKey() *Node {
	p.idx = p.look.start
	t := p.lex()
	if t.kind == tString || t.kind == tNum {
		if t.octal {
			p.fail("Octal literals are not allowed in strict mode.")
		}
		return p.literalNode(t, p.src)
	}
	return identNode(t.val)
}

func (p *parser) parseObjectProperty() *Node {
	p.idx = p.look.start
	if p.look.kind == tEOF || p.look.kind == tPunct {
		p.unexpected(p.look)
	}
	key := p.parseObjectKey()
	p.expect(":")
	val := p.parseConditional()
	return p.node(&Node{Kind: KindProperty, Key: key, Right: val}, val)
}

func (p *parser) parseObject() *Node {
	p.idx = p.look.start
	p.expect("{")
	var props []*Node
	seen := map[string]struct{}{}
	for !p.match("}") {
		prop := p.parseObjectProperty()
		var name string
		if prop.Key.Kind == KindIdentifier {
			name = prop.Key.Name
		} else {
			name = prop.Key.Value.AsString()
		}
		if _, dup := seen[name]; dup {
			p.fail("Duplicate data property in object literal not allowed in strict mode")
		}
		seen[name] = struct{}{}
		props = append(props, prop)
		if !p.match("}") {
			p.expect(",")
		}
	}
	p.expect("}")
	return p.node(&Node{Kind: KindObject, Elems: props})
}

func (p *parser) parseGroup() *Node {
	p.expect("(")
	e := p.parseExpression()
	p.expect(")")
	return e
}

func (p *parser) parsePrimary() *Node {
	if p.match("(") {
		return p.parseGroup()
	}
	if p.match("[") {
		return p.parseArray()
	}
	if p.match("{") {
		return p.parseObject()
	}
	t := p.look
	p.idx = t.start
	switch {
	case t.kind == tIdent || t.kind == tKeyword && t.val == "if":
		return identNode(p.lex().val)
	case t.kind == tString && (t.val == "if" || disallowedProperties[t.val]):
		// upstream tests `legalKeywords[lookahead.value]` on a plain object
		// for every token: a string literal whose value is "if" or the name
		// of an Object.prototype property is read as an identifier.
		return identNode(p.lex().val)
	case t.kind == tString || t.kind == tNum:
		if t.octal {
			p.fail("Octal literals are not allowed in strict mode.")
		}
		return p.literalNode(p.lex(), p.src)
	case t.kind == tKeyword:
		p.fail("Disabled.")
	case t.kind == tBool || t.kind == tNull:
		return p.literalNode(p.lex(), p.src)
	case p.match("/") || p.match("/="):
		pat, raw := p.scanRegExp()
		n := &Node{Kind: KindLiteral, Raw: raw, Value: jsval.PatternValue(pat), Regex: pat, depth: 1}
		p.peekAgain()
		return n
	}
	p.unexpected(p.lex())
	return nil
}

func (p *parser) parseArguments() []*Node {
	var args []*Node
	p.expect("(")
	if !p.match(")") {
		for p.idx < len(p.src) || p.look.kind != tEOF {
			args = append(args, p.parseConditional())
			if p.match(")") {
				break
			}
			p.expect(",")
		}
	}
	p.expect(")")
	return args
}

func (p *parser) parseLeftHandSide() *Node {
	expr := p.parsePrimary()
	for {
		switch {
		case p.match("."):
			p.expect(".")
			p.idx = p.look.start
			t := p.lex()
			if t.kind != tIdent && t.kind != tKeyword && t.kind != tBool && t.kind != tNull {
				p.unexpected(t)
			}
			expr = p.node(&Node{Kind: KindMember, Left: expr, Right: identNode(t.val)}, expr)
		case p.match("("):
			args := p.parseArguments()
			expr = p.node(&Node{Kind: KindCall, Left: expr, Elems: args}, expr)
		case p.match("["):
			p.expect("[")
			prop := p.parseExpression()
			p.expect("]")
			expr = p.node(&Node{Kind: KindMember, Left: expr, Right: prop, Computed: true}, expr, prop)
		default:
			return expr
		}
	}
}

func (p *parser) parsePostfix() *Node {
	expr := p.parseLeftHandSide()
	if p.look.kind == tPunct && (p.match("++") || p.match("--")) {
		p.fail("Disabled.")
	}
	return expr
}

func (p *parser) parseUnary() *Node {
	p.depth++
	if p.depth > MaxDepth {
		p.fail("Expression is nested too deeply")
	}
	defer func() { p.depth-- }()
	if p.look.kind != tPunct && p.look.kind != tKeyword {
		return p.parsePostfix()
	}
	switch {
	case p.match("++") || p.match("--"):
		p.fail("Disabled.")
	case p.match("+") || p.match("-") || p.match("~") || p.match("!"):
		op := p.lex().val
		arg := p.parseUnary()
		return p.node(&Node{Kind: KindUnary, Op: op, Left: arg}, arg)
	case p.matchKeyword("delete") || p.matchKeyword("void") || p.matchKeyword("typeof"):
		p.fail("Disabled.")
	}
	return p.parsePostfix()
}

func binaryPrecedence(t token) int {
	if t.kind != tPunct && t.kind != tKeyword {
		return 0
	}
	switch t.val {
	case "||":
		return 1
	case "&&":
		return 2
	case "|":
		return 3
	case "^":
		return 4
	case "&":
		return 5
	case "==", "!=", "===", "!==":
		return 6
	case "<", ">", "<=", ">=", "instanceof", "in":
		return 7
	case "<<", ">>", ">>>":
		return 8
	case "+", "-":
		return 9
	case "*", "/", "%":
		return 11
	}
	return 0
}

func (p *parser) binaryNode(op string, l, r *Node) *Node {
	k := KindBinary
	if op == "||" || op == "&&" {
		k = KindLogical
	}
	return p.node(&Node{Kind: k, Op: op, Left: l, Right: r}, l, r)
}

// parseBinary is upstream's operator-precedence (shift/reduce) parser, so
// long chains of operators do not recurse.
func (p *parser) parseBinary() *Node {
	left := p.parseUnary()
	tok := p.look
	prec := binaryPrecedence(tok)
	if prec == 0 {
		return left
	}
	tok.prec = prec
	p.lex()
	right := p.parseUnary()
	// operands[i] is separated from operands[i+1] by ops[i].
	operands := []*Node{left, right}
	ops := []token{tok}
	for {
		prec = binaryPrecedence(p.look)
		if prec == 0 {
			break
		}
		for len(ops) > 0 && prec <= ops[len(ops)-1].prec {
			n := len(operands)
			r, l := operands[n-1], operands[n-2]
			operands = operands[:n-2]
			operands = append(operands, p.binaryNode(ops[len(ops)-1].val, l, r))
			ops = ops[:len(ops)-1]
		}
		t := p.lex()
		t.prec = prec
		ops = append(ops, t)
		operands = append(operands, p.parseUnary())
	}
	expr := operands[len(operands)-1]
	for i := len(ops) - 1; i >= 0; i-- {
		expr = p.binaryNode(ops[i].val, operands[i], expr)
	}
	return expr
}

func (p *parser) parseConditional() *Node {
	p.depth++
	if p.depth > MaxDepth {
		p.fail("Expression is nested too deeply")
	}
	defer func() { p.depth-- }()
	expr := p.parseBinary()
	if p.match("?") {
		p.lex()
		cons := p.parseConditional()
		p.expect(":")
		alt := p.parseConditional()
		expr = p.node(&Node{Kind: KindConditional, Test: expr, Left: cons, Right: alt}, expr, cons, alt)
	}
	return expr
}

func (p *parser) parseExpression() *Node {
	expr := p.parseConditional()
	if p.match(",") {
		p.fail("Disabled.") // no sequence expressions
	}
	return expr
}
