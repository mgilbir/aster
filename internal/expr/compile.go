package expr

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mgilbir/aster/internal/jsval"
)

// node is one compiled expression: a closure over its compiled children.
// Compiling once into closures means evaluation walks no AST and dispatches on
// no operator strings.
type node func(*Scope) jsval.Value

// CompileError reports an expression that parses but cannot be compiled: an
// unknown function, a forbidden identifier, missing arguments, or a data
// function whose dataset name is not a string literal.
type CompileError struct{ Msg string }

func (e *CompileError) Error() string { return e.Msg }

func cerr(format string, args ...any) error { return &CompileError{Msg: fmt.Sprintf(format, args...)} }

// IndexDep is an index a data function needs: indata(Data, Field, ...) reads
// an index of Data on Field (and the selection functions with the "intersect"
// operation read one on "unit").
type IndexDep struct{ Data, Field string }

// Deps are the runtime objects an expression refers to, which the dataflow
// needs to schedule re-evaluation. It mirrors what vega-parser collects for an
// expression: identifiers that are (potentially) signals, datum fields read
// through `datum`, datasets, indices and scales.
type Deps struct {
	// Signals are the identifiers the expression reads as signals, in order of
	// first use. The runtime keeps the ones that name a defined signal.
	Signals []string
	// Fields are the properties read directly off `datum` with a literal name
	// (datum.a, datum["a"]).
	Fields []string
	// Data are the dataset names of data(), treePath(), treeAncestors() and the
	// vlSelection* functions.
	Data []string
	// RequiredData are the datasets that must exist: vega-parser fails with
	// "Undefined data set name" when indata() or a vlSelection* function names
	// an unknown one (data() and the tree functions merely read nothing).
	RequiredData []string
	// Indexes are the dataset indices the expression reads.
	Indexes []IndexDep
	// Scales are the literal scale names used by the scale functions.
	Scales []string
	// AllScales is set when a scale function takes a non-literal scale name, so
	// every scale is a dependency.
	AllScales bool
}

// Program is a compiled expression. It is immutable and safe for concurrent
// use; evaluate it against a per-goroutine Scope.
type Program struct {
	src  string
	ast  *Node
	run  node
	deps Deps
}

// Compile parses and compiles an expression.
func Compile(src string) (*Program, error) {
	ast, err := Parse(src)
	if err != nil {
		return nil, err
	}
	p, err := CompileNode(ast)
	if err != nil {
		return nil, err
	}
	p.src = src
	return p, nil
}

// Compiled expressions are shared between renders: a chart compiles the same
// few dozen expression strings (Vega-Lite emits them from a fixed set of
// templates) every time it is drawn, and a Program is immutable. The cache is
// bounded, since specifications choose the strings; it is dropped whole when
// full and skips long sources.
const (
	programCacheMax    = 1024
	programCacheMaxLen = 2048
)

var programCache = struct {
	sync.Mutex
	m map[string]*Program
}{m: map[string]*Program{}}

// CompileCached is Compile with a process-wide cache of successful compiles.
// Errors are not cached. The Program is shared; it is safe for concurrent use.
func CompileCached(src string) (*Program, error) {
	if len(src) > programCacheMaxLen {
		return Compile(src)
	}
	programCache.Lock()
	p := programCache.m[src]
	programCache.Unlock()
	if p != nil {
		return p, nil
	}
	p, err := Compile(src)
	if err != nil {
		return nil, err
	}
	programCache.Lock()
	if len(programCache.m) >= programCacheMax {
		clear(programCache.m)
	}
	programCache.m[src] = p
	programCache.Unlock()
	return p, nil
}

// CompileNode compiles a parsed expression.
func CompileNode(ast *Node) (*Program, error) {
	c := &compiler{
		signals: map[string]struct{}{},
		fields:  map[string]struct{}{},
		data:    map[string]struct{}{},
		indexes: map[IndexDep]struct{}{},
		scales:  map[string]struct{}{},
	}
	// Dependency visitors run over every call before code generation, and
	// their errors abort compilation, as upstream's parser does.
	var verr error
	ast.Walk(func(n *Node) bool {
		if n.Kind != KindCall || n.Left.Kind != KindIdentifier {
			return false
		}
		if def := lookupFunc(n.Left.Name); def != nil && def.visit != nil {
			if verr = def.visit(c, n); verr != nil {
				return true
			}
		}
		return false
	})
	if verr != nil {
		return nil, verr
	}
	run, err := c.compile(ast)
	if err != nil {
		return nil, err
	}
	p := &Program{ast: ast, run: run}
	p.deps = c.result()
	return p, nil
}

