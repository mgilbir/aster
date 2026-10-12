package vegalite

// Dataflow optimizer — vega-lite/src/compile/data/{optimize,optimizer,optimizers,subtree}.ts.
// Node lists are walked with live indices where upstream iterates arrays that
// the loop body mutates, so passes see the same nodes upstream's do.

import "strconv"

const facetScalePrefix = "scale_"

const maxOptimizationRuns = 5

// optimizerFlag records whether a pass changed anything.
type optimizerFlag struct{ modified bool }

func (o *optimizerFlag) setModified() { o.modified = true }

// topDown runs run on node and then on its (live) children.
func topDown(o *optimizerFlag, run func(n dfNode), node dfNode) bool {
	var visit func(n dfNode)
	visit = func(n dfNode) {
		cc := n.base().cc
		cc.work(1)
		run(n)
		for i := 0; i < n.base().numChildren(); i++ {
			visit(n.base().child(i))
		}
	}
	visit(node)
	return o.modified
}

// bottomUp runs run on every node of the subtree, deepest first. Upstream
// collects the nodes depth-first and stable-sorts them by decreasing depth; a
// counting sort on the depth is that stable sort, in linear time.
func bottomUp(o *optimizerFlag, run func(n dfNode), node dfNode) bool {
	cc := node.base().cc
	type nd struct {
		n     dfNode
		depth int
	}
	var list []nd
	maxDepth := 0
	var walk func(n dfNode, d int)
	walk = func(n dfNode, d int) {
		cc.work(1)
		list = append(list, nd{n, d})
		maxDepth = max(maxDepth, d)
		for _, c := range n.base().children() {
			walk(c, d+1)
		}
	}
	walk(node, 0)
	// start[d] is where the nodes of depth d begin in the sorted order.
	start := make([]int, maxDepth+2)
	for _, e := range list {
		start[maxDepth-e.depth+1]++
	}
	for d := 1; d < len(start); d++ {
		start[d] += start[d-1]
	}
	sorted := make([]dfNode, len(list))
	for _, e := range list {
		k := maxDepth - e.depth
		sorted[start[k]] = e.n
		start[k]++
	}
	for _, n := range sorted {
		cc.work(1)
		run(n)
	}
	return o.modified
}

func runOptimizer(f func(root dfNode) bool, nodes []dfNode) bool {
	modified := false
	for _, n := range nodes {
		r := f(n)
		modified = r || modified
	}
	return modified
}

func nonEmptyRoots(roots []dfNode) []dfNode {
	var out []dfNode
	for _, r := range roots {
		if r.base().numChildren() > 0 {
			out = append(out, r)
		}
	}
	return out
}

func optimizationDataflowHelper(dc *dataComponent, m Model, firstPass bool) bool {
	roots := dc.sources.items
	modified := false

	{
		o := &optimizerFlag{}
		modified = runOptimizer(func(n dfNode) bool {
			return topDown(o, func(x dfNode) {
				if out, ok := x.(*outputNode); ok && !out.isRequired() {
					o.setModified()
					out.remove()
				}
			}, n)
		}, roots) || modified
	}
	{
		o := &optimizerFlag{}
		needsID := requiresSelectionID(m)
		modified = runOptimizer(func(n dfNode) bool {
			return topDown(o, func(x dfNode) {
				if id, ok := x.(*identifierNode); ok {
					p := id.par
					_, isParse := p.(*parseNode)
					_, isAgg := p.(*aggregateNode)
					if !(needsID && (isDataSourceNode(p) || isAgg || isParse)) {
						o.setModified()
						id.remove()
					}
				}
			}, n)
		}, roots) || modified
	}
	roots = nonEmptyRoots(roots)
	{
		o := &optimizerFlag{}
		modified = runOptimizer(func(n dfNode) bool {
			return bottomUp(o, func(x dfNode) {
				switch x.(type) {
				case *outputNode, *facetNode:
					return
				case *sourceNode:
					return
				}
				if x.base().numChildren() > 0 {
					return
				}
				o.setModified()
				x.base().remove()
			}, n)
		}, roots) || modified
	}
	roots = nonEmptyRoots(roots)
	if !firstPass {
		modified = runOptimizer(moveParseUpFn(), roots) || modified
		modified = runOptimizer(mergeBinsFn(m), roots) || modified
		modified = runOptimizer(removeDuplicateTimeUnitsFn(), roots) || modified
		modified = runOptimizer(mergeParseFn(), roots) || modified
		modified = runOptimizer(mergeAggregatesFn(), roots) || modified
		modified = runOptimizer(mergeTimeUnitsFn(), roots) || modified
		modified = runOptimizer(mergeIdenticalNodesFn(), roots) || modified
		modified = runOptimizer(mergeOutputsFn(), roots) || modified
	}
	dc.sources.items = roots
	return modified
}

