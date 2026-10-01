package expr

import (
	"testing"

	"github.com/mgilbir/aster/internal/upstream"
)

// astNode renders a parsed node as vega-expression's parser makes it: an ESTree-like node (the
// recorder tags the class), with `member: true` on the property of a dotted access.
func astNode(n *Node, member bool) any {
	node := func(typ string, fields map[string]any) map[string]any {
		fields["$class"] = "ASTNode"
		fields["type"] = typ
		return fields
	}
	switch n.Kind {
	case KindLiteral:
		f := map[string]any{"value": upstream.FromValue(n.Value), "raw": n.Raw}
		if n.Regex != nil {
			f["value"] = map[string]any{"$": "regexp", "source": n.Regex.Source, "flags": n.Regex.Flags}
			f["regex"] = map[string]any{"pattern": n.Regex.Source, "flags": n.Regex.Flags}
		}
		return node("Literal", f)
	case KindIdentifier:
		f := map[string]any{"name": n.Name}
		if member {
			f["member"] = true
		}
		return node("Identifier", f)
	case KindArray:
		items := make([]any, len(n.Elems))
		for i, e := range n.Elems {
			items[i] = astNode(e, false)
		}
		return node("ArrayExpression", map[string]any{"elements": items})
	case KindObject:
		items := make([]any, len(n.Elems))
		for i, e := range n.Elems {
			items[i] = astNode(e, false)
		}
		return node("ObjectExpression", map[string]any{"properties": items})
	case KindProperty:
		return node("Property", map[string]any{"key": astNode(n.Key, false), "value": astNode(n.Right, false), "kind": "init"})
	case KindUnary:
		return node("UnaryExpression", map[string]any{"operator": n.Op, "argument": astNode(n.Left, false), "prefix": true})
	case KindBinary:
		return node("BinaryExpression", map[string]any{"operator": n.Op, "left": astNode(n.Left, false), "right": astNode(n.Right, false)})
	case KindLogical:
		return node("LogicalExpression", map[string]any{"operator": n.Op, "left": astNode(n.Left, false), "right": astNode(n.Right, false)})
	case KindConditional:
		return node("ConditionalExpression", map[string]any{
			"test": astNode(n.Test, false), "consequent": astNode(n.Left, false), "alternate": astNode(n.Right, false),
		})
	case KindMember:
		return node("MemberExpression", map[string]any{
			"computed": n.Computed, "object": astNode(n.Left, false), "property": astNode(n.Right, !n.Computed),
		})
	case KindCall:
		args := make([]any, len(n.Elems))
		for i, e := range n.Elems {
			args[i] = astNode(e, false)
		}
		return node("CallExpression", map[string]any{"callee": astNode(n.Left, false), "arguments": args})
	}
	return nil
}

// TestUpstreamVegaExpressionParse replays vega-expression's parser tests: the tree the engine's parser
// builds, in the shape upstream's has, for every expression its tests parse (those that parse, and
// those that throw).
func TestUpstreamVegaExpressionParse(t *testing.T) {
	r := upstream.Start(t, "vega-expression")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil || c.Fn != "parseExpression" {
			continue
		}
		src, ok := argAtUpstream(c.Args, 0).(string)
		if !ok {
			r.Skip("input that is not a string")
			continue
		}
		n, err := Parse(src)
		if err != nil {
			r.Check(c, nil, true)
			continue
		}
		r.Check(c, astNode(n, false), false)
	}
	r.Done(100)
}

func argAtUpstream(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return upstream.Undefined()
}
