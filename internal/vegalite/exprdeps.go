package vegalite

import (
	"context"
	"errors"
	"strings"

	"github.com/mgilbir/aster/internal/expr"
	"github.com/mgilbir/aster/internal/jsval"
)

// A parser for the Vega expression language, used only to find which datum
// fields an expression reads (upstream's getDependentFields, which parses
// with vega-expression). Its tokens are internal/expr's, vega-expression's
// tokenizer (C47 of the 2026-10-18 audit: a lexer of its own had drifted
// from it); its grammar is vega-expression's, accepting and refusing what
// internal/expr's Parse does (TestExprDepsAcceptsAsParse), except that chains
// of binary operators and of conditionals are taken at any length (C10).

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
	// cc is charged a step per token, so a long expression stops at the deadline.
	cc    *compileCtx
	s     *expr.Scanner
	t     expr.Token // the lookahead
	depth int
}

// MaxExprDepth bounds how deeply an expression nests: the parser's own
// recursion (parentheses, calls, array and object literals, unary operators,
// a conditional's consequent) and the depth of the syntax tree it builds,
// which exprNode.visit and exprNames walk recursively. It is the same bound
// as internal/expr's MaxDepth (TestExprDepthMatchesVega checks), which Vega's
// parser enforces on the same text later.
//
// Chains of binary operators (a || b || ...) and of conditionals in the
// alternate (a ? 0 : b ? 1 : ..., which an explicit sort array compiles to)
// are not nesting here: they are parsed in a loop into one node whose
// operands are its children in source order, so their length is bounded by
// the expression's, not by this depth (C10). Only the fields an expression
// reads are taken from the tree, in the order a walk of upstream's tree
// meets them, which is source order for the operands of a chain whatever
// their precedence.
const MaxExprDepth = 512

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
	if n.depth > MaxExprDepth {
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

// next reads the next token (internal/expr's tokenizer, vega-expression's).
func (p *exprParser) next() {
	p.t = p.s.Next()
	p.cc.work(1 + int64(p.t.End-p.t.Start)/16)
}

func (p *exprParser) isP(v string) bool { return p.t.Kind == expr.TokPunct && p.t.Text == v }

// unexpected fails at token t with vega-expression's message.
func (p *exprParser) unexpected() {
	switch p.t.Kind {
	case expr.TokEOF:
		p.fail("Unexpected end of input")
	case expr.TokNum:
		p.fail("Unexpected number")
	case expr.TokString:
		p.fail("Unexpected string")
	case expr.TokIdent:
		p.fail("Unexpected identifier")
	case expr.TokKeyword:
		p.fail("Unexpected reserved word")
	}
	p.fail("Unexpected token " + p.t.Text)
}

func (p *exprParser) expect(v string) {
	if !p.isP(v) {
		p.unexpected()
	}
	p.next()
}

func parseExprAST(cc *compileCtx, src string) (root *exprNode, err error) {
	defer func() {
		if r := recover(); r != nil {
			switch se := r.(type) {
			case exprSyntaxError:
				root, err = nil, se.err()
				return
			case *expr.SyntaxError:
				// The tokenizer's: an illegal token, a regular expression
				// literal that does not compile, or the call's deadline or
				// memory reached compiling one.
				if cerr := cc.ctxErr(); cerr != nil && errors.Is(se, cerr) {
					panic(cancelError{cerr})
				}
				root, err = nil, exprSyntaxError{msg: se.Msg, limit: se.Limit}.err()
				return
			}
			panic(r)
		}
	}()
	var ctx context.Context
	if cc != nil {
		ctx = cc.ctx
	}
	p := &exprParser{cc: cc, s: expr.NewScanner(ctx, src)}
	p.next()
	root = p.parseConditional()
	if p.isP(",") {
		p.fail("Disabled.") // no sequence expressions
	}
	if p.t.Kind != expr.TokEOF {
		p.fail("Unexpect token after expression.")
	}
	return root, nil
}

func (p *exprParser) parseConditional() *exprNode {
	p.depth++
	if p.depth > MaxExprDepth {
		p.failLimit("expression too deeply nested")
	}
	defer func() { p.depth-- }()
	test := p.parseBinary()
	if !p.isP("?") {
		return test
	}
	// a ? b : c ? d : e is right-associative: the alternate of each is the
	// next conditional. The chain is one node, [a, b, c, d, e].
	kids := []*exprNode{test}
	for p.isP("?") {
		p.cc.work(1)
		p.next()
		kids = append(kids, p.parseConditional())
		p.expect(":")
		kids = append(kids, p.parseBinary())
	}
	return p.mk(&exprNode{kind: "Conditional", kids: kids})
}

// isBinaryOperator is vega-expression's binaryPrecedence(token) > 0: the
// operators of ECMAScript's binary expressions, the keywords in and
// instanceof included.
func isBinaryOperator(t expr.Token) bool {
	if t.Kind != expr.TokPunct && t.Kind != expr.TokKeyword {
		return false
	}
	switch t.Text {
	case "||", "&&", "|", "^", "&", "==", "!=", "===", "!==", "<", ">", "<=", ">=", "instanceof", "in",
		"<<", ">>", ">>>", "+", "-", "*", "/", "%":
		return true
	}
	return false
}

// parseBinary parses operands joined by binary operators into one node of
// the operands in source order: the order a walk of the precedence tree
// meets them, and every operator sequence the precedence grammar accepts.
func (p *exprParser) parseBinary() *exprNode {
	first := p.parseUnary()
	if !isBinaryOperator(p.t) {
		return first
	}
	kids := []*exprNode{first}
	for isBinaryOperator(p.t) {
		p.cc.work(1)
		p.next()
		kids = append(kids, p.parseUnary())
	}
	return p.mk(&exprNode{kind: "Binary", kids: kids})
}

func (p *exprParser) parseUnary() *exprNode {
	p.depth++
	if p.depth > MaxExprDepth {
		p.failLimit("expression too deeply nested")
	}
	defer func() { p.depth-- }()
	switch {
	case p.isP("++") || p.isP("--"):
		p.fail("Disabled.")
	case p.isP("+") || p.isP("-") || p.isP("~") || p.isP("!"):
		p.next()
		arg := p.parseUnary()
		return p.mk(&exprNode{kind: "Unary", kids: []*exprNode{arg}})
	case p.t.Kind == expr.TokKeyword && (p.t.Text == "delete" || p.t.Text == "void" || p.t.Text == "typeof"):
		p.fail("Disabled.")
	}
	return p.parsePostfix()
}

func (p *exprParser) parsePostfix() *exprNode {
	e := p.parseLeftHandSide()
	if p.isP("++") || p.isP("--") {
		p.fail("Disabled.")
	}
	return e
}

func (p *exprParser) parseLeftHandSide() *exprNode {
	e := p.parsePrimary()
	for {
		switch {
		case p.isP("."):
			p.next()
			switch p.t.Kind {
			case expr.TokIdent, expr.TokKeyword, expr.TokBool, expr.TokNull:
			default:
				p.unexpected()
			}
			prop := &exprNode{kind: "Identifier", name: p.t.Text}
			p.next()
			e = p.mk(&exprNode{kind: "Member", object: e, property: prop})
		case p.isP("["):
			p.next()
			prop := p.parseConditional()
			if p.isP(",") {
				p.fail("Disabled.")
			}
			p.expect("]")
			e = p.mk(&exprNode{kind: "Member", object: e, property: prop})
		case p.isP("("):
			p.next()
			kids := []*exprNode{e}
			if !p.isP(")") {
				for {
					kids = append(kids, p.parseConditional())
					if p.isP(")") {
						break
					}
					p.expect(",")
				}
			}
			p.expect(")")
			e = p.mk(&exprNode{kind: "Call", kids: kids})
		default:
			return e
		}
	}
}

func (p *exprParser) parsePrimary() *exprNode {
	t := p.t
	switch {
	case p.isP("("):
		p.next()
		e := p.parseConditional()
		if p.isP(",") {
			p.fail("Disabled.")
		}
		p.expect(")")
		return e
	case p.isP("["):
		p.next()
		n := &exprNode{kind: "Array"}
		for !p.isP("]") {
			if p.isP(",") {
				p.fail("Unexpected token ,") // an elision
			}
			n.kids = append(n.kids, p.parseConditional())
			if !p.isP("]") {
				p.expect(",")
			}
		}
		p.next()
		return p.mk(n)
	case p.isP("{"):
		return p.parseObject()
	case expr.IsPrimaryIdentifier(t):
		p.next()
		return &exprNode{kind: "Identifier", name: t.Text}
	case t.Kind == expr.TokString || t.Kind == expr.TokNum:
		if t.Octal {
			p.fail("Octal literals are not allowed in strict mode.")
		}
		p.next()
		return &exprNode{kind: "Literal", lit: literalText(t)}
	case t.Kind == expr.TokKeyword:
		p.fail("Disabled.")
	case t.Kind == expr.TokBool || t.Kind == expr.TokNull:
		p.next()
		return &exprNode{kind: "Literal", lit: t.Text}
	case p.isP("/") || p.isP("/="):
		raw := p.s.RegExp(t.Start)
		p.next()
		return &exprNode{kind: "Literal", lit: raw}
	}
	p.unexpected()
	return nil
}

// parseObject is an object literal: keys are identifiers, keywords,
// literals; a key given twice fails (strict mode).
func (p *exprParser) parseObject() *exprNode {
	p.next()
	n := &exprNode{kind: "Object"}
	seen := map[string]bool{}
	for !p.isP("}") {
		t := p.t
		if t.Kind == expr.TokEOF || t.Kind == expr.TokPunct {
			p.unexpected()
		}
		var key *exprNode
		name := t.Text
		if t.Kind == expr.TokString || t.Kind == expr.TokNum {
			if t.Octal {
				p.fail("Octal literals are not allowed in strict mode.")
			}
			name = literalText(t)
			key = &exprNode{kind: "Literal", lit: name}
		} else {
			key = &exprNode{kind: "Identifier", name: name}
		}
		p.next()
		p.expect(":")
		val := p.parseConditional()
		if seen[name] {
			p.fail("Duplicate data property in object literal not allowed in strict mode")
		}
		seen[name] = true
		n.kids = append(n.kids, p.mk(&exprNode{kind: "Property", kids: []*exprNode{key, val}}))
		if !p.isP("}") {
			p.expect(",")
		}
	}
	p.expect("}")
	return p.mk(n)
}

// literalText is a literal's value as getName's join writes it: a string
// as itself, a number as JavaScript prints it.
func literalText(t expr.Token) string {
	if t.Kind == expr.TokNum {
		return jsval.JSNumberString(t.Num)
	}
	return t.Text
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
func getDependentFields(cc *compileCtx, expression string) *sset {
	ast, err := parseExprAST(cc, expression)
	if err != nil {
		panic(err)
	}
	deps := newSset()
	ast.visit(func(n *exprNode) {
		cc.work(1)
		if n.kind == "Member" && startsWithDatum(n) {
			names := exprNames(n)
			if len(names) > 0 {
				deps.add(strings.Join(names[1:], "."))
			}
		}
	})
	return deps
}