// Deps returns the dependencies of the expression.
func (p *Program) Deps() Deps { return p.deps }

// AST returns the parsed syntax tree.
func (p *Program) AST() *Node { return p.ast }

// Source returns the expression text when compiled with Compile.
func (p *Program) Source() string { return p.src }

// Eval evaluates the expression in s. JavaScript exceptions (a property read
// of null, an invalid regular expression, an unknown-scale error from the
// runtime) are returned as errors, as is context cancellation; Eval does not
// panic.
func (p *Program) Eval(s *Scope) (v jsval.Value, err error) {
	base := len(s.stack)
	defer func() {
		if r := recover(); r != nil {
			s.stack = s.stack[:base]
			v = jsval.Undefined
			switch e := r.(type) {
			case *Error:
				err = e
			case *cancelled:
				err = e.err
			default:
				err = fmt.Errorf("expr: internal error: %v", r)
			}
		}
	}()
	return p.run(s), nil
}

type compiler struct {
	signals map[string]struct{}
	sigList []string
	fields  map[string]struct{}
	fldList []string
	data    map[string]struct{}
	datList []string
	reqList []string
	indexes map[IndexDep]struct{}
	idxList []IndexDep
	scales  map[string]struct{}
	scaList []string
	allScal bool
}

func (c *compiler) result() Deps {
	return Deps{Signals: c.sigList, Fields: c.fldList, Data: c.datList, RequiredData: c.reqList, Indexes: c.idxList, Scales: c.scaList, AllScales: c.allScal}
}

func (c *compiler) addSignal(n string) {
	if _, ok := c.signals[n]; !ok {
		c.signals[n] = struct{}{}
		c.sigList = append(c.sigList, n)
	}
}

func (c *compiler) addField(n string) {
	if _, ok := c.fields[n]; !ok {
		c.fields[n] = struct{}{}
		c.fldList = append(c.fldList, n)
	}
}

func (c *compiler) addData(n string) {
	if _, ok := c.data[n]; !ok {
		c.data[n] = struct{}{}
		c.datList = append(c.datList, n)
	}
}

func (c *compiler) require(n string) {
	for _, r := range c.reqList {
		if r == n {
			return
		}
	}
	c.reqList = append(c.reqList, n)
}

func (c *compiler) addIndex(d IndexDep) {
	if _, ok := c.indexes[d]; !ok {
		c.indexes[d] = struct{}{}
		c.idxList = append(c.idxList, d)
	}
}

func (c *compiler) addScale(n string) {
	if _, ok := c.scales[n]; !ok {
		c.scales[n] = struct{}{}
		c.scaList = append(c.scaList, n)
	}
}

// constants are vega-expression's named constants.
var constants = map[string]float64{
	"NaN":       math.NaN(),
	"E":         math.E,
	"LN2":       math.Ln2,
	"LN10":      math.Ln10,
	"LOG2E":     math.Log2E,
	"LOG10E":    math.Log10E,
	"PI":        math.Pi,
	"SQRT1_2":   math.Sqrt2 / 2,
	"SQRT2":     math.Sqrt2,
	"MIN_VALUE": 5e-324,
	"MAX_VALUE": math.MaxFloat64,
}