func optimizeDataflow(dc *dataComponent, m Model) {
	cc := m.b().ctx
	first, second := 0, 0
	for i := 0; i < maxOptimizationRuns; i++ {
		cc.check()
		if !optimizationDataflowHelper(dc, m, true) {
			break
		}
		first++
	}
	for _, r := range dc.sources.items {
		cc.work(1)
		moveFacetDown(r)
	}
	for i := 0; i < maxOptimizationRuns; i++ {
		if !optimizationDataflowHelper(dc, m, false) {
			break
		}
		second++
	}
	if max(first, second) == maxOptimizationRuns {
		cc.warn("Maximum optimization runs(" + strconv.Itoa(maxOptimizationRuns) + ") reached.")
	}
}

func moveParseUpFn() func(root dfNode) bool {
	o := &optimizerFlag{}
	return func(root dfNode) bool {
		cc := root.base().cc
		return bottomUp(o, func(node dfNode) {
			if isDataSourceNode(node) {
				return
			}
			if node.base().numChildren() > 1 {
				return
			}
			for i := 0; i < node.base().numChildren(); i++ {
				cc.work(1)
				child := node.base().child(i)
				if pc, ok := child.(*parseNode); ok {
					if np, ok := node.(*parseNode); ok {
						o.setModified()
						np.merge(pc)
					} else {
						if fieldIntersection(node.base().cc, node.producedFields(), child.dependentFields()) {
							continue
						}
						o.setModified()
						child.base().swapWithParent()
					}
				}
			}
		}, root)
	}
}

func mergeBinsFn(m Model) func(root dfNode) bool {
	o := &optimizerFlag{}
	return func(root dfNode) bool {
		cc := root.base().cc
		return bottomUp(o, func(node dfNode) {
			moveBinsUp := true
			switch node.(type) {
			case *filterNode, *parseNode, *identifierNode:
				moveBinsUp = false
			}
			if isDataSourceNode(node) {
				moveBinsUp = false
			}
			var promotable, remaining []*binNode
			for _, child := range node.base().children() {
				cc.work(1)
				if bn, ok := child.(*binNode); ok {
					if moveBinsUp && !fieldIntersection(node.base().cc, node.producedFields(), child.dependentFields()) {
						promotable = append(promotable, bn)
					} else {
						remaining = append(remaining, bn)
					}
				}
			}
			rename := m.b().renameSignal
			if len(promotable) > 0 {
				promoted := promotable[len(promotable)-1]
				promotable = promotable[:len(promotable)-1]
				for _, bin := range promotable {
					cc.work(1)
					promoted.merge(bin, rename)
				}
				o.setModified()
				if nb, ok := node.(*binNode); ok {
					nb.merge(promoted, rename)
				} else {
					promoted.swapWithParent()
				}
			}
			if len(remaining) > 1 {
				rem := remaining[len(remaining)-1]
				remaining = remaining[:len(remaining)-1]
				for _, bin := range remaining {
					cc.work(1)
					rem.merge(bin, rename)
				}
				o.setModified()
			}
		}, root)
	}
}

func removeDuplicateTimeUnitsFn() func(root dfNode) bool {
	o := &optimizerFlag{}
	return func(root dfNode) bool {
		cc := root.base().cc
		var run func(node dfNode, fields *sset)
		run = func(node dfNode, timeUnitFields *sset) {
			cc.work(1)
			produced := newSset()
			if tu, ok := node.(*timeUnitNode); ok {
				produced = tu.producedFields()
				if hasIntersection(produced, timeUnitFields) {
					o.setModified()
					tu.removeFormulas(timeUnitFields)
					// upstream tests `node.producedFields.length === 0`, the arity of a
					// method, which is always 0: the node is always removed here.
					tu.remove()
				}
			}
			for _, child := range append([]dfNode(nil), node.base().children()...) {
				next := timeUnitFields.clone()
				for _, f := range produced.list() {
					cc.work(1)
					next.add(f)
				}
				run(child, next)
			}
		}
		run(root, newSset())
		return o.modified
	}
}

