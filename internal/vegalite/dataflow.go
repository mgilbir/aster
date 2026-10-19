package vegalite

import (
	"fmt"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// The data flow graph — vega-lite/src/compile/data/dataflow.ts and the node
// classes beside it. Each model contributes nodes; optimizer passes then merge
// and reorder them before assembly turns the tree into Vega data definitions.

type dataSourceType int

const (
	dsRaw dataSourceType = iota
	dsMain
	dsRow
	dsColumn
	dsLookup
	dsPreFilterInvalid
	dsPostFilterInvalid
)

func (t dataSourceType) String() string {
	return [...]string{"Raw", "Main", "Row", "Column", "Lookup", "PreFilterInvalid", "PostFilterInvalid"}[t]
}

// sourceGetter is what a model's outputNodes map holds (an OutputNode or FacetNode).
type sourceGetter interface{ getSource() Value }

type dfNode interface {
	base() *dfBase
	hash() string
	clone() dfNode
	dependentFields() *sset
	producedFields() *sset // nil means unknown (undefined upstream)
}

type dfBase struct {
	// cc is the compilation the node belongs to; setParent copies it from the parent.
	cc   *compileCtx
	self dfNode
	// kids is upstream's `_children` array (see kidList). Only this file's
	// methods change it.
	kids kidList
	par  dfNode
}

// ccOf is the compilation n belongs to (nil for no node).
func ccOf(n dfNode) *compileCtx {
	if n == nil {
		return nil
	}
	return n.base().cc
}

func (b *dfBase) hasChild(c dfNode) bool { return b.kids.has(c) }

// children returns the children in order; see kidList.all for when the
// slice is live.
func (b *dfBase) children() []dfNode { return b.kids.all() }

// child returns the i-th child.
func (b *dfBase) child(i int) dfNode { return b.kids.at(i) }

func (b *dfBase) base() *dfBase { return b }

func (b *dfBase) setParent(p dfNode) {
	if _, isSource := b.self.(*sourceNode); isSource {
		throw("Source nodes have to be roots.")
	}
	b.par = p
	if p != nil {
		if b.cc == nil {
			b.cc = p.base().cc
		}
		p.base().addChild(b.self, -1)
	}
}

func (b *dfBase) numChildren() int { return b.kids.len() }

func (b *dfBase) indexOf(c dfNode) int { return b.kids.indexOf(c) }

func (b *dfBase) addChild(c dfNode, loc int) {
	cc := b.cc
	cc.work(1)
	if b.hasChild(c) {
		cc.warn(msgAddSameChildTwice)
		return
	}
	n := b.kids.len()
	if loc < 0 || loc > n {
		loc = n
	}
	b.kids.insert(loc, c)
}

// removeChild mirrors Array.prototype.splice(indexOf(x), 1), including its
// quirk that a missing child (index -1) removes the last one.
func (b *dfBase) removeChild(c dfNode) int {
	cc := b.cc
	cc.work(1)
	loc := b.indexOf(c)
	switch {
	case loc >= 0:
		b.kids.removeAt(loc)
	case b.kids.len() > 0:
		b.kids.removeAt(b.kids.len() - 1)
	}
	return loc
}

func (b *dfBase) remove() {
	if _, isSource := b.self.(*sourceNode); isSource {
		throw("Source nodes are roots and cannot be removed.")
	}
	loc := b.par.base().removeChild(b.self)
	for _, child := range append([]dfNode(nil), b.children()...) {
		child.base().par = b.par
		b.par.base().addChild(child, loc)
		if loc >= 0 {
			loc++
		}
	}
}

func (b *dfBase) insertAsParentOf(other dfNode) {
	parent := other.base().par
	parent.base().removeChild(b.self)
	b.setParent(parent)
	other.base().setParent(b.self)
}

func (b *dfBase) swapWithParent() {
	parent := b.par
	newParent := parent.base().par
	for _, child := range append([]dfNode(nil), b.children()...) {
		child.base().setParent(parent)
	}
	b.kids.clear()
	parent.base().removeChild(b.self)
	loc := parent.base().par.base().removeChild(parent)
	b.par = newParent
	newParent.base().addChild(b.self, loc)
	parent.base().setParent(b.self)
}

// initNode wires a freshly built node to its parent (upstream's constructor).
func initNode[T dfNode](n T, parent dfNode) T {
	n.base().self = n
	if parent != nil {
		n.base().setParent(parent)
	}
	return n
}

// ---- output ----

type outputNode struct {
	dfBase
	typ       dataSourceType
	refCounts map[string]int
	// source is the data set the node reads, as upstream holds its name:
	// a data object's name of any type reaches the output as it is.
	source  Value
	name    string
	hashKey string
}

func newOutputNode(parent dfNode, source string, typ dataSourceType, refCounts map[string]int) *outputNode {
	o := &outputNode{typ: typ, refCounts: refCounts, source: jsval.Str(source), name: source}
	if refCounts != nil {
		if _, ok := refCounts[o.name]; !ok {
			refCounts[o.name] = 0
		}
	}
	return initNode(o, parent)
}

func (o *outputNode) clone() dfNode {
	c := &outputNode{typ: o.typ, refCounts: o.refCounts, source: o.source, name: "clone_" + o.name}
	c.base().self = c
	c.refCounts[c.name] = 0
	return c
}
func (o *outputNode) dependentFields() *sset { return newSset() }
func (o *outputNode) producedFields() *sset  { return newSset() }
func (o *outputNode) hash() string {
	if o.hashKey == "" {
		if o.cc != nil {
			o.cc.outputSeq++
			o.hashKey = "Output " + jsval.JSNumberString(float64(42+o.cc.outputSeq))
		} else {
			o.hashKey = fmt.Sprintf("Output %p", o)
		}
	}
	return o.hashKey
}
func (o *outputNode) getSource() Value {
	o.refCounts[o.name]++
	return o.source
}
func (o *outputNode) isRequired() bool  { return o.refCounts[o.name] != 0 }
func (o *outputNode) setSource(s Value) { o.source = s }

// ---- source ----

type sourceNode struct {
	dfBase
	data *Object
	// name is the data object's name as given (undefined when it has
	// none), of whatever type: upstream compares it with === and outputs it.
	name      Value
	generator bool
	// extra generator payloads live in graticuleNode/sequenceNode
}

func isUrlData(d Value) bool           { return hasProperty(d, "url") }
func isInlineData(d Value) bool        { return hasProperty(d, "values") }
func isSequenceGenerator(d Value) bool { return hasProperty(d, "sequence") }
func isSphereGenerator(d Value) bool   { return hasProperty(d, "sphere") }
func isGraticuleGenerator(d Value) bool {
	return hasProperty(d, "graticule")
}
func isGenerator(d Value) bool {
	return d.IsTruthy() && (isSequenceGenerator(d) || isSphereGenerator(d) || isGraticuleGenerator(d))
}
func isNamedData(d Value) bool {
	return hasProperty(d, "name") && !isUrlData(d) && !isInlineData(d) && !isGenerator(d)
}

// dataAsVL58 is what 5.8's data predicates make of an array given as data:
// they test with `in`, and 'values' in [] holds (Array.prototype.values), so
// the array is inline data whose values are that function.
func dataAsVL58(cc *compileCtx, data Value) Value {
	if cc.v5 && data.IsArr() {
		return mkv("values", jsval.NativeFunction("values"))
	}
	return data
}

func newSourceNode(cc *compileCtx, data Value) *sourceNode {
	if data.IsNullish() {
		data = mkv("name", "source")
	}
	data = dataAsVL58(cc, data)
	if cc.v5 && !isObject(data) {
		// 5.8's isGenerator (data && 'sequence' in data), then isInlineData.
		if data.IsTruthy() {
			jsIn(data, "sequence")
		}
		jsIn(data, "values")
	}
	s := &sourceNode{}
	s.cc = cc
	var format *Object
	if !isGenerator(data) {
		if f := data.Get("format"); f.IsTruthy() {
			format = cloneObj(omit(f, "parse"))
		} else {
			format = jsval.NewObject(0)
		}
	}
	switch {
	case isInlineData(data):
		s.data = mk("values", data.Get("values"))
	case isUrlData(data):
		s.data = mk("url", data.Get("url"))
		if !format.Lookup("type").IsTruthy() {
			ext := urlExtension(data.Get("url").AsString())
			if !contains([]string{"json", "csv", "tsv", "dsv", "topojson"}, ext) {
				ext = "json"
			}
			format.Set("type", jsval.Str(ext))
		}
	case isSphereGenerator(data):
		s.data = mk("values", arr(mkv("type", "Sphere")))
	case isNamedData(data) || isGenerator(data):
		s.data = jsval.NewObject(2)
	}
	s.generator = isGenerator(data)
	if n := data.Get("name"); n.IsTruthy() {
		s.name = n
	}
	if format != nil && format.Len() > 0 {
		if s.data == nil {
			throw("Cannot set properties of undefined (setting 'format')")
		}
		s.data.Set("format", jsval.Obj(format))
	}
	s.base().self = s
	return s
}

// urlExtension is /(?:\.([^.]+))?$/.exec(url)[1].
func urlExtension(url string) string {
	i := strings.LastIndex(url, ".")
	if i < 0 || i == len(url)-1 {
		return ""
	}
	return url[i+1:]
}

func (s *sourceNode) clone() dfNode          { throw("Cannot clone node"); return nil }
func (s *sourceNode) dependentFields() *sset { return newSset() }
func (s *sourceNode) producedFields() *sset  { return nil }
func (s *sourceNode) hash() string           { throw("Cannot hash sources"); return "" }
func (s *sourceNode) hasName() bool          { return s.name.IsTruthy() }
func (s *sourceNode) assemble() *Object {
	o := mk("name", s.name)
	spread(o, jsval.Obj(s.data))
	o.Set("transform", jsval.Arr(nil))
	return o
}

// ---- helpers for hashing ----

func setToHashString(cc *compileCtx, s *sset) string {
	// Set.prototype.toJSON as Vega-Lite patches it:
	// `Set(${[...this].map((x) => stringify(x)).join(',')})`, each name
	// JSON.stringify's (no U+2028 escape, unlike stringValue's).
	// A member added as a value other than a string (a group-by of ["a"])
	// is that value's stringify, as the Set holds it.
	parts := make([]string, 0, s.size())
	for _, x := range s.values() {
		cc.work(1)
		parts = append(parts, stringify(cc, x))
	}
	return "Set(" + strings.Join(parts, ",") + ")"
}

// moveChildrenLive reparents b's children to dst the way upstream does:
// `for (const child of other.children) { other.removeChild(child); child.parent = dst }`
// walks the array while removing from it, so every second child is skipped
// (they stay behind and are moved by the following remove()).
func (b *dfBase) moveChildrenLive(dst dfNode) {
	for i := 0; i < b.numChildren(); i++ {
		child := b.child(i)
		b.removeChild(child)
		child.base().setParent(dst)
	}
}
