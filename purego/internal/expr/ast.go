package expr

import "github.com/mgilbir/aster/purego/internal/jsval"

// Kind is the type of an AST node, named after its ESTree counterpart.
type Kind uint8

const (
	KindLiteral Kind = iota + 1
	KindIdentifier
	KindArray
	KindObject
	KindProperty
	KindUnary
	KindBinary
	KindLogical
	KindConditional
	KindMember
	KindCall
)

// Node is one node of the parsed expression. Which fields are set depends on
// Kind:
//
//	Literal      Value, Raw (source text); Regex for a regular expression literal
//	Identifier   Name
//	Array        Elems
//	Object       Elems (of KindProperty)
//	Property     Key (Identifier or Literal), Right (value)
//	Unary        Op, Left (the argument)
//	Binary       Op, Left, Right
//	Logical      Op ("&&" or "||"), Left, Right
//	Conditional  Test, Left (consequent), Right (alternate)
//	Member       Left (object), Right (property), Computed
//	Call         Left (callee, always an Identifier when compiled), Elems (arguments)
type Node struct {
	Kind     Kind
	Op       string
	Name     string
	Raw      string
	Value    jsval.Value
	Regex    *jsval.Pattern
	Test     *Node
	Left     *Node
	Right    *Node
	Key      *Node
	Elems    []*Node
	Computed bool
	// depth is the height of the subtree, used to bound recursion.
	depth int
}

// Walk visits n and its descendants in the order upstream's ASTNode.visit does
// (a node before its children; callee before arguments; key before value). It
// stops early when visit returns true.
func (n *Node) Walk(visit func(*Node) (stop bool)) bool {
	if n == nil {
		return false
	}
	if visit(n) {
		return true
	}
	switch n.Kind {
	case KindArray, KindObject:
		for _, e := range n.Elems {
			if e.Walk(visit) {
				return true
			}
		}
	case KindBinary, KindLogical, KindMember:
		return n.Left.Walk(visit) || n.Right.Walk(visit)
	case KindCall:
		if n.Left.Walk(visit) {
			return true
		}
		for _, e := range n.Elems {
			if e.Walk(visit) {
				return true
			}
		}
	case KindConditional:
		return n.Test.Walk(visit) || n.Left.Walk(visit) || n.Right.Walk(visit)
	case KindProperty:
		return n.Key.Walk(visit) || n.Right.Walk(visit)
	case KindUnary:
		return n.Left.Walk(visit)
	}
	return false
}