func mergeParseFn() func(root dfNode) bool {
	o := &optimizerFlag{}
	return func(root dfNode) bool {
		cc := root.base().cc
		return bottomUp(o, func(node dfNode) {
			original := append([]dfNode(nil), node.base().children()...)
			var parseChildren []*parseNode
			for _, c := range node.base().children() {
				cc.work(1)
				if pn, ok := c.(*parseNode); ok {
					parseChildren = append(parseChildren, pn)
				}
			}
			if node.base().numChildren() > 1 && len(parseChildren) >= 1 {
				common := newOmap[Value]()
				conflicting := newSset()
				for _, pn := range parseChildren {
					cc.work(1)
					for _, k := range pn.parse.Keys() {
						cc.work(1)
						v := pn.parse.Lookup(k)
						if cv, ok := common.get(k); !ok {
							common.set(k, v)
						} else if !strictEq(cv, v) {
							conflicting.add(k)
						}
					}
				}
				for _, f := range conflicting.list() {
					cc.work(1)
					common.del(f)
				}
				if common.len() > 0 {
					o.setModified()
					co := newOrderedObject(node.base().cc, common)
					mergedParse := newParseNode(node, co)
					for _, child := range original {
						cc.work(1)
						if pn, ok := child.(*parseNode); ok {
							for _, k := range common.keysView() {
								cc.work(1)
								pn.parse.Delete(k)
							}
						}
						node.base().removeChild(child)
						child.base().setParent(mergedParse)
						if pn, ok := child.(*parseNode); ok && pn.parse.Len() == 0 {
							pn.remove()
						}
					}
				}
			}
		}, root)
	}
}

func newOrderedObject(cc *compileCtx, m *omap[Value]) *Object {
	o := mk()
	for _, k := range m.keysView() {
		cc.work(1)
		jsSet(cc, o, k, m.m[k])
	}
	return o
}

func mergeAggregatesFn() func(root dfNode) bool {
	o := &optimizerFlag{}
	return func(root dfNode) bool {
		cc := root.base().cc
		return bottomUp(o, func(node dfNode) {
			groups := newOmap[[]*aggregateNode]()
			for _, c := range node.base().children() {
				cc.work(1)
				if agg, ok := c.(*aggregateNode); ok {
					k := setToHashString(node.base().cc, agg.groupBy())
					list, _ := groups.get(k)
					groups.set(k, append(list, agg))
				}
			}
			for _, k := range groups.keysView() {
				cc.work(1)
				mergeable := groups.m[k]
				if len(mergeable) > 1 {
					mergedAggs := mergeable[len(mergeable)-1]
					mergeable = mergeable[:len(mergeable)-1]
					for _, agg := range mergeable {
						cc.work(1)
						if mergedAggs.merge(agg) {
							node.base().removeChild(agg)
							agg.base().setParent(mergedAggs)
							agg.remove()
							o.setModified()
						}
					}
				}
			}
		}, root)
	}
}

func mergeTimeUnitsFn() func(root dfNode) bool {
	o := &optimizerFlag{}
	return func(root dfNode) bool {
		cc := root.base().cc
		return bottomUp(o, func(node dfNode) {
			var tus []*timeUnitNode
			for _, c := range node.base().children() {
				cc.work(1)
				if t, ok := c.(*timeUnitNode); ok {
					tus = append(tus, t)
				}
			}
			if len(tus) == 0 {
				return
			}
			combination := tus[len(tus)-1]
			tus = tus[:len(tus)-1]
			for _, t := range tus {
				cc.work(1)
				o.setModified()
				combination.merge(t)
			}
		}, root)
	}
}

func mergeIdenticalNodesFn() func(root dfNode) bool {
	o := &optimizerFlag{}
	return func(root dfNode) bool {
		cc := root.base().cc
		return topDown(o, func(node dfNode) {
			buckets := newOmap[[]dfNode]()
			for _, c := range node.base().children() {
				cc.work(1)
				h := c.hash()
				list, _ := buckets.get(h)
				buckets.set(h, append(list, c))
			}
			for _, k := range buckets.keysView() {
				cc.work(1)
				nodes := buckets.m[k]
				if len(nodes) > 1 {
					o.setModified()
					merged := nodes[0]
					for _, n := range nodes[1:] {
						cc.work(1)
						node.base().removeChild(n)
						n.base().setParent(merged)
						n.base().remove()
					}
				}
			}
		}, root)
	}
}

