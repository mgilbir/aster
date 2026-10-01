package vegalite

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/jsval"
)

// A small parser for the Vega expression language, used only to find which
// datum fields an expression reads (upstream's getDependentFields). It accepts
// the expression grammar of vega-expression — literals, identifiers, member
// and call expressions, unary/binary/logical/conditional operators, array and
// object literals — and reports a syntax error where upstream's parser throws.

type exprNode struct {
	kind     string // Identifier, Literal, Member, Call, Binary, Unary, Conditional, Array, Object, Property
	name     string // Identifier name
	lit      string // Literal text as JavaScript would join() it
	object   *exprNode
	property *exprNode
	kids     []*exprNode // generic children in visit order
	depth    int         // levels of the tree below and including this node
}

type exprParser struct {
	src   string
	pos   int
	tok   string
	kind  string // "num","str","id","punct","eof","regex"
	val   string
	depth int
}

const maxExprDepth = 200

// maxExprNodeDepth bounds the depth of the syntax tree. maxExprDepth bounds
// the parser's own recursion, but a left-associative chain (a+b+c+..., a.b.c,
// f()()()) is built in a loop and nests one level per operator in the tree,
// which exprNode.visit and exprNames then walk recursively. It matches
// expr.MaxDepth, which Vega's parser enforces on the same text later.
const maxExprNodeDepth = 512

// mk finishes a node that has children: it records its depth and fails when
// the tree is too deep.
func (p *exprParser) mk(n *exprNode) *exprNode {
	d := 0
	for _, c := range n.children() {
		if c != nil && c.depth > d {
			d = c.depth
		}
	}
	n.depth = d + 1
	if n.depth > maxExprNodeDepth {
		p.failLimit("expression too deeply nested")
	}
	return n
}

type exprSyntaxError struct {
	msg   string
	limit bool // the text exceeds the parser's limits
}

func (p *exprParser) fail(msg string)      { panic(exprSyntaxError{msg: msg}) }
func (p *exprParser) failLimit(msg string) { panic(exprSyntaxError{msg: msg, limit: true}) }

// err is the error Compile reports for the syntax error.
func (e exprSyntaxError) err() error {
	if e.limit {
		return limitError{"Invalid expression: " + e.msg}
	}
	return compileError{"Invalid expression: " + e.msg}
}

func (p *exprParser) skipSpace() {
	for p.pos < len(p.src) {
		r, w := utf8.DecodeRuneInString(p.src[p.pos:])
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f' || r == 0xA0 || r == 0xFEFF || r == 0x2028 || r == 0x2029 || unicode.Is(unicode.Zs, r) {
			p.pos += w
			continue
		}
		break
	}
}

var puncts3 = []string{">>>", "===", "!=="}
var puncts2 = []string{"==", "!=", "<=", ">=", "&&", "||", "<<", ">>"}

func isIDStart(r rune) bool { return r == '$' || r == '_' || unicode.IsLetter(r) }
func isIDPart(r rune) bool {
	return isIDStart(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r)
}

