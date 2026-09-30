package expr

import (
	"math"

	"github.com/mgilbir/aster/internal/jsval"
)

// The Vega-Lite selection functions of vega-selections, evaluated over a
// selection store dataset served by DataProvider.

const selectionID = "_vgsid_"

func (s *Scope) storeEntries(name string) []jsval.Value {
	if s.data == nil {
		return nil
	}
	v, ok := s.data.Data(name)
	if !ok {
		return nil
	}
	return v.Items()
}

// fieldGetter reads a field-definition's path off a datum with the semantics of
// vega-util's field(): a null intermediate is a TypeError.
func (s *Scope) readPath(datum jsval.Value, path string) jsval.Value {
	segs := jsval.ParseFieldPath(path)
	v := datum
	for _, seg := range segs {
		v = s.getProp(v, seg)
	}
	return v
}

// sameValueZero is Array.prototype.includes' equality: like === but NaN
// equals NaN.
func sameValueZero(a, b jsval.Value) bool {
	if a.IsNum() && b.IsNum() && math.IsNaN(a.NumValue()) && math.IsNaN(b.NumValue()) {
		return true
	}
	return strictEquals(a, b)
}

func includes(items []jsval.Value, v jsval.Value) bool {
	for _, it := range items {
		if sameValueZero(it, v) {
			return true
		}
	}
	return false
}

// dateToNumber is isDate(v) ? toNumber(v) : v.
func dateToNumber(v jsval.Value) jsval.Value {
	if v.IsTimestamp() {
		return jsval.Num(v.NumValue())
	}
	return v
}

// testPoint is selectionTest's per-entry test.
func (s *Scope) testPoint(datum, entry jsval.Value) bool {
	fields := s.getProp(entry, "fields")
	values := s.getProp(entry, "values")
	n := int(s.num(s.getProp(fields, "length")))
	for i := 0; i < n; i++ {
		f := s.getIndex(fields, jsval.Int(i))
		dval := dateToNumber(s.readPath(datum, s.str(s.getProp(f, "field"))))
		val := s.getIndex(values, jsval.Int(i))
		val = dateToNumber(val)
		if val.IsArr() && val.Len() > 0 && val.Index(0).IsTimestamp() {
			items := val.Items()
			conv := make([]jsval.Value, len(items))
			for j, it := range items {
				conv[j] = jsval.Num(s.num(it))
			}
			val = jsval.Arr(conv)
		}
		typ := s.str(s.getProp(f, "type"))
		switch typ {
		case "E":
			if val.IsArr() {
				if !includes(val.Items(), dval) {
					return false
				}
			} else if !strictEquals(dval, val) {
				return false
			}
		case "R":
			if !s.inRange(dval, val, true, true) {
				return false
			}
		case "R-RE":
			if !s.inRange(dval, val, true, false) {
				return false
			}
		case "R-E":
			if !s.inRange(dval, val, false, false) {
				return false
			}
		case "R-LE":
			if !s.inRange(dval, val, false, true) {
				return false
			}
		case "E-LT":
			if s.ge(dval, val) {
				return false
			}
		case "E-LTE":
			if s.gt(dval, val) {
				return false
			}
		case "E-GT":
			if s.le(dval, val) {
				return false
			}
		case "E-GTE":
			if s.lt(dval, val) {
				return false
			}
		case "E-VALID":
			if dval.IsNull() || math.IsNaN(s.num(dval)) {
				return false
			}
		case "E-ONE":
			if s.indexOfStrict(val, dval) == -1 {
				return false
			}
		}
	}
	return true
}

// indexOfStrict is `seq.indexOf(x)` for an array or string.
func (s *Scope) indexOfStrict(seq, x jsval.Value) int {
	switch {
	case seq.IsArr():
		for i, it := range seq.Items() {
			if strictEquals(it, x) {
				return i
			}
		}
		return -1
	case seq.IsStr():
		return indexOfUnits(seq.StrValue(), s.str(x), 0)
	}
	typeError("Cannot read properties of %s (reading 'indexOf')", seq.AsString())
	return -1
}