func mergeOutputsFn() func(root dfNode) bool {
	o := &optimizerFlag{}
	return func(root dfNode) bool {
		cc := root.base().cc
		return bottomUp(o, func(node dfNode) {
			children := append([]dfNode(nil), node.base().children()...)
			hasOutputChild := false
			for _, c := range children {
				cc.work(1)
				if _, ok := c.(*outputNode); ok {
					hasOutputChild = true
				}
			}
			if !hasOutputChild || node.base().numChildren() <= 1 {
				return
			}
			var other []dfNode
			var mainOutput dfNode
			for _, child := range children {
				cc.work(1)
				if _, ok := child.(*outputNode); ok {
					lastOutput := child
					for lastOutput.base().numChildren() == 1 {
						cc.work(1)
						the := lastOutput.base().child(0)
						if _, ok := the.(*outputNode); ok {
							lastOutput = the
						} else {
							break
						}
					}
					other = append(other, lastOutput.base().children()...)
					if mainOutput != nil {
						node.base().removeChild(child)
						child.base().setParent(mainOutput.base().par)
						mainOutput.base().par.base().removeChild(mainOutput)
						mainOutput.base().setParent(lastOutput)
						o.setModified()
					} else {
						mainOutput = lastOutput
					}
				} else {
					other = append(other, child)
				}
			}
			if len(other) > 0 {
				o.setModified()
				for _, child := range other {
					cc.work(1)
					child.base().par.base().removeChild(child)
					child.base().setParent(mainOutput)
				}
			}
		}, root)
	}
}

// ---- facet subtree ----

func isAddDimensionsNode(n dfNode) bool {
	switch n := n.(type) {
	case *aggregateNode, *stackNode:
		return true
	case *xformNode:
		k := n.kind
		return k == "window" || k == "joinaggregate"
	}
	return false
}

func addDimensionsTo(n dfNode, fields []string) {
	switch t := n.(type) {
	case *aggregateNode:
		t.addDimensions(fields)
	case *stackNode:
		t.addDimensions(fields)
	case *xformNode:
		t.addDimensions(fields)
	}
}

func cloneSubtree(facet *facetNode) func(node dfNode) []dfNode {
	cc := facet.cc
	var clone func(node dfNode) []dfNode
	clone = func(node dfNode) []dfNode {
		if _, isFacet := node.(*facetNode); !isFacet {
			cp := node.clone()
			cp.base().cc = node.base().cc
			if out, ok := cp.(*outputNode); ok {
				newName := facetScalePrefix + out.getSource()
				out.setSource(newName)
				facet.model.comp.data.outputNodes[newName] = out
			} else if isAddDimensionsNode(cp) {
				addDimensionsTo(cp, facet.fields())
			}
			for _, k := range node.base().children() {
				cc.work(1)
				for _, n := range clone(k) {
					n.base().setParent(cp)
				}
			}
			return []dfNode{cp}
		}
		var out []dfNode
		for _, k := range node.base().children() {
			cc.work(1)
			out = append(out, clone(k)...)
		}
		return out
	}
	return clone
}

func moveFacetDown(node dfNode) {
	cc := node.base().cc
	if f, ok := node.(*facetNode); ok {
		if f.numChildren() == 1 {
			if _, isOut := f.child(0).(*outputNode); !isOut {
				child := f.child(0)
				if isAddDimensionsNode(child) {
					addDimensionsTo(child, f.fields())
				}
				child.base().swapWithParent()
				moveFacetDown(node)
				return
			}
		}
		facetMain := f.model.comp.data.main
		moveMainDownToFacet(facetMain)
		cloner := cloneSubtree(f)
		var copies []dfNode
		for _, k := range f.children() {
			cc.work(1)
			copies = append(copies, cloner(k)...)
		}
		for _, c := range copies {
			cc.work(1)
			c.base().setParent(facetMain)
		}
		return
	}
	for _, k := range append([]dfNode(nil), node.base().children()...) {
		cc.work(1)
		moveFacetDown(k)
	}
}

func moveMainDownToFacet(node dfNode) {
	if out, ok := node.(*outputNode); ok && out.typ == dsMain {
		if out.numChildren() == 1 {
			child := out.child(0)
			if _, isFacet := child.(*facetNode); !isFacet {
				child.base().swapWithParent()
				moveMainDownToFacet(node)
			}
		}
	}
}
