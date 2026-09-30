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
type sourceGetter interface{ getSource() string }

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
	kids []dfNode
	par  dfNode
}

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

func (b *dfBase) numChildren() int { return len(b.kids) }

func (b *dfBase) indexOf(c dfNode) int {
	for i, k := range b.kids {
		if k == c {
			return i
		}
	}
	return -1
}

func (b *dfBase) addChild(c dfNode, loc int) {
	if b.indexOf(c) >= 0 {
		return
	}
	if loc >= 0 {
		if loc > len(b.kids) {
			loc = len(b.kids)
		}
		b.kids = append(b.kids, nil)
		copy(b.kids[loc+1:], b.kids[loc:])
		b.kids[loc] = c
		return
	}
	b.kids = append(b.kids, c)
}

// removeChild mirrors Array.prototype.splice(indexOf(x), 1), including its
// quirk that a missing child (index -1) removes the last one.
func (b *dfBase) removeChild(c dfNode) int {
	loc := b.indexOf(c)
	switch {
	case loc >= 0:
		b.kids = append(b.kids[:loc], b.kids[loc+1:]...)
	case len(b.kids) > 0:
		b.kids = b.kids[:len(b.kids)-1]
	}
	return loc
}

func (b *dfBase) remove() {
	if _, isSource := b.self.(*sourceNode); isSource {
		throw("Source nodes are roots and cannot be removed.")
	}
	loc := b.par.base().removeChild(b.self)
	for _, child := range append([]dfNode(nil), b.kids...) {
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
	for _, child := range append([]dfNode(nil), b.kids...) {
		child.base().setParent(parent)
	}
	b.kids = nil
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
	source    string
	name      string
	hashKey   string
}

func newOutputNode(parent dfNode, source string, typ dataSourceType, refCounts map[string]int) *outputNode {
	o := &outputNode{typ: typ, refCounts: refCounts, source: source, name: source}
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
func (o *outputNode) getSource() string {
	o.refCounts[o.name]++
	return o.source
}
func (o *outputNode) isRequired() bool   { return o.refCounts[o.name] != 0 }
func (o *outputNode) setSource(s string) { o.source = s }

// ---- source ----

type sourceNode struct {
	dfBase
	data      *Object
	name      string
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

func newSourceNode(cc *compileCtx, data Value) *sourceNode {
	if data.IsNullish() {
		data = mkv("name", "source")
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
		s.name = n.AsString()
	}
	if format != nil && format.Len() > 0 {
		s.data.Set("format", jsval.Obj(format))
	}
	if s.data == nil {
		s.data = jsval.NewObject(0)
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
func (s *sourceNode) hasName() bool          { return s.name != "" }
func (s *sourceNode) assemble() *Object {
	o := mk("name", s.name)
	spread(o, jsval.Obj(s.data))
	o.Set("transform", jsval.Arr(nil))
	return o
}

// ---- helpers for hashing ----

func setToHashString(s *sset) string {
	parts := make([]string, 0, s.size())
	for _, x := range s.list() {
		parts = append(parts, jsonString(x))
	}
	return "Set(" + strings.Join(parts, ",") + ")"
}

// moveChildrenLive reparents b's children to dst the way upstream does:
// `for (const child of other.children) { other.removeChild(child); child.parent = dst }`
// walks the array while removing from it, so every second child is skipped
// (they stay behind and are moved by the following remove()).
func (b *dfBase) moveChildrenLive(dst dfNode) {
	for i := 0; i < len(b.kids); i++ {
		child := b.kids[i]
		b.removeChild(child)
		child.base().setParent(dst)
	}
}
