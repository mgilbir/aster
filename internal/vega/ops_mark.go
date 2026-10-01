package vega

import (
	"github.com/mgilbir/aster/internal/jssort"
	"slices"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
	"github.com/mgilbir/aster/internal/transforms"
)

func init() {
	transformFactories["datajoin"] = facDataJoin
	transformFactories["itemstore"] = facItemStore
	transformFactories["mark"] = facMark
	transformFactories["encode"] = facEncode
	transformFactories["sortitems"] = facSortItems
	transformFactories["bound"] = facBound
	transformFactories["render"] = facRender
}

// itemPulse builds an item-flow pulse.
func itemPulse(in *flowPulse, items, add, rem, mod []*scene.Item) *flowPulse {
	return &flowPulse{
		stamp: in.stamp, encode: in.encode, items: items,
		add: add, rem: rem, mod: mod,
		changed: len(add) > 0 || len(rem) > 0 || len(mod) > 0,
	}
}

// -- data join -----------------------------------------------------------------

// joinEntry is the item bound to one key.
type joinEntry struct {
	item *scene.Item
	exit bool
	seen int // run in which a tuple last mapped to this entry
}

// joinMap is the DataJoin state: key -> item. Keys are tuple identity by
// default, or the string a key accessor returns.
type joinMap struct {
	keyFn func(jsval.Value) string // nil: tuple identity
	objs  map[*jsval.Object]*joinEntry
	vals  map[jsval.Value]*joinEntry
	strs  map[string]*joinEntry
	order []*joinEntry
	run   int
}

func (m *joinMap) size() int { return len(m.order) }

func (m *joinMap) find(t jsval.Value) *joinEntry {
	if m.keyFn != nil {
		return m.strs[m.keyFn(t)]
	}
	if o := t.ObjValue(); o != nil {
		return m.objs[o]
	}
	return m.vals[t]
}

func (m *joinMap) put(t jsval.Value, e *joinEntry) {
	switch {
	case m.keyFn != nil:
		m.strs[m.keyFn(t)] = e
	case t.ObjValue() != nil:
		m.objs[t.ObjValue()] = e
	default:
		m.vals[t] = e
	}
	m.order = append(m.order, e)
}

// lookup finds the item of a datum by the join key (`map.lookup`).
func (m *joinMap) lookup(t jsval.Value) *scene.Item {
	if e := m.find(t); e != nil {
		return e.item
	}
	return nil
}

func facDataJoin(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		jm, _ := n.value.(*joinMap)
		if jm == nil {
			jm = &joinMap{objs: map[*jsval.Object]*joinEntry{}, vals: map[jsval.Value]*joinEntry{}, strs: map[string]*joinEntry{}}
			switch k := p.Get("key").(type) {
			case transforms.Field:
				g, v := k.Get, n.g.view
				jm.keyFn = func(t jsval.Value) string { return v.jsString(g(t)) }
			case transforms.KeyFunc:
				jm.keyFn = func(t jsval.Value) string { return k(t) }
			}
			n.value = jm
		} else if p.Modified("key") {
			fail("DataJoin does not support modified key function or fields.")
		}

		tuples := pulse.tuples
		if pulse.items != nil {
			// A mark derived from another mark reads its items as tuples: the
			// ones already joined in join order, then the entering ones in the
			// order they were added (not the order the source mark sorted
			// them into).
			tuples = n.g.view.derivedTuples(jm, pulse)
		}
		jm.run++
		var add, mod []*scene.Item
		var slab []scene.Item
		var entries []joinEntry
		for i, t := range tuples {
			if i&1023 == 0 {
				if err := n.g.ctx.Err(); err != nil {
					failErr(err)
				}
			}
			x := jm.find(t)
			if x != nil {
				if x.exit {
					add = append(add, x.item)
				} else {
					mod = append(mod, x.item)
				}
			} else {
				if len(slab) == 0 {
					chunk := min(len(tuples)-i, 4096)
					slab = make([]scene.Item, chunk)
					entries = make([]joinEntry, chunk)
					n.g.view.countItems(chunk)
				}
				it := &slab[0]
				it.Bounds = scene.NewBounds()
				n.g.view.itemSeq++
				it.Seq = n.g.view.itemSeq
				slab = slab[1:]
				x = &entries[0]
				entries = entries[1:]
				x.item = it
				jm.put(t, x)
				add = append(add, it)
			}
			x.item.Datum = t
			x.exit = false
			x.seen = jm.run
		}

		var rem []*scene.Item
		for _, x := range jm.order {
			if x.seen != jm.run && !x.exit {
				rem = append(rem, x.item)
				x.exit = true
			}
		}
		return itemPulse(pulse, nil, add, rem, mod)
	}), nil
}