func selectionTest(s *Scope, args []jsval.Value) jsval.Value {
	name := s.str(arg(args, 0))
	datum := arg(args, 1)
	intersect := arg(args, 2).IsStr() && arg(args, 2).StrValue() == "intersect"
	entries := s.storeEntries(name)
	var counts map[string]int
	hasUnits := false
	if s.units != nil {
		counts, hasUnits = s.units.UnitCounts(name)
	}
	useUnits := hasUnits && intersect
	var miss map[string]int
	for _, entry := range entries {
		if useUnits {
			if miss == nil {
				miss = map[string]int{}
			}
			unit := s.str(s.getProp(entry, "unit"))
			count := miss[unit]
			if count == -1 {
				continue
			}
			b := s.testPoint(datum, entry)
			if b {
				miss[unit] = -1
			} else {
				count++
				miss[unit] = count
			}
			if b && len(counts) == 1 {
				return jsval.True
			}
			if !b {
				c, ok := counts[unit]
				if !ok {
					typeError("Cannot read properties of undefined (reading 'count')")
				}
				if count == c {
					return jsval.False
				}
			}
		} else {
			b := s.testPoint(datum, entry)
			// intersect ^ b: stop at the first miss when intersecting, the
			// first hit otherwise.
			if intersect != b {
				return jsval.Bool(b)
			}
		}
	}
	// `n && intersect`: with no entries this is the number 0.
	if len(entries) == 0 {
		return jsval.Num(0)
	}
	return jsval.Bool(intersect)
}