// next advances to the next token. regexOK says a '/' starts a regex literal.
func (p *exprParser) next(regexOK bool) {
	p.skipSpace()
	if p.pos >= len(p.src) {
		p.kind, p.tok, p.val = "eof", "", ""
		return
	}
	c, w := utf8.DecodeRuneInString(p.src[p.pos:])
	switch {
	case isIDStart(c) || c == '\\':
		start := p.pos
		p.pos += w
		for p.pos < len(p.src) {
			r, w2 := utf8.DecodeRuneInString(p.src[p.pos:])
			if !isIDPart(r) {
				break
			}
			p.pos += w2
		}
		p.kind, p.val = "id", p.src[start:p.pos]
		p.tok = p.val
	case c >= '0' && c <= '9' || (c == '.' && p.pos+1 < len(p.src) && p.src[p.pos+1] >= '0' && p.src[p.pos+1] <= '9'):
		start := p.pos
		if c == '0' && p.pos+1 < len(p.src) && (p.src[p.pos+1] == 'x' || p.src[p.pos+1] == 'X') {
			p.pos += 2
			for p.pos < len(p.src) && strings.ContainsRune("0123456789abcdefABCDEF", rune(p.src[p.pos])) {
				p.pos++
			}
		} else {
			for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
				p.pos++
			}
			if p.pos < len(p.src) && p.src[p.pos] == '.' {
				p.pos++
				for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
					p.pos++
				}
			}
			if p.pos < len(p.src) && (p.src[p.pos] == 'e' || p.src[p.pos] == 'E') {
				q := p.pos + 1
				if q < len(p.src) && (p.src[q] == '+' || p.src[q] == '-') {
					q++
				}
				if q < len(p.src) && p.src[q] >= '0' && p.src[q] <= '9' {
					for q < len(p.src) && p.src[q] >= '0' && p.src[q] <= '9' {
						q++
					}
					p.pos = q
				} else {
					p.fail("Unexpected token ILLEGAL")
				}
			}
		}
		if p.pos < len(p.src) {
			if r, _ := utf8.DecodeRuneInString(p.src[p.pos:]); isIDStart(r) {
				p.fail("Unexpected token ILLEGAL")
			}
		}
		p.kind, p.val = "num", p.src[start:p.pos]
		p.tok = p.val
	case c == '\'' || c == '"':
		p.pos += w
		var sb strings.Builder
		for {
			if p.pos >= len(p.src) {
				p.fail("Unexpected token ILLEGAL")
			}
			r, w2 := utf8.DecodeRuneInString(p.src[p.pos:])
			p.pos += w2
			if r == c {
				break
			}
			if r == '\n' || r == '\r' {
				p.fail("Unexpected token ILLEGAL")
			}
			if r == '\\' {
				if p.pos >= len(p.src) {
					p.fail("Unexpected token ILLEGAL")
				}
				e, w3 := utf8.DecodeRuneInString(p.src[p.pos:])
				p.pos += w3
				switch e {
				case 'n':
					sb.WriteByte('\n')
				case 't':
					sb.WriteByte('\t')
				case 'r':
					sb.WriteByte('\r')
				case 'b':
					sb.WriteByte('\b')
				case 'f':
					sb.WriteByte('\f')
				case 'v':
					sb.WriteByte('\v')
				case 'x':
					if p.pos+2 <= len(p.src) {
						if n, err := strconv.ParseUint(p.src[p.pos:p.pos+2], 16, 32); err == nil {
							sb.WriteRune(rune(n))
							p.pos += 2
						}
					}
				case 'u':
					if p.pos+4 <= len(p.src) {
						if n, err := strconv.ParseUint(p.src[p.pos:p.pos+4], 16, 32); err == nil {
							sb.WriteRune(rune(n))
							p.pos += 4
						}
					}
				case '\n', '\r':
				default:
					sb.WriteRune(e)
				}
				continue
			}
			sb.WriteRune(r)
		}
		p.kind, p.val = "str", sb.String()
		p.tok = "'"
	case c == '/' && regexOK:
		start := p.pos
		p.pos++
		inClass := false
		for {
			if p.pos >= len(p.src) {
				p.fail("Invalid regular expression: missing /")
			}
			r := p.src[p.pos]
			p.pos++
			if r == '\\' {
				p.pos++
				continue
			}
			if r == '[' {
				inClass = true
			} else if r == ']' {
				inClass = false
			} else if r == '/' && !inClass {
				break
			}
		}
		for p.pos < len(p.src) {
			r, w2 := utf8.DecodeRuneInString(p.src[p.pos:])
			if !isIDPart(r) {
				break
			}
			p.pos += w2
		}
		p.kind, p.val = "regex", p.src[start:p.pos]
		p.tok = p.val
	default:
		rest := p.src[p.pos:]
		for _, t := range puncts3 {
			if strings.HasPrefix(rest, t) {
				p.kind, p.tok, p.val = "punct", t, t
				p.pos += 3
				return
			}
		}
		for _, t := range puncts2 {
			if strings.HasPrefix(rest, t) {
				p.kind, p.tok, p.val = "punct", t, t
				p.pos += 2
				return
			}
		}
		if strings.ContainsRune("(){}[],.;:?+-*/%!~<>=&|^", c) {
			p.kind, p.tok, p.val = "punct", string(c), string(c)
			p.pos += w
			return
		}
		p.fail("Unexpected token ILLEGAL")
	}
}