// -- item collection -------------------------------------------------------------

// facItemStore is Collect over visual items: it keeps the ordered list of live
// items, appending entering ones and dropping exiting ones.
func facItemStore(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return []*scene.Item(nil), trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		list, _ := n.value.([]*scene.Item)
		changed := len(pulse.add) > 0 || len(pulse.rem) > 0 || len(pulse.mod) > 0
		if len(pulse.rem) > 0 {
			gone := make(map[*scene.Item]struct{}, len(pulse.rem))
			for _, it := range pulse.rem {
				gone[it] = struct{}{}
			}
			kept := make([]*scene.Item, 0, len(list))
			for _, it := range list {
				if _, ok := gone[it]; !ok {
					kept = append(kept, it)
				}
			}
			list = kept
		}
		if len(pulse.add) > 0 {
			// Copy so that slices handed to marks earlier are not appended into.
			list = append(slices.Clip(list), pulse.add...)
		}
		n.value = list
		n.modified = changed
		out := itemPulse(pulse, list, pulse.add, pulse.rem, pulse.mod)
		return out
	}), nil
}

// -- mark -------------------------------------------------------------------------

func facMark(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return (*scene.Mark)(nil), trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		mark, _ := n.value.(*scene.Mark)
		ctx := c
		if mark == nil {
			def, _ := p.Get("markdef").(*markDef)
			if def == nil {
				fail("mark operator without a mark definition")
			}
			group := lookupGroup(p)
			idx := int(p.Value("index").NumValue())
			if group == nil {
				group = c.view.sg.RootItem()
			}
			// group.items[index] = mark: marks are stored by their position in
			// the specification, whatever order they are created in.
			for len(group.Items) <= idx {
				group.Items = append(group.Items, nil)
			}
			mark = c.view.sg.AddMark(scene.MarkDef{
				Type: def.typ, Name: def.name, Role: def.role,
				Zindex: def.zindex, Aria: def.aria, Description: def.description,
			}, group, idx)
			if ctx.group == nil {
				ctx.group = mark.Group
			}
			n.value = mark
			applyMarkFlags(mark, p, c.view)
			c.view.registerMark(mark, ctx)
		}
		for _, it := range pulse.add {
			it.Mark = mark
			if mark.Type == scene.MarkGroup && it.Items == nil {
				it.Items = []*scene.Mark{}
			}
		}
		out := pulse
		if p.Modified("clip") || p.Modified("interactive") {
			applyMarkFlags(mark, p, c.view)
			out = &flowPulse{}
			*out = *pulse
			out.reflow = true
		}
		mark.Items = pulse.items
		return out
	}), nil
}

// lookupGroup finds the group item a new mark belongs in: the only group item
// of the parent mark, or the one bound to the parent datum.
func lookupGroup(p *opParams) *scene.Item {
	g, _ := p.Get("groups").(*joinMap)
	if g == nil {
		return nil
	}
	if g.size() == 1 {
		return g.order[0].item
	}
	parent := p.Value("parent")
	if parent.IsUndefined() && !p.Has("parent") {
		return nil
	}
	return g.lookup(parent)
}

func applyMarkFlags(mark *scene.Mark, p *opParams, view *runView) {
	mark.ClipPath = nil
	switch v := p.Get("clip").(type) {
	case jsval.Value:
		mark.Clip = v.IsTruthy()
		if fn := view.shapeOf(v); fn != nil {
			mark.ClipPath = func(ctx scene.PathContext) string { return fn(ctx, nil) }
		}
	case bool:
		mark.Clip = v
	}
	if v, ok := p.Get("interactive").(jsval.Value); ok {
		mark.NonInteractive = !v.IsTruthy()
	}
}

// -- encode -----------------------------------------------------------------------

// boundSet is an encoding set instantiated in a context.
type boundSet struct {
	set *encodeSet
	ctx *rtContext
	ev  encEval
}