func selectionIDTest(s *Scope, args []jsval.Value) jsval.Value {
	name := s.str(arg(args, 0))
	datum := arg(args, 1)
	intersect := arg(args, 2).IsStr() && arg(args, 2).StrValue() == "intersect"
	entries := s.storeEntries(name)
	value := s.getProp(datum, selectionID)
	// d3.bisector(accessor).left: ascending() is NaN for null or incomparable
	// values, which makes `< 0` false.
	asc := func(a, b jsval.Value) float64 {
		switch {
		case a.IsNullish() || b.IsNullish():
			return math.NaN()
		case s.lt(a, b):
			return -1
		case s.gt(a, b):
			return 1
		case s.ge(a, b):
			return 0
		}
		return math.NaN()
	}
	idOf := func(e jsval.Value) jsval.Value { return s.getProp(e, selectionID) }
	bisect := func(right bool) int {
		lo, hi := 0, len(entries)
		if lo < hi {
			if asc(value, value) != 0 {
				return hi
			}
			for lo < hi {
				mid := int(uint(lo+hi) >> 1)
				c := asc(idOf(entries[mid]), value)
				if right && c <= 0 || !right && c < 0 {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
		}
		return lo
	}
	index := bisect(false)
	if index == len(entries) {
		return jsval.False
	}
	if !strictEquals(idOf(entries[index]), value) {
		return jsval.False
	}
	if s.units != nil && intersect {
		if counts, ok := s.units.UnitCounts(name); ok {
			if len(counts) == 1 {
				return jsval.True
			}
			if bisect(true)-index < len(counts) {
				return jsval.False
			}
		}
	}
	return jsval.True
}

func selectionTuples(s *Scope, args []jsval.Value) jsval.Value {
	arr, base := arg(args, 0), arg(args, 1)
	if !arr.IsArr() {
		throw("Error", "First argument to selectionTuples must be an array.")
	}
	if !isObjectLike(base) {
		throw("Error", "Second argument to selectionTuples must be an object.")
	}
	items := arr.Items()
	out := make([]jsval.Value, len(items))
	fields := s.getProp(base, "fields")
	for i, x := range items {
		t := jsval.NewObject(4)
		datum := s.getProp(x, "datum")
		if fields.IsTruthy() {
			if !fields.IsArr() {
				typeError("base.fields.map is not a function")
			}
			fs := fields.Items()
			vals := make([]jsval.Value, len(fs))
			for j, f := range fs {
				vals[j] = s.readPath(datum, s.str(s.getProp(f, "field")))
			}
			t.Set("values", jsval.Arr(vals))
		} else {
			t.Set(selectionID, s.getProp(datum, selectionID))
		}
		switch {
		case base.IsObj():
			for k, v := range base.ObjValue().All() {
				t.Set(k, v)
			}
		case base.IsArr():
			for j, v := range base.Items() {
				t.Set(jsval.Int(j).AsString(), v)
			}
		}
		out[i] = jsval.Obj(jsOrder(t))
	}
	return jsval.Arr(out)
}

// ---- selectionResolve ----

type unitValues struct {
	keys []string
	vals map[string][]jsval.Value
}

func (u *unitValues) get(unit string) []jsval.Value { return u.vals[unit] }

func (u *unitValues) set(unit string, v []jsval.Value) {
	if u.vals == nil {
		u.vals = map[string][]jsval.Value{}
	}
	if _, ok := u.vals[unit]; !ok {
		u.keys = append(u.keys, unit)
	}
	u.vals[unit] = v
}

type resolveOps struct{ s *Scope }

func (r resolveOps) union(typ byte, base, value []jsval.Value) []jsval.Value {
	s := r.s
	switch typ {
	case 'E':
		if len(base) == 0 {
			return append([]jsval.Value(nil), value...)
		}
		out := append([]jsval.Value(nil), base...)
		for _, v := range value {
			if !includes(out, v) {
				out = append(out, v)
			}
		}
		return out
	case 'R':
		lo, hi := jsval.Num(s.num(idx(value, 0))), jsval.Num(s.num(idx(value, 1)))
		if s.gt(lo, hi) {
			lo, hi = idx(value, 1), idx(value, 0)
		}
		if len(base) == 0 {
			return []jsval.Value{lo, hi}
		}
		out := append([]jsval.Value(nil), base...)
		if s.gt(idx(out, 0), lo) {
			out[0] = lo
		}
		if s.lt(idx(out, 1), hi) {
			out[1] = hi
		}
		return out
	}
	typeError("union is not a function")
	return nil
}

func (r resolveOps) intersect(typ byte, base, value []jsval.Value) []jsval.Value {
	s := r.s
	switch typ {
	case 'E':
		if len(base) == 0 {
			return append([]jsval.Value(nil), value...)
		}
		var out []jsval.Value
		for _, v := range base {
			if includes(value, v) {
				out = append(out, v)
			}
		}
		return out
	case 'R':
		lo, hi := jsval.Num(s.num(idx(value, 0))), jsval.Num(s.num(idx(value, 1)))
		if s.gt(lo, hi) {
			lo, hi = idx(value, 1), idx(value, 0)
		}
		if len(base) == 0 {
			return []jsval.Value{lo, hi}
		}
		if s.lt(hi, idx(base, 0)) || s.lt(idx(base, 1), lo) {
			return nil
		}
		out := append([]jsval.Value(nil), base...)
		if s.lt(out[0], lo) {
			out[0] = lo
		}
		if s.gt(out[1], hi) {
			out[1] = hi
		}
		return out
	}
	typeError("intersect is not a function")
	return nil
}

func idx(v []jsval.Value, i int) jsval.Value {
	if i < len(v) {
		return v[i]
	}
	return jsval.Undefined
}

// internKey identifies a value the way d3's InternSet does: primitives by
// value.
func internKey(v jsval.Value) string {
	return v.Kind().String() + ":" + v.AsString()
}

func internUnion(lists ...[]jsval.Value) []jsval.Value {
	seen := map[string]struct{}{}
	var out []jsval.Value
	for _, l := range lists {
		for _, v := range l {
			k := internKey(v)
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				out = append(out, v)
			}
		}
	}
	return out
}

func internIntersection(first []jsval.Value, others ...[]jsval.Value) []jsval.Value {
	sets := make([]map[string]struct{}, len(others))
	for i, o := range others {
		sets[i] = map[string]struct{}{}
		for _, v := range o {
			sets[i][internKey(v)] = struct{}{}
		}
	}
	var out []jsval.Value
	seen := map[string]struct{}{}
next:
	for _, v := range first {
		k := internKey(v)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		for _, st := range sets {
			if _, ok := st[k]; !ok {
				continue next
			}
		}
		out = append(out, v)
	}
	return out
}

// selectionResolve returns an object of resolved field values. For
// extensional (_vgsid_) selections upstream returns a JavaScript Set; here it
// is an array of the distinct ids in first-seen order.
func selectionResolve(s *Scope, args []jsval.Value) jsval.Value {
	name := s.str(arg(args, 0))
	opV, isMulti, vl5 := arg(args, 1), arg(args, 2).IsTruthy(), arg(args, 3).IsTruthy()
	entries := s.storeEntries(name)
	ops := resolveOps{s}

	var fieldOrder []string
	resolved := map[string]*unitValues{}
	types := map[string]byte{}
	var multiKeys []string
	multiRes := map[string][]jsval.Value{}
	addMulti := func(unit string, v jsval.Value) {
		if _, ok := multiRes[unit]; !ok {
			multiKeys = append(multiKeys, unit)
		}
		multiRes[unit] = append(multiRes[unit], v)
	}
	resolvedFor := func(field string) *unitValues {
		r := resolved[field]
		if r == nil {
			r = &unitValues{}
			resolved[field] = r
			fieldOrder = append(fieldOrder, field)
		}
		return r
	}

	for _, entry := range entries {
		unitV := s.getProp(entry, "unit")
		unit := s.str(unitV)
		fields, values := s.getProp(entry, "fields"), s.getProp(entry, "values")
		if fields.IsTruthy() && values.IsTruthy() {
			n := int(s.num(s.getProp(fields, "length")))
			for j := 0; j < n; j++ {
				f := s.getIndex(fields, jsval.Int(j))
				fname := s.str(s.getProp(f, "field"))
				res := resolvedFor(fname)
				typeStr := s.str(s.getProp(f, "type"))
				var typ byte
				if typeStr != "" {
					typ = typeStr[0]
				}
				types[fname] = typ
				res.set(unit, ops.union(typ, res.get(unit), toArray(s.getIndex(values, jsval.Int(j)))))
			}
			if isMulti {
				obj := jsval.NewObject(n)
				for j, cur := range toArray(values) {
					f := s.getIndex(fields, jsval.Int(j))
					obj.Set(s.str(s.getProp(f, "field")), cur)
				}
				addMulti(unit, jsval.Obj(obj))
			}
		} else {
			value := s.getProp(entry, selectionID)
			res := resolvedFor(selectionID)
			res.set(unit, append(res.get(unit), value))
			if isMulti {
				addMulti(unit, jsval.Obj(jsval.ObjectOf(selectionID, value)))
			}
		}
	}

	op := "union"
	if opV.IsTruthy() {
		op = s.str(opV)
	}
	out := jsval.NewObject(len(fieldOrder) + 1)
	if ids := resolved[selectionID]; ids != nil {
		lists := make([][]jsval.Value, len(ids.keys))
		for i, k := range ids.keys {
			lists[i] = ids.vals[k]
		}
		var set []jsval.Value
		switch op {
		case "union":
			set = internUnion(lists...)
		case "intersect":
			if len(lists) == 0 {
				typeError("Cannot read properties of undefined")
			}
			set = internIntersection(lists[0], lists[1:]...)
		default:
			typeError("ops[`_vgsid__%s`] is not a function", op)
		}
		for _, f := range fieldOrder {
			if f == selectionID {
				out.Set(f, jsval.Arr(set))
			} else {
				// Mixed stores keep the per-unit map, as upstream leaves it.
				u := resolved[f]
				o := jsval.NewObject(len(u.keys))
				for _, k := range u.keys {
					o.Set(k, jsval.Arr(u.vals[k]))
				}
				out.Set(f, jsval.Obj(o))
			}
		}
	} else {
		for _, f := range fieldOrder {
			u := resolved[f]
			var acc []jsval.Value
			have := false
			for _, k := range u.keys {
				cur := u.vals[k]
				if !have {
					acc, have = cur, true
					continue
				}
				switch op {
				case "union":
					acc = ops.union(types[f], acc, cur)
				case "intersect":
					acc = ops.intersect(types[f], acc, cur)
				default:
					typeError("ops[`%c_%s`] is not a function", types[f], op)
				}
			}
			out.Set(f, jsval.Arr(acc))
		}
	}

	if isMulti && len(multiKeys) > 0 {
		key := "vlMulti"
		if vl5 {
			key = "vlPoint"
		}
		if op == "union" {
			var all []jsval.Value
			for _, k := range multiKeys {
				all = append(all, multiRes[k]...)
			}
			out.Set(key, jsval.Obj(jsval.ObjectOf("or", jsval.Arr(all))))
		} else {
			parts := make([]jsval.Value, len(multiKeys))
			for i, k := range multiKeys {
				parts[i] = jsval.Obj(jsval.ObjectOf("or", jsval.Arr(multiRes[k])))
			}
			out.Set(key, jsval.Obj(jsval.ObjectOf("and", jsval.Arr(parts))))
		}
	}
	return jsval.Obj(out)
}

func init() {
	defv("vlSelectionTest", 1, selectionVisitor, selectionTest)
	defv("vlSelectionIdTest", 1, selectionVisitor, selectionIDTest)
	defv("vlSelectionResolve", 1, selectionVisitor, selectionResolve)
	fn("vlSelectionTuples", selectionTuples)
}