// Constants returns the names of the expression language's numeric constants,
// sorted.
func Constants() []string {
	names := make([]string, 0, len(constants))
	for k := range constants {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func (c *compiler) compile(n *Node) (node, error) {
	switch n.Kind {
	case KindLiteral:
		return c.literal(n), nil
	case KindIdentifier:
		return c.identifier(n)
	case KindArray:
		return c.array(n)
	case KindObject:
		return c.object(n)
	case KindUnary:
		return c.unary(n)
	case KindBinary:
		return c.binary(n)
	case KindLogical:
		return c.logical(n)
	case KindConditional:
		return c.conditional(n)
	case KindMember:
		return c.member(n)
	case KindCall:
		return c.call(n)
	}
	return nil, cerr("Unsupported type: %d", n.Kind)
}

func (c *compiler) literal(n *Node) node {
	if n.Regex != nil {
		src, flags := n.Regex.Source, n.Regex.Flags
		if strings.ContainsAny(flags, "gy") {
			// A regexp literal is a fresh object on every evaluation, and g/y
			// patterns carry lastIndex state; recompile per evaluation so state
			// never leaks between calls.
			return func(s *Scope) jsval.Value {
				p, err := jsval.NewPattern(src, flags)
				if err != nil {
					throw("SyntaxError", "%v", err)
				}
				return jsval.PatternValue(p)
			}
		}
		v := n.Value
		return func(*Scope) jsval.Value { return v }
	}
	v := n.Value
	return func(*Scope) jsval.Value { return v }
}

func (c *compiler) identifier(n *Node) (node, error) {
	id := n.Name
	if id == "_" {
		return nil, cerr("Illegal identifier: _")
	}
	if f, ok := constants[id]; ok {
		v := jsval.Num(f)
		return func(*Scope) jsval.Value { return v }, nil
	}
	switch id {
	case "datum":
		return func(s *Scope) jsval.Value { return s.Datum }, nil
	case "event":
		return func(s *Scope) jsval.Value { return s.Event }, nil
	case "item":
		return func(s *Scope) jsval.Value { return s.Item }, nil
	}
	c.addSignal(id)
	return func(s *Scope) jsval.Value {
		if s.env == nil {
			return jsval.Undefined
		}
		v, _ := s.env.Signal(id)
		return v
	}, nil
}

func (c *compiler) array(n *Node) (node, error) {
	elems := make([]node, len(n.Elems))
	for i, e := range n.Elems {
		f, err := c.compile(e)
		if err != nil {
			return nil, err
		}
		elems[i] = f
	}
	return func(s *Scope) jsval.Value {
		items := make([]jsval.Value, len(elems))
		for i, f := range elems {
			items[i] = f(s)
		}
		return jsval.Arr(items)
	}, nil
}

// disallowedProperties are the Object.prototype method names (and __proto__)
// that an object literal may not define with an identifier key.
var disallowedProperties = map[string]bool{
	"constructor": true, "__defineGetter__": true, "__defineSetter__": true, "hasOwnProperty": true,
	"__lookupGetter__": true, "__lookupSetter__": true, "isPrototypeOf": true, "propertyIsEnumerable": true,
	"toString": true, "valueOf": true, "__proto__": true, "toLocaleString": true,
}

// IsObjectPrototypeName reports whether name is a property every JavaScript
// object inherits (constructor, toString, __proto__, ...). Upstream keeps its
// signals, scales and data in plain objects, so a lookup by such a name finds
// the inherited property instead of nothing.
func IsObjectPrototypeName(name string) bool { return disallowedProperties[name] }

func (c *compiler) object(n *Node) (node, error) {
	type prop struct {
		key  string
		val  node
		skip bool
	}
	props := make([]prop, len(n.Elems))
	special := false
	for i, p := range n.Elems {
		var key string
		// Only identifier keys are checked, as upstream (`prop.key.name`).
		if p.Key.Kind == KindIdentifier {
			if disallowedProperties[p.Key.Name] {
				return nil, cerr("Illegal property: %s", p.Key.Name)
			}
			key = p.Key.Name
		} else {
			key = p.Key.Value.AsString()
		}
		f, err := c.compile(p.Right)
		if err != nil {
			return nil, err
		}
		props[i] = prop{key: key, val: f}
		// A quoted "__proto__" key sets the prototype in JavaScript instead of
		// defining a property: the value is evaluated and dropped.
		if key == "__proto__" {
			props[i].skip = true
			special = true
		}
		if _, ok := arrayIndex(key); ok {
			special = true
		}
	}
	if !special {
		return func(s *Scope) jsval.Value {
			o := jsval.NewObject(len(props))
			for _, p := range props {
				o.Set(p.key, p.val(s))
			}
			return jsval.Obj(o)
		}, nil
	}
	// JavaScript enumerates integer-like keys first, in ascending order.
	var order []int
	var ints []int
	for i, p := range props {
		if _, ok := arrayIndex(p.key); ok {
			ints = append(ints, i)
		}
	}
	sort.SliceStable(ints, func(a, b int) bool {
		x, _ := arrayIndex(props[ints[a]].key)
		y, _ := arrayIndex(props[ints[b]].key)
		return x < y
	})
	order = append(order, ints...)
	for i, p := range props {
		if _, ok := arrayIndex(p.key); !ok {
			order = append(order, i)
		}
	}
	return func(s *Scope) jsval.Value {
		tmp := make([]jsval.Value, len(props))
		for i, p := range props {
			tmp[i] = p.val(s)
		}
		o := jsval.NewObject(len(props))
		for _, i := range order {
			if !props[i].skip {
				o.Set(props[i].key, tmp[i])
			}
		}
		return jsval.Obj(o)
	}, nil
}

func (c *compiler) unary(n *Node) (node, error) {
	if n.Op == "-" && n.Left.Kind == KindLiteral && n.Left.Value.IsNum() {
		v := jsval.Num(-n.Left.Value.NumValue())
		return func(*Scope) jsval.Value { return v }, nil
	}
	arg, err := c.compile(n.Left)
	if err != nil {
		return nil, err
	}
	switch n.Op {
	case "-":
		return func(s *Scope) jsval.Value {
			v := arg(s)
			if v.IsNum() {
				return jsval.Num(-v.NumValue())
			}
			return jsval.Num(-s.num(v))
		}, nil
	case "+":
		return func(s *Scope) jsval.Value {
			v := arg(s)
			if v.IsNum() {
				return v
			}
			return jsval.Num(s.num(v))
		}, nil
	case "!":
		return func(s *Scope) jsval.Value { return jsval.Bool(!arg(s).IsTruthy()) }, nil
	case "~":
		return func(s *Scope) jsval.Value { return jsval.Num(float64(^toInt32(s.num(arg(s))))) }, nil
	}
	return nil, cerr("Unsupported operator: %s", n.Op)
}

func (c *compiler) logical(n *Node) (node, error) {
	l, err := c.compile(n.Left)
	if err != nil {
		return nil, err
	}
	r, err := c.compile(n.Right)
	if err != nil {
		return nil, err
	}
	if n.Op == "&&" {
		return func(s *Scope) jsval.Value {
			v := l(s)
			if !v.IsTruthy() {
				return v
			}
			return r(s)
		}, nil
	}
	return func(s *Scope) jsval.Value {
		v := l(s)
		if v.IsTruthy() {
			return v
		}
		return r(s)
	}, nil
}

func (c *compiler) conditional(n *Node) (node, error) {
	t, err := c.compile(n.Test)
	if err != nil {
		return nil, err
	}
	a, err := c.compile(n.Left)
	if err != nil {
		return nil, err
	}
	b, err := c.compile(n.Right)
	if err != nil {
		return nil, err
	}
	return func(s *Scope) jsval.Value {
		if t(s).IsTruthy() {
			return a(s)
		}
		return b(s)
	}, nil
}

func (c *compiler) binary(n *Node) (node, error) {
	l, err := c.compile(n.Left)
	if err != nil {
		return nil, err
	}
	r, err := c.compile(n.Right)
	if err != nil {
		return nil, err
	}
	arith := func(f func(a, b float64) float64) node {
		return func(s *Scope) jsval.Value {
			a, b := l(s), r(s)
			if a.IsNum() && b.IsNum() {
				return jsval.Num(f(a.NumValue(), b.NumValue()))
			}
			return jsval.Num(f(s.num(a), s.num(b)))
		}
	}
	bits := func(f func(a, b int32) float64) node {
		return func(s *Scope) jsval.Value {
			a, b := s.num(l(s)), s.num(r(s))
			return jsval.Num(f(toInt32(a), toInt32(b)))
		}
	}
	switch n.Op {
	case "+":
		return func(s *Scope) jsval.Value { return s.add(l(s), r(s)) }, nil
	case "-":
		return arith(func(a, b float64) float64 { return a - b }), nil
	case "*":
		return arith(func(a, b float64) float64 { return a * b }), nil
	case "/":
		return arith(func(a, b float64) float64 { return a / b }), nil
	case "%":
		return arith(jsMod), nil
	case "==":
		return func(s *Scope) jsval.Value { return jsval.Bool(s.looseEquals(l(s), r(s))) }, nil
	case "!=":
		return func(s *Scope) jsval.Value { return jsval.Bool(!s.looseEquals(l(s), r(s))) }, nil
	case "===":
		return func(s *Scope) jsval.Value { return jsval.Bool(strictEquals(l(s), r(s))) }, nil
	case "!==":
		return func(s *Scope) jsval.Value { return jsval.Bool(!strictEquals(l(s), r(s))) }, nil
	case "<":
		return func(s *Scope) jsval.Value { return jsval.Bool(s.lt(l(s), r(s))) }, nil
	case ">":
		return func(s *Scope) jsval.Value { return jsval.Bool(s.gt(l(s), r(s))) }, nil
	case "<=":
		return func(s *Scope) jsval.Value { return jsval.Bool(s.le(l(s), r(s))) }, nil
	case ">=":
		return func(s *Scope) jsval.Value { return jsval.Bool(s.ge(l(s), r(s))) }, nil
	case "&":
		return bits(func(a, b int32) float64 { return float64(a & b) }), nil
	case "|":
		return bits(func(a, b int32) float64 { return float64(a | b) }), nil
	case "^":
		return bits(func(a, b int32) float64 { return float64(a ^ b) }), nil
	case "<<":
		return bits(func(a, b int32) float64 { return float64(a << (uint32(b) & 31)) }), nil
	case ">>":
		return bits(func(a, b int32) float64 { return float64(a >> (uint32(b) & 31)) }), nil
	case ">>>":
		return bits(func(a, b int32) float64 { return float64(uint32(a) >> (uint32(b) & 31)) }), nil
	case "in":
		return func(s *Scope) jsval.Value {
			k, o := l(s), r(s)
			return jsval.Bool(s.hasProperty(o, k))
		}, nil
	case "instanceof":
		return func(s *Scope) jsval.Value {
			l(s)
			r(s)
			// No value an expression can hold is callable.
			typeError("Right-hand side of 'instanceof' is not callable")
			return jsval.Undefined
		}, nil
	}
	return nil, cerr("Unsupported operator: %s", n.Op)
}

// stripQuotes is upstream's helper for recording field names: it removes one
// pair of matching quotes from the literal's source text.
func stripQuotes(s string) string {
	n := len(s) - 1
	if n > 0 && (s[0] == '"' && s[n] == '"' || s[0] == '\'' && s[n] == '\'') {
		return s[1:n]
	}
	return s
}

// bareInteger reports whether n is a decimal integer literal written without a
// point, exponent or prefix ("1", "123"). Upstream generates JavaScript source,
// where `1.x` is a syntax error: a member access on such a literal (and length(1),
// whose code is `1.length`) fails to compile.
func bareInteger(n *Node) bool {
	if n.Kind != KindLiteral || !n.Value.IsNum() || n.Raw == "" {
		return false
	}
	for i := 0; i < len(n.Raw); i++ {
		if n.Raw[i] < '0' || n.Raw[i] > '9' {
			return false
		}
	}
	return true
}

func (c *compiler) member(n *Node) (node, error) {
	if !n.Computed && bareInteger(n.Left) {
		return nil, cerr("Invalid or unexpected token")
	}
	// datum.x and datum["x"] are the hot path of every filter and formula.
	isDatum := n.Left.Kind == KindIdentifier && n.Left.Name == "datum"
	if !n.Computed || n.Right.Kind == KindLiteral && n.Right.Value.IsStr() {
		var key string
		if n.Computed {
			key = n.Right.Value.StrValue()
		} else {
			key = n.Right.Name
		}
		if isDatum {
			if n.Computed {
				c.addField(stripQuotes(n.Right.Raw))
			} else {
				c.addField(key)
			}
			var hint slotHint
			return func(s *Scope) jsval.Value {
				d := s.Datum
				if d.IsObj() {
					return hint.get(d.ObjValue(), key)
				}
				return s.getProp(d, key)
			}, nil
		}
		obj, err := c.compile(n.Left)
		if err != nil {
			return nil, err
		}
		var hint slotHint
		return func(s *Scope) jsval.Value {
			o := obj(s)
			if o.IsObj() {
				return hint.get(o.ObjValue(), key)
			}
			return s.getProp(o, key)
		}, nil
	}
	obj, err := c.compile(n.Left)
	if err != nil {
		return nil, err
	}
	if isDatum && n.Right.Kind == KindLiteral {
		c.addField(stripQuotes(n.Right.Raw))
	}
	prop, err := c.compile(n.Right)
	if err != nil {
		return nil, err
	}
	return func(s *Scope) jsval.Value {
		o := obj(s)
		return s.getIndex(o, prop(s))
	}, nil
}

// hasProperty is the `in` operator.
func (s *Scope) hasProperty(o, k jsval.Value) bool {
	if !isObjectLike(o) {
		typeError("Cannot use 'in' operator to search for '%s' in %s", s.str(k), s.str(o))
	}
	key := s.str(k)
	switch o.Kind() {
	case jsval.KindObj:
		return o.ObjValue().Has(key)
	case jsval.KindArr:
		if key == "length" {
			return true
		}
		if i, ok := arrayIndex(key); ok {
			return i < o.Len()
		}
	case jsval.KindPattern:
		return patternProp(o.PatternOf(), key) != jsval.Undefined
	}
	return false
}

// arrayIndex parses a canonical array index ("0", "17"; not "01" or "-1").
func arrayIndex(key string) (int, bool) {
	if key == "" || len(key) > 9 || len(key) > 1 && key[0] == '0' {
		return 0, false
	}
	n := 0
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return 0, false
		}
		n = n*10 + int(key[i]-'0')
	}
	return n, true
}

