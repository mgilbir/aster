package expr

import (
	"github.com/mgilbir/aster/internal/jssort"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// MaxSequenceLength bounds sequence(): far above any chart (a tick or a
// gradient stop list is hundreds), and small enough that a hostile
// specification cannot allocate gigabytes.
const MaxSequenceLength = 1 << 20

// requireArray is vega-functions' `array(seq)` followed by a method call: a
// non-array makes the call a TypeError.
func requireArray(v jsval.Value, method string) []jsval.Value {
	if !v.IsArr() {
		typeError("Cannot read properties of %s (reading '%s')", nullName(v), method)
	}
	return v.Items()
}

// nullName is how V8 names a receiver in "Cannot read properties of X":
// null and undefined print as such; vega-functions' array() helper turns other
// non-arrays into null first.
func nullName(v jsval.Value) string {
	if v.IsUndefined() {
		return "undefined"
	}
	return "null"
}

// requireSequence is vega-functions' `sequence(seq)`: an array or a string.
func requireSequence(v jsval.Value, method string) {
	if !v.IsArr() && !v.IsStr() {
		typeError("Cannot read properties of null (reading '%s')", method)
	}
}

// vegaAscending is vega-util's `ascending`: nulls first, NaN after numbers'
// natural order ... exactly as its ternary chain reads. Dates compare by time.
func (s *Scope) vegaAscending(u, v jsval.Value) int {
	if (s.lt(u, v) || u.IsNullish()) && !v.IsNullish() {
		return -1
	}
	if (s.gt(u, v) || v.IsNullish()) && !u.IsNullish() {
		return 1
	}
	if u.IsTimestamp() {
		u = jsval.Num(u.NumValue())
	}
	if v.IsTimestamp() {
		v = jsval.Num(v.NumValue())
	}
	// (u = ...) !== u is a NaN test on u; v === v tests that v is not NaN.
	uNaN := u.IsNum() && math.IsNaN(u.NumValue())
	vNaN := v.IsNum() && math.IsNaN(v.NumValue())
	if uNaN && !vNaN {
		return -1
	}
	if vNaN && !uNaN {
		return 1
	}
	return 0
}

func (s *Scope) lastOf(arr jsval.Value) jsval.Value {
	n := s.getProp(arr, "length")
	return s.getIndex(arr, jsval.Num(s.num(n)-1))
}

func init() {
	fn("join", func(s *Scope, args []jsval.Value) jsval.Value {
		items := requireArray(arg(args, 0), "join")
		sep := ","
		if v := arg(args, 1); !v.IsUndefined() {
			sep = s.str(v)
		}
		if len(items) == 0 {
			return jsval.Str("")
		}
		var b strings.Builder
		for i, it := range items {
			if i > 0 {
				b.WriteString(sep)
			}
			if !it.IsNullish() {
				b.WriteString(s.str(it))
			}
			// Checked while building, so the builder never grows past the
			// limit by more than one element.
			if b.Len() > MaxStringLength {
				s.checkLen(b.Len())
			}
			s.tick()
		}
		s.checkLen(b.Len())
		return jsval.Str(b.String())
	})
	fn("indexof", func(s *Scope, args []jsval.Value) jsval.Value {
		seq := arg(args, 0)
		requireSequence(seq, "indexOf")
		x := arg(args, 1)
		if seq.IsStr() {
			from := 0
			if p := arg(args, 2); !p.IsUndefined() {
				from = int(math.Max(0, math.Min(toInteger(s.num(p)), float64(utf16Len(seq.StrValue())))))
			}
			return jsval.Int(indexOfUnits(seq.StrValue(), s.str(x), from))
		}
		items := seq.Items()
		start := 0
		if p := arg(args, 2); !p.IsUndefined() {
			f := toInteger(s.num(p))
			if f < 0 {
				f = math.Max(0, f+float64(len(items)))
			}
			if f >= float64(len(items)) {
				return jsval.Int(-1)
			}
			start = int(f)
		}
		for i := start; i < len(items); i++ {
			if strictEquals(items[i], x) {
				return jsval.Int(i)
			}
		}
		return jsval.Int(-1)
	})
	fn("lastindexof", func(s *Scope, args []jsval.Value) jsval.Value {
		seq := arg(args, 0)
		requireSequence(seq, "lastIndexOf")
		x := arg(args, 1)
		if seq.IsStr() {
			str := seq.StrValue()
			n := utf16Len(str)
			from := n
			if p := arg(args, 2); !p.IsUndefined() {
				f := s.num(p)
				if !math.IsNaN(f) {
					from = int(math.Max(0, math.Min(toInteger(f), float64(n))))
				}
			}
			return jsval.Int(lastIndexOfUnits(str, s.str(x), from))
		}
		items := seq.Items()
		start := len(items) - 1
		if len(args) > 2 {
			f := toInteger(s.num(args[2]))
			if f < 0 {
				f += float64(len(items))
			}
			if f < 0 {
				return jsval.Int(-1)
			}
			start = int(math.Min(f, float64(len(items)-1)))
		}
		for i := start; i >= 0; i-- {
			if strictEquals(items[i], x) {
				return jsval.Int(i)
			}
		}
		return jsval.Int(-1)
	})
	fn("slice", func(s *Scope, args []jsval.Value) jsval.Value {
		seq := arg(args, 0)
		requireSequence(seq, "slice")
		hasEnd := !arg(args, 2).IsUndefined()
		start := s.num(arg(args, 1))
		end := 0.0
		if hasEnd {
			end = s.num(args[2])
		}
		if seq.IsStr() {
			return jsval.Str(sliceUnits(seq.StrValue(), start, end, hasEnd))
		}
		items := seq.Items()
		from := sliceIndex(start, len(items))
		to := len(items)
		if hasEnd {
			to = sliceIndex(end, len(items))
		}
		if to <= from {
			return jsval.Arr(nil)
		}
		return jsval.Arr(slices.Clone(items[from:to]))
	})
	fn("reverse", func(s *Scope, args []jsval.Value) jsval.Value {
		items := slices.Clone(requireArray(arg(args, 0), "slice"))
		slices.Reverse(items)
		return jsval.Arr(items)
	})
	fn("sort", func(s *Scope, args []jsval.Value) jsval.Value {
		items := slices.Clone(requireArray(arg(args, 0), "slice"))
		// Array.prototype.sort moves undefined elements to the end without
		// consulting the comparator.
		defined := items[:0:0]
		undef := 0
		for _, it := range items {
			if it.IsUndefined() {
				undef++
			} else {
				defined = append(defined, it)
			}
		}
		s.tick()
		jssort.Sort(defined, func(a, b jsval.Value) int {
			s.tick()
			return s.vegaAscending(a, b)
		})
		for i := 0; i < undef; i++ {
			defined = append(defined, jsval.Undefined)
		}
		return jsval.Arr(defined)
	})
	fn("peek", func(s *Scope, args []jsval.Value) jsval.Value { return s.lastOf(arg(args, 0)) })
	fn("span", func(s *Scope, args []jsval.Value) jsval.Value {
		a := arg(args, 0)
		if !a.IsTruthy() {
			return jsval.Num(0)
		}
		d := s.num(s.lastOf(a)) - s.num(s.getIndex(a, jsval.Num(0)))
		if d == 0 || math.IsNaN(d) {
			return jsval.Num(0)
		}
		return jsval.Num(d)
	})
	fn("extent", func(s *Scope, args []jsval.Value) jsval.Value {
		a := arg(args, 0)
		var elem func(i int) jsval.Value
		n := 0
		switch {
		case a.IsArr():
			items := a.Items()
			n = len(items)
			elem = func(i int) jsval.Value { return items[i] }
		case a.IsStr():
			n = utf16Len(a.StrValue())
			elem = func(i int) jsval.Value { return charAt(a.StrValue(), i) }
		case a.IsObj():
			l := a.Get("length")
			if f := s.num(l); f > 0 {
				n = int(math.Min(f, 1<<24))
			}
			elem = func(i int) jsval.Value { return s.getProp(a, jsval.Int(i).AsString()) }
		}
		if n == 0 {
			return jsval.ArrOf(jsval.Undefined, jsval.Undefined)
		}
		i := 0
		v := elem(0)
		for i < n && (v.IsNullish() || v.IsNum() && math.IsNaN(v.NumValue())) {
			i++
			if i < n {
				v = elem(i)
			} else {
				v = jsval.Undefined
			}
		}
		mn, mx := v, v
		for ; i < n; i++ {
			s.tick()
			v = elem(i)
			if !v.IsNullish() {
				if s.lt(v, mn) {
					mn = v
				}
				if s.gt(v, mx) {
					mx = v
				}
			}
		}
		return jsval.ArrOf(mn, mx)
	})
	fn("lerp", func(s *Scope, args []jsval.Value) jsval.Value {
		a := arg(args, 0)
		lo := s.getIndex(a, jsval.Num(0))
		hi := s.lastOf(a)
		f := s.num(arg(args, 1))
		if hi.IsUndefined() {
			return lo
		}
		switch {
		case f == 0 || math.IsNaN(f):
			return lo
		case f == 1:
			return hi
		}
		// lo + f * (hi - lo): the string/date coercions of `+` apply to lo.
		return s.add(lo, jsval.Num(float64(f*(s.num(hi)-s.num(lo)))))
	})
	fn("inrange", func(s *Scope, args []jsval.Value) jsval.Value {
		left, right := arg(args, 2), arg(args, 3)
		return jsval.Bool(s.inRange(arg(args, 0), arg(args, 1),
			left.IsUndefined() || left.IsTruthy(), right.IsUndefined() || right.IsTruthy()))
	})
	fn("clampRange", func(s *Scope, args []jsval.Value) jsval.Value {
		rng := arg(args, 0)
		lo, hi := s.num(s.getIndex(rng, jsval.Num(0))), s.num(s.getIndex(rng, jsval.Num(1)))
		mn, mx := s.num(arg(args, 1)), s.num(arg(args, 2))
		if hi < lo {
			lo, hi = hi, lo
		}
		span := hi - lo
		if span >= mx-mn {
			return jsval.ArrOf(jsval.Num(mn), jsval.Num(mx))
		}
		lo = jsMin2(jsMax2(lo, mn), mx-span)
		return jsval.ArrOf(jsval.Num(lo), jsval.Num(lo+span))
	})
	// flush(range, value, threshold, left, right, center): pick left, right or
	// center depending on how close value lies to an end of the range.
	fn("flush", func(s *Scope, args []jsval.Value) jsval.Value {
		rng, value, threshold := arg(args, 0), s.num(arg(args, 1)), arg(args, 2)
		left, right, center := arg(args, 3), arg(args, 4), arg(args, 5)
		if !threshold.IsTruthy() && !(threshold.IsNum() && threshold.NumValue() == 0) {
			return center
		}
		t := s.num(threshold)
		a := s.num(s.getIndex(rng, jsval.Num(0)))
		bv := s.lastOf(rng)
		if bv.IsUndefined() {
			return center
		}
		b := s.num(bv)
		if b < a {
			a, b = b, a
		}
		l := math.Abs(value - a)
		r := math.Abs(b - value)
		switch {
		case l < r && l <= t:
			return left
		case r <= t:
			return right
		}
		return center
	})
	// sequence is d3.range: arguments are (stop), (start, stop) or (start,
	// stop, step).
	fn("sequence", func(s *Scope, args []jsval.Value) jsval.Value {
		start, stop, step := math.NaN(), math.NaN(), 1.0
		switch {
		case len(args) < 2:
			stop, start = s.num(arg(args, 0)), 0
		case len(args) < 3:
			start, stop = s.num(args[0]), s.num(args[1])
		default:
			start, stop, step = s.num(args[0]), s.num(args[1]), s.num(args[2])
		}
		// d3 sizes the array with `Math.max(0, Math.ceil(...)) | 0`: ToInt32
		// makes an infinite count empty and a count of 2^31 or more negative,
		// which `new Array` rejects.
		n32 := toInt32(jsMax2(0, math.Ceil((stop-start)/step)))
		if n32 < 0 {
			throw("RangeError", "Invalid array length")
		}
		if n32 > MaxSequenceLength {
			throw("RangeError", "sequence length exceeds %d", MaxSequenceLength)
		}
		n := int(n32)
		s.chargeItems(n)
		out := make([]jsval.Value, n)
		for i := range out {
			s.tick()
			out[i] = jsval.Num(start + float64(float64(i)*step))
		}
		return jsval.Arr(out)
	})
	fn("pluck", func(s *Scope, args []jsval.Value) jsval.Value {
		data := arg(args, 0)
		// field(name) splits the name as an access path: undefined and null
		// have no length (TypeError), and other non-strings yield an empty
		// path, which reads the item itself.
		var path []string
		switch name := arg(args, 1); {
		case name.IsNullish():
			typeError("Cannot read properties of %s (reading 'length')", name.AsString())
		case name.IsStr() || name.IsArr():
			path = jsval.ParseFieldPath(s.str(name))
		}
		get := func(o jsval.Value) jsval.Value {
			for _, seg := range path {
				o = s.getProp(o, seg)
			}
			return o
		}
		if data.IsArr() {
			items := data.Items()
			out := make([]jsval.Value, len(items))
			for i, it := range items {
				out[i] = get(it)
			}
			return jsval.Arr(out)
		}
		return get(data)
	})
	// merge is Object.assign({}, ...objects) with for-in semantics.
	fn("merge", func(s *Scope, args []jsval.Value) jsval.Value {
		out := jsval.NewObject(0)
		for _, a := range args {
			switch a.Kind() {
			case jsval.KindObj:
				for k, v := range a.ObjValue().All() {
					out.Set(k, v)
				}
			case jsval.KindArr:
				for i, v := range a.Items() {
					out.Set(jsval.Int(i).AsString(), v)
				}
			case jsval.KindStr:
				u := toUTF16(a.StrValue())
				for i := range u {
					out.Set(jsval.Int(i).AsString(), jsval.Str(fromUTF16(u[i:i+1])))
				}
			}
		}
		return jsval.Obj(jsOrder(out))
	})
}

// jsOrder reorders an object's keys the way JavaScript enumerates them:
// integer-like keys ascending, then the rest in insertion order.
func jsOrder(o *jsval.Object) *jsval.Object {
	type kv struct {
		k string
		i int
	}
	var ints []kv
	for _, k := range o.Keys() {
		if i, ok := arrayIndex(k); ok {
			ints = append(ints, kv{k, i})
		}
	}
	if len(ints) == 0 {
		return o
	}
	sort.SliceStable(ints, func(a, b int) bool { return ints[a].i < ints[b].i })
	out := jsval.NewObject(o.Len())
	for _, e := range ints {
		out.Set(e.k, o.Lookup(e.k))
	}
	for k, v := range o.All() {
		if _, ok := arrayIndex(k); !ok {
			out.Set(k, v)
		}
	}
	return out
}

// inRange is vega-util's inrange: whether value lies within the span of the
// range's first and last values, with inclusive or exclusive ends.
func (s *Scope) inRange(value, rng jsval.Value, left, right bool) bool {
	r0, r1 := s.getIndex(rng, jsval.Num(0)), s.lastOf(rng)
	if s.gt(r0, r1) {
		r0, r1 = r1, r0
	}
	var okL, okR bool
	if left {
		okL = s.le(r0, value)
	} else {
		okL = s.lt(r0, value)
	}
	if !okL {
		return false
	}
	if right {
		okR = s.le(value, r1)
	} else {
		okR = s.lt(value, r1)
	}
	return okR
}