func (p *exprParser) isP(t string) bool { return p.kind == "punct" && p.tok == t }

func (p *exprParser) expect(t string) {
	if !p.isP(t) {
		p.fail("Unexpected token " + p.tok)
	}
	p.next(true)
}

// reserved words the expression language does not allow as identifiers.
var exprDisabled = strSet([]string{
	"do", "in", "var", "for", "new", "try", "let", "this", "else", "case", "void", "with", "enum", "while",
	"break", "catch", "throw", "const", "yield", "class", "super", "return", "typeof", "delete", "switch",
	"export", "import", "public", "static", "default", "finally", "extends", "package", "private", "function",
	"continue", "debugger", "interface", "protected", "instanceof", "implements",
})

func parseExprAST(src string) (root *exprNode, err error) {
	defer func() {
		if r := recover(); r != nil {
			if se, ok := r.(exprSyntaxError); ok {
				root, err = nil, se.err()
				return
			}
			panic(r)
		}
	}()
	p := &exprParser{src: src}
	p.next(true)
	root = p.parseConditional()
	if p.kind != "eof" {
		p.fail("Unexpected token " + p.tok)
	}
	return root, nil
}

func (p *exprParser) parseConditional() *exprNode {
	p.depth++
	if p.depth > maxExprDepth {
		p.failLimit("expression too deeply nested")
	}
	defer func() { p.depth-- }()
	test := p.parseBinary(0)
	if p.isP("?") {
		p.next(true)
		cons := p.parseConditional()
		p.expect(":")
		alt := p.parseConditional()
		return p.mk(&exprNode{kind: "Conditional", kids: []*exprNode{test, cons, alt}})
	}
	return test
}

var binPrec = map[string]int{
	"||": 1, "&&": 2, "|": 3, "^": 4, "&": 5,
	"==": 6, "!=": 6, "===": 6, "!==": 6,
	"<": 7, ">": 7, "<=": 7, ">=": 7,
	"<<": 8, ">>": 8, ">>>": 8,
	"+": 9, "-": 9, "*": 10, "/": 10, "%": 10,
}

func (p *exprParser) parseBinary(minPrec int) *exprNode {
	left := p.parseUnary()
	for p.kind == "punct" {
		prec, ok := binPrec[p.tok]
		if !ok || prec <= minPrec {
			break
		}
		p.next(true)
		right := p.parseBinary(prec)
		left = p.mk(&exprNode{kind: "Binary", kids: []*exprNode{left, right}})
	}
	return left
}

func (p *exprParser) parseUnary() *exprNode {
	p.depth++
	if p.depth > maxExprDepth {
		p.failLimit("expression too deeply nested")
	}
	defer func() { p.depth-- }()
	if p.kind == "punct" && (p.tok == "+" || p.tok == "-" || p.tok == "!" || p.tok == "~") {
		p.next(true)
		arg := p.parseUnary()
		return p.mk(&exprNode{kind: "Unary", kids: []*exprNode{arg}})
	}
	return p.parsePostfix()
}

func (p *exprParser) parsePostfix() *exprNode {
	expr := p.parsePrimary()
	for {
		switch {
		case p.isP("."):
			p.next(false)
			if p.kind != "id" {
				p.fail("Unexpected token " + p.tok)
			}
			prop := &exprNode{kind: "Identifier", name: p.val}
			p.next(false)
			expr = p.mk(&exprNode{kind: "Member", object: expr, property: prop})
		case p.isP("["):
			p.next(true)
			prop := p.parseConditional()
			if p.isP("]") {
				p.next(false)
			} else {
				p.fail("Unexpected token " + p.tok)
			}
			expr = p.mk(&exprNode{kind: "Member", object: expr, property: prop})
		case p.isP("("):
			p.next(true)
			kids := []*exprNode{expr}
			if !p.isP(")") {
				for {
					kids = append(kids, p.parseConditional())
					if p.isP(",") {
						p.next(true)
						continue
					}
					break
				}
			}
			if !p.isP(")") {
				p.fail("Unexpected token " + p.tok)
			}
			p.next(false)
			expr = p.mk(&exprNode{kind: "Call", kids: kids})
		default:
			return expr
		}
	}
}