// getProp reads obj[key] for a string key.
func (s *Scope) getProp(obj jsval.Value, key string) jsval.Value {
	switch obj.Kind() {
	case jsval.KindObj:
		return obj.Get(key)
	case jsval.KindArr:
		if key == "length" {
			return jsval.Int(obj.Len())
		}
		if i, ok := arrayIndex(key); ok {
			return obj.Index(i)
		}
	case jsval.KindStr:
		str := obj.StrValue()
		if key == "length" {
			return jsval.Int(utf16Len(str))
		}
		if i, ok := arrayIndex(key); ok {
			return charAt(str, i)
		}
	case jsval.KindPattern:
		return patternProp(obj.PatternOf(), key)
	case jsval.KindUndefined, jsval.KindNull:
		typeError("Cannot read properties of %s (reading '%s')", obj.AsString(), key)
	}
	return jsval.Undefined
}

// getIndex reads obj[k] for a computed key.
func (s *Scope) getIndex(obj, k jsval.Value) jsval.Value {
	if k.IsNum() {
		f := k.NumValue()
		if i := int(f); float64(i) == f && i >= 0 {
			switch obj.Kind() {
			case jsval.KindArr:
				return obj.Index(i)
			case jsval.KindStr:
				return charAt(obj.StrValue(), i)
			}
		}
	}
	if obj.IsNullish() {
		typeError("Cannot read properties of %s (reading '%s')", obj.AsString(), s.str(k))
	}
	return s.getProp(obj, s.str(k))
}