// run encodes one item, reporting whether any property changed.
func (b *boundSet) run(it *scene.Item) bool {
	set := b.set
	b.ev.ctx, b.ev.item, b.ev.datum = b.ctx, it, it.Datum
	ev := &b.ev
	mod := false
	for i := range set.channels {
		ch := &set.channels[i]
		val := ch.fn(ev)
		if ch.name == "shape" {
			// a shape generator (geoShape/pathShape) is a function, not a value
			if fn := b.ctx.view.shapeOf(val); fn != nil {
				it.Shape = scene.Shape{Func: fn}
				mod = true
				continue
			}
		}
		if setItemProp(it, ch.name, val) {
			mod = true
		}
	}
	if adjustSpatial(it, set) {
		mod = true
	}
	return mod
}

// adjustSpatial derives x/width, y/height from the x2/xc/y2/yc channels (rule
// marks keep their x2/y2). It runs after all channels are set, exactly like the
// generated encoder code.
func adjustSpatial(it *scene.Item, set *encodeSet) bool {
	mt := set.marktype
	if mt == "rule" {
		return false
	}
	swap := mt == "group" || mt == "image" || mt == "rect"
	mod := false
	setNum := func(name string, v float64) {
		if setItemProp(it, name, jsval.Num(v)) {
			mod = true
		}
	}
	if set.x2 {
		if set.x {
			if swap && jsval.ToNumber(getItemProp(it, "x")) > jsval.ToNumber(getItemProp(it, "x2")) {
				x, x2 := getItemProp(it, "x"), getItemProp(it, "x2")
				setItemProp(it, "x", x2)
				setItemProp(it, "x2", x)
				mod = true
			}
			setNum("width", jsval.ToNumber(getItemProp(it, "x2"))-jsval.ToNumber(getItemProp(it, "x")))
		} else {
			w := jsToNumOr0(getItemProp(it, "width"))
			setNum("x", jsval.ToNumber(getItemProp(it, "x2"))-w)
		}
	}
	if set.xc {
		w := jsToNumOr0(getItemProp(it, "width"))
		setNum("x", xcOf(it)-w/2)
	}
	if set.y2 {
		if set.y {
			if swap && jsval.ToNumber(getItemProp(it, "y")) > jsval.ToNumber(getItemProp(it, "y2")) {
				y, y2 := getItemProp(it, "y"), getItemProp(it, "y2")
				setItemProp(it, "y", y2)
				setItemProp(it, "y2", y)
				mod = true
			}
			setNum("height", jsval.ToNumber(getItemProp(it, "y2"))-jsval.ToNumber(getItemProp(it, "y")))
		} else {
			h := jsToNumOr0(getItemProp(it, "height"))
			setNum("y", jsval.ToNumber(getItemProp(it, "y2"))-h)
		}
	}
	if set.yc {
		h := jsToNumOr0(getItemProp(it, "height"))
		setNum("y", ycOf(it)-h/2)
	}
	return mod
}

// jsToNumOr0 is `(o.width||0)`.
func jsToNumOr0(v jsval.Value) float64 {
	if !v.IsTruthy() {
		return 0 // undefined, null, false, "", 0 and NaN
	}
	return jsval.ToNumber(v) // a word or an object stays NaN: `{}/2` is NaN
}

// xc and yc are encoded channels that the item type does not model; they are
// kept in Extra by setItemProp.
func xcOf(it *scene.Item) float64 { return jsval.ToNumber(getItemProp(it, "xc")) }
func ycOf(it *scene.Item) float64 { return jsval.ToNumber(getItemProp(it, "yc")) }

// bindEncoders instantiates an Encode operator's encoder table.
func (c *rtContext) bindEncoders(pe pEncode, b *paramBuilder) map[string]*boundSet {
	out := make(map[string]*boundSet, len(pe.sets))
	for name, s := range pe.sets {
		out[name] = &boundSet{set: s, ctx: c}
	}
	return out
}