func (p *exprParser) parsePrimary() *exprNode {
	switch p.kind {
	case "num":
		n := &exprNode{kind: "Literal", lit: numLiteralString(p.val)}
		p.next(false)
		return n
	case "str":
		n := &exprNode{kind: "Literal", lit: p.val}
		p.next(false)
		return n
	case "regex":
		n := &exprNode{kind: "Literal", lit: p.val}
		p.next(false)
		return n
	case "id":
		name := p.val
		if exprDisabled[name] {
			p.fail("Unexpected reserved word")
		}
		p.next(false)
		switch name {
		case "true", "false", "null":
			return &exprNode{kind: "Literal", lit: name}
		}
		return &exprNode{kind: "Identifier", name: name}
	case "punct":
		switch p.tok {
		case "(":
			p.next(true)
			e := p.parseConditional()
			if !p.isP(")") {
				p.fail("Unexpected token " + p.tok)
			}
			p.next(false)
			return e
		case "[":
			p.next(true)
			n := &exprNode{kind: "Array"}
			for !p.isP("]") {
				n.kids = append(n.kids, p.parseConditional())
				if p.isP(",") {
					p.next(true)
					continue
				}
				break
			}
			if !p.isP("]") {
				p.fail("Unexpected token " + p.tok)
			}
			p.next(false)
			return p.mk(n)
		case "{":
			p.next(true)
			n := &exprNode{kind: "Object"}
			for !p.isP("}") {
				var key *exprNode
				switch p.kind {
				case "id":
					key = &exprNode{kind: "Identifier", name: p.val}
				case "str", "num":
					key = &exprNode{kind: "Literal", lit: p.val}
				default:
					p.fail("Unexpected token " + p.tok)
				}
				p.next(false)
				p.expect(":")
				val := p.parseConditional()
				n.kids = append(n.kids, p.mk(&exprNode{kind: "Property", kids: []*exprNode{key, val}}))
				if p.isP(",") {
					p.next(true)
					continue
				}
				break
			}
			if !p.isP("}") {
				p.fail("Unexpected token " + p.tok)
			}
			p.next(false)
			return p.mk(n)
		}
	case "eof":
		p.fail("Unexpected end of input")
	}
	p.fail("Unexpected token " + p.tok)
	return nil
}

func numLiteralString(s string) string {
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		if n, err := strconv.ParseUint(s[2:], 16, 64); err == nil {
			return jsval.JSNumberString(float64(n))
		}
	}
	f := jsval.StringToNumber(s)
	return jsval.JSNumberString(f)
}

func (n *exprNode) children() []*exprNode {
	if n.kind == "Member" {
		return []*exprNode{n.object, n.property}
	}
	return n.kids
}

func (n *exprNode) visit(f func(*exprNode)) {
	f(n)
	for _, c := range n.children() {
		c.visit(f)
	}
}

func exprNames(n *exprNode) []string {
	switch n.kind {
	case "Identifier":
		return []string{n.name}
	case "Literal":
		return []string{n.lit}
	case "Member":
		return append(exprNames(n.object), exprNames(n.property)...)
	}
	return nil
}

func startsWithDatum(n *exprNode) bool {
	if n.object.kind == "Member" {
		return startsWithDatum(n.object)
	}
	return n.object.kind == "Identifier" && n.object.name == "datum"
}

// getDependentFields lists the datum fields an expression reads.
func getDependentFields(expression string) *sset {
	ast, err := parseExprAST(expression)
	if err != nil {
		panic(err)
	}
	deps := newSset()
	ast.visit(func(n *exprNode) {
		if n.kind == "Member" && startsWithDatum(n) {
			names := exprNames(n)
			if len(names) > 0 {
				deps.add(strings.Join(names[1:], "."))
			}
		}
	})
	return deps
}
