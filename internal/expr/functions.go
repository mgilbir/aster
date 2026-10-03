package expr

import (
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// builtinFn is an expression function. args aliases a per-Scope stack: read
// it, do not keep it (copy before retaining). Missing arguments are absent
// from the slice; use arg to read one as undefined.
type builtinFn func(s *Scope, args []jsval.Value) jsval.Value

// funcDef describes one entry of the function table. Exactly one of math1,
// math2, fn or special is set.
type funcDef struct {
	// min is the number of arguments code generation needs to be present.
	// vega-expression builds `String(args[0]).toUpperCase()` and the like at
	// compile time, so a missing argument is a compile error there; functions
	// resolved through the runtime context just receive undefined.
	min   int
	math1 func(float64) float64
	math2 func(a, b float64) float64
	fn    builtinFn
	// pred1, for a function of one value that only tests it, is what fn does,
	// called without the argument stack.
	pred1 func(v jsval.Value) bool
	// special compiles the call itself (if, clamp).
	special func(c *compiler, n *Node) (node, error)
	// check validates the call's arguments at compile time (after min).
	check func(n *Node) error
	// visit is the dependency visitor of vega-functions.
	visit func(c *compiler, n *Node) error
}

var funcTable = map[string]*funcDef{}

func def(name string, d *funcDef) {
	if _, dup := funcTable[name]; dup {
		panic("expr: duplicate function " + name)
	}
	funcTable[name] = d
}

func fn(name string, f builtinFn) { def(name, &funcDef{fn: f}) }

// pred defines a function that tests its first argument.
func pred(name string, p func(v jsval.Value) bool) {
	def(name, &funcDef{pred1: p, fn: func(s *Scope, args []jsval.Value) jsval.Value { return jsval.Bool(p(arg(args, 0))) }})
}

func fnMin(name string, min int, f builtinFn) { def(name, &funcDef{fn: f, min: min}) }

func lookupFunc(name string) *funcDef { return funcTable[name] }

// HasFunction reports whether name is in the expression function table.
func HasFunction(name string) bool { return funcTable[name] != nil }

func arg(args []jsval.Value, i int) jsval.Value {
	if i < len(args) {
		return args[i]
	}
	return jsval.Undefined
}

func (c *compiler) call(n *Node) (node, error) {
	if n.Left.Kind != KindIdentifier {
		return nil, cerr("Illegal callee type: %s", kindName(n.Left.Kind))
	}
	name := n.Left.Name
	d := lookupFunc(name)
	if d == nil {
		return nil, cerr("Unrecognized function: %s", name)
	}
	if d.special != nil {
		return d.special(c, n)
	}
	if len(n.Elems) < d.min {
		return nil, cerr("Missing arguments to %s function.", name)
	}
	if d.check != nil {
		if err := d.check(n); err != nil {
			return nil, err
		}
	}
	args := make([]node, len(n.Elems))
	for i, a := range n.Elems {
		f, err := c.compile(a)
		if err != nil {
			return nil, err
		}
		args[i] = f
	}
	switch {
	case d.math1 != nil:
		f := d.math1
		if len(args) == 0 {
			return func(*Scope) jsval.Value { return jsval.Num(f(math.NaN())) }, nil
		}
		a0 := args[0]
		rest := args[1:]
		return func(s *Scope) jsval.Value {
			v := a0(s)
			for _, r := range rest {
				r(s)
			}
			if v.IsNum() {
				return jsval.Num(f(v.NumValue()))
			}
			return jsval.Num(f(s.num(v)))
		}, nil
	case d.math2 != nil:
		f := d.math2
		undef := node(func(*Scope) jsval.Value { return jsval.Undefined })
		a0, a1 := undef, undef
		if len(args) > 0 {
			a0 = args[0]
		}
		if len(args) > 1 {
			a1 = args[1]
		}
		var rest []node
		if len(args) > 2 {
			rest = args[2:]
		}
		return func(s *Scope) jsval.Value {
			x, y := a0(s), a1(s)
			for _, r := range rest {
				r(s)
			}
			return jsval.Num(f(s.num(x), s.num(y)))
		}, nil
	}
	if d.pred1 != nil && len(args) == 1 {
		p, a0 := d.pred1, args[0]
		return func(s *Scope) jsval.Value { return jsval.Bool(p(a0(s))) }, nil
	}
	f := d.fn
	switch len(args) {
	case 0:
		return func(s *Scope) jsval.Value { return f(s, nil) }, nil
	case 1:
		a0 := args[0]
		return func(s *Scope) jsval.Value {
			v := a0(s)
			base := len(s.stack)
			s.stack = append(s.stack, v)
			r := f(s, s.stack[base:base+1:base+1])
			s.stack = s.stack[:base]
			return r
		}, nil
	case 2:
		a0, a1 := args[0], args[1]
		return func(s *Scope) jsval.Value {
			v0 := a0(s)
			v1 := a1(s)
			base := len(s.stack)
			s.stack = append(s.stack, v0, v1)
			r := f(s, s.stack[base:base+2:base+2])
			s.stack = s.stack[:base]
			return r
		}, nil
	}
	return func(s *Scope) jsval.Value {
		base := len(s.stack)
		for _, a := range args {
			v := a(s)
			s.stack = append(s.stack, v)
		}
		end := len(s.stack)
		r := f(s, s.stack[base:end:end])
		s.stack = s.stack[:base]
		return r
	}, nil
}

func kindName(k Kind) string {
	switch k {
	case KindLiteral:
		return "Literal"
	case KindIdentifier:
		return "Identifier"
	case KindArray:
		return "ArrayExpression"
	case KindObject:
		return "ObjectExpression"
	case KindProperty:
		return "Property"
	case KindUnary:
		return "UnaryExpression"
	case KindBinary:
		return "BinaryExpression"
	case KindLogical:
		return "LogicalExpression"
	case KindConditional:
		return "ConditionalExpression"
	case KindMember:
		return "MemberExpression"
	case KindCall:
		return "CallExpression"
	}
	return "Unknown"
}

// argNodes compiles a call's arguments.
func (c *compiler) argNodes(n *Node) ([]node, error) {
	out := make([]node, len(n.Elems))
	for i, a := range n.Elems {
		f, err := c.compile(a)
		if err != nil {
			return nil, err
		}
		out[i] = f
	}
	return out, nil
}

func init() {
	// Control flow. `if(test, a, b)` is the only function taking lazily
	// evaluated branches.
	def("if", &funcDef{special: func(c *compiler, n *Node) (node, error) {
		if len(n.Elems) < 3 {
			return nil, cerr("Missing arguments to if function.")
		}
		if len(n.Elems) > 3 {
			return nil, cerr("Too many arguments to if function.")
		}
		a, err := c.argNodes(n)
		if err != nil {
			return nil, err
		}
		return func(s *Scope) jsval.Value {
			if a[0](s).IsTruthy() {
				return a[1](s)
			}
			return a[2](s)
		}, nil
	}})
	// clamp(v, lo, hi) is Math.max(lo, Math.min(hi, v)).
	def("clamp", &funcDef{special: func(c *compiler, n *Node) (node, error) {
		if len(n.Elems) < 3 {
			return nil, cerr("Missing arguments to clamp function.")
		}
		if len(n.Elems) > 3 {
			return nil, cerr("Too many arguments to clamp function.")
		}
		a, err := c.argNodes(n)
		if err != nil {
			return nil, err
		}
		return func(s *Scope) jsval.Value {
			v, lo, hi := a[0](s), a[1](s), a[2](s)
			return jsval.Num(jsMax2(s.num(lo), jsMin2(s.num(hi), s.num(v))))
		}, nil
	}})
	for _, name := range [...]string{"view", "item", "group", "xy", "x", "y"} {
		name := name
		fn(name, func(s *Scope, args []jsval.Value) jsval.Value {
			if s.view == nil {
				return jsval.Undefined
			}
			return s.view.EventFunction(name, append([]jsval.Value(nil), args...))
		})
	}
}