func facEncode(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		encoders, _ := p.Get("encoders").(map[string]*boundSet)
		fmod := p.Value("mod").IsTruthy()
		encode := pulse.encode

		reenter := encode == "enter"
		update, enter, exit := encoders["update"], encoders["enter"], encoders["exit"]
		set := update
		if encode != "" && !reenter {
			set = encoders[encode]
		}

		var outMod []*scene.Item
		if len(pulse.add) > 0 {
			for _, t := range pulse.add {
				if enter != nil {
					enter.run(t)
				}
				if update != nil {
					update.run(t)
				}
			}
			if set != nil && set != update {
				for _, t := range pulse.add {
					set.run(t)
				}
			}
		}
		if len(pulse.rem) > 0 && exit != nil {
			for _, t := range pulse.rem {
				exit.run(t)
			}
		}
		if reenter || set != nil {
			items := pulse.mod
			if p.Modified() {
				items = reflowItems(pulse)
			}
			if reenter {
				for _, t := range items {
					mod := fmod
					if enter != nil && enter.run(t) {
						mod = true
					}
					if set != nil && set.run(t) {
						mod = true
					}
					if mod {
						outMod = append(outMod, t)
					}
				}
			} else {
				for _, t := range items {
					if set.run(t) || fmod {
						outMod = append(outMod, t)
					}
				}
			}
		}
		out := itemPulse(pulse, pulse.items, pulse.add, pulse.rem, outMod)
		if !out.changed {
			return stopPulse
		}
		return out
	}), nil
}

// reflowItems is the set Pulse.visit(MOD|REFLOW) reaches: the modified items
// plus every source item that is neither added nor modified.
func reflowItems(p *flowPulse) []*scene.Item {
	src := p.items
	sum := len(p.add) + len(p.mod)
	if sum == len(src) {
		return p.mod
	}
	if sum == 0 {
		return src
	}
	skip := make(map[*scene.Item]struct{}, sum)
	for _, it := range p.add {
		skip[it] = struct{}{}
	}
	for _, it := range p.mod {
		skip[it] = struct{}{}
	}
	out := append([]*scene.Item(nil), p.mod...)
	for _, it := range src {
		if _, ok := skip[it]; !ok {
			out = append(out, it)
		}
	}
	return out
}

func facSortItems(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		cmp := p.comparator("sort")
		if cmp == nil {
			// A comparator over no fields is null (vega-util's compare).
			// SortItems then sorts with stableCompare(null), null, which
			// Array.prototype.sort rejects; when nothing asks for a sort it
			// still reads null.fields.
			if p.Modified("sort") || len(pulse.add) > 0 {
				fail("The comparison function must be either a function or undefined: null")
			}
			fail("Cannot read properties of null (reading 'fields')")
		}
		mod := p.Modified("sort") || len(pulse.add) > 0 || len(pulse.mod) > 0 || len(pulse.rem) > 0
		if mod && cmp != nil {
			// The comparator's fields are paths into the item (datum.x, x, ...).
			v := n.g.view
			views := make(map[*scene.Item]jsval.Value, len(pulse.items))
			for _, it := range pulse.items {
				views[it] = v.itemTuple(it)
			}
			// stableCompare: ties go by tuple id, the order the items were created.
			jssort.Sort(pulse.items, func(a, b *scene.Item) int {
				if c := cmp(views[a], views[b]); c != 0 {
					return c
				}
				switch {
				case a.Seq < b.Seq:
					return -1
				case a.Seq > b.Seq:
					return 1
				}
				return 0
			})
		}
		n.modified = mod
		return pulse
	}), nil
}

func facBound(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		mark, _ := p.Get("mark").(*scene.Mark)
		if mark == nil {
			return nil
		}
		changed := pulse.changed || p.Modified() || mark.Type == scene.MarkGroup || mark.Type.Nested()
		if changed {
			if err := n.g.view.bounder.BoundMark(mark); err != nil {
				failErr(err)
			}
		}
		// A group's bounds are recomputed on every run (and any mark's when its
		// parameters were modified); for axes, legends and titles the pulse
		// is reflowed to carry the layout change to the enclosing layout.
		if (mark.Type == scene.MarkGroup || p.Modified()) && !mark.Type.Nested() {
			switch mark.Role {
			case "axis", "legend", "title":
				return reflowPulse(pulse)
			}
		}
		return nil
	}), nil
}

func facRender(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse { return nil }), nil
}

// -- items as values ---------------------------------------------------------------

// itemAsValue presents an item to expressions: its datum, properties and the
// mark (with its group chain and item list) it belongs to.
func (v *runView) itemAsValue(it *scene.Item) jsval.Value {
	if it == nil {
		return jsval.Undefined
	}
	o := jsval.NewObject(16)
	eachItemProp(it, func(name string, val jsval.Value) { o.Set(name, val) })
	o.Set("datum", it.Datum)
	o.Set("$id", jsval.Int(v.itemHandle(it)))
	if it.Mark != nil {
		o.Set("mark", v.markAsValue(it.Mark, true))
	}
	return jsval.Obj(o)
}