func charAt(str string, i int) jsval.Value {
	if isASCII(str) {
		if i < len(str) {
			return jsval.Str(str[i : i+1])
		}
		return jsval.Undefined
	}
	u := toUTF16(str)
	if i < len(u) {
		return jsval.Str(fromUTF16(u[i : i+1]))
	}
	return jsval.Undefined
}

func patternProp(p *jsval.Pattern, key string) jsval.Value {
	switch key {
	case "source":
		return jsval.Str(p.Source)
	case "flags":
		return jsval.Str(p.Flags)
	case "global":
		return jsval.Bool(strings.Contains(p.Flags, "g"))
	case "ignoreCase":
		return jsval.Bool(strings.Contains(p.Flags, "i"))
	case "multiline":
		return jsval.Bool(strings.Contains(p.Flags, "m"))
	case "unicode":
		return jsval.Bool(strings.Contains(p.Flags, "u"))
	case "sticky":
		return jsval.Bool(strings.Contains(p.Flags, "y"))
	case "dotAll":
		return jsval.Bool(strings.Contains(p.Flags, "s"))
	case "lastIndex":
		return jsval.Int(p.Re.LastIndex())
	}
	return jsval.Undefined
}

// slotHint remembers where a property was last found at one access site.
// Objects of a dataset share their key order, so checking the remembered slot
// first turns most lookups into one string comparison instead of a scan. It is
// only a hint: a wrong slot falls back to the full lookup. The slot is atomic
// because a compiled program may be evaluated by several goroutines.
type slotHint struct{ slot atomic.Int32 }

func (h *slotHint) get(o *jsval.Object, key string) jsval.Value {
	if i := int(h.slot.Load()); i < o.Len() && o.KeyAt(i) == key {
		return o.ValueAt(i)
	}
	v, ok := o.Get(key)
	if ok {
		for i, k := range o.Keys() {
			if k == key {
				h.slot.Store(int32(i))
				break
			}
		}
	}
	return v
}