// markAsValue presents a mark: its type, name and role, its items (as tuple
// views) and, when asked, the group item it lives in.
func (v *runView) markAsValue(m *scene.Mark, withGroup bool) jsval.Value {
	if m == nil {
		return jsval.Undefined
	}
	o := jsval.NewObject(8)
	o.Set("marktype", jsval.Str(m.Type.String()))
	if m.Name != "" {
		o.Set("name", jsval.Str(m.Name))
	}
	if m.Role != "" {
		o.Set("role", jsval.Str(m.Role))
	}
	o.Set("items", v.markItemsValue(m))
	if withGroup && m.Group != nil {
		o.Set("group", v.itemAsValue(m.Group))
	}
	return jsval.Obj(o)
}

// itemTuple is an item as a tuple, for marks and transforms fed from another
// mark's items. The tuple object is stable per item and refreshed on each call.
func (v *runView) itemTuple(it *scene.Item) jsval.Value {
	if v.itemTupleCache == nil {
		v.itemTupleCache = map[*scene.Item]*jsval.Object{}
	}
	o := v.itemTupleCache[it]
	if o == nil {
		o = jsval.NewObject(16)
		v.itemTupleCache[it] = o
		if v.viewItem == nil {
			v.viewItem = map[*jsval.Object]*scene.Item{}
		}
		v.viewItem[o] = it
	}
	eachItemProp(it, func(name string, val jsval.Value) { o.Set(name, val) })
	o.Set("datum", it.Datum)
	b := it.Bounds
	o.Set("bounds", obj("x1", jsval.Num(b.X1), "y1", jsval.Num(b.Y1), "x2", jsval.Num(b.X2), "y2", jsval.Num(b.Y2)))
	return jsval.Obj(o)
}

// markItemsValue is `mark.items` as expressions read it: the items as tuple
// views. A mark can hold many items and an expression may be evaluated for each
// of them, so the array is built once per item list and the views are not
// refreshed (typically only its length is read).
func (v *runView) markItemsValue(m *scene.Mark) jsval.Value {
	if v.markItemsCache == nil {
		v.markItemsCache = map[*scene.Mark]markItems{}
	}
	var first *scene.Item
	if len(m.Items) > 0 {
		first = m.Items[0]
	}
	if c, ok := v.markItemsCache[m]; ok && c.n == len(m.Items) && c.first == first {
		return c.val
	}
	items := make([]jsval.Value, len(m.Items))
	for i, it := range m.Items {
		if it == nil {
			continue
		}
		o := v.itemTupleCache[it]
		if o == nil {
			items[i] = v.itemTuple(it)
			continue
		}
		items[i] = jsval.Obj(o)
	}
	val := jsval.Arr(items)
	v.markItemsCache[m] = markItems{n: len(m.Items), first: first, val: val}
	return val
}

type markItems struct {
	n     int
	first *scene.Item
	val   jsval.Value
}

// derivedTuples lists the tuples of the items a derived mark is joined to.
func (v *runView) derivedTuples(jm *joinMap, pulse *flowPulse) []jsval.Value {
	live := make(map[*scene.Item]struct{}, len(pulse.items))
	for _, it := range pulse.items {
		live[it] = struct{}{}
	}
	out := make([]jsval.Value, 0, len(pulse.items))
	seen := make(map[*scene.Item]struct{}, len(pulse.items))
	for _, e := range jm.order {
		d := e.item.Datum.ObjValue()
		if d == nil {
			continue
		}
		it := v.viewItem[d]
		if it == nil {
			continue
		}
		if _, ok := live[it]; ok {
			out = append(out, v.itemTuple(it))
			seen[it] = struct{}{}
		}
	}
	for _, it := range pulse.add {
		if _, ok := seen[it]; !ok {
			out = append(out, v.itemTuple(it))
			seen[it] = struct{}{}
		}
	}
	// items neither joined nor added (a reflow of an unrelated change)
	for _, it := range pulse.items {
		if _, ok := seen[it]; !ok {
			out = append(out, v.itemTuple(it))
		}
	}
	return out
}
