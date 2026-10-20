package expr

import (
	"math"
	"slices"
	"sort"
	"strconv"

	"github.com/mgilbir/aster/internal/jssort"
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
		b := newStrBuilder(s)
		for i, it := range items {
			if i > 0 {
				b.add(sep)
			}
			if !it.IsNullish() {
				b.add(s.str(it))
			}
			s.work(1)
		}
		return jsval.Str(b.String())
	})
	fn("indexof", func(s *Scope, args []jsval.Value) jsval.Value {
		seq := arg(args, 0)
		requireSequence(seq, "indexOf")
		x := arg(args, 1)
		if seq.IsStr() {
			str := seq.StrValue()
			from := 0
			if p := arg(args, 2); !p.IsUndefined() {
				from = int(math.Max(0, math.Min(toInteger(s.num(p)), float64(utf16Len(str)))))
			}
			return jsval.Int(s.indexOf(str, s.str(x), from))
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
		s.work(len(items) - start)
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
			return jsval.Int(s.lastIndexOf(str, s.str(x), from))
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
		s.work(start + 1)
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
			return jsval.Str(s.sliceUnits(seq.StrValue(), start, end, hasEnd))
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
		return jsval.Arr(s.cloneItems(items[from:to]))
	})
	fn("reverse", func(s *Scope, args []jsval.Value) jsval.Value {
		items := s.cloneItems(requireArray(arg(args, 0), "slice"))
		slices.Reverse(items)
		return jsval.Arr(items)
	})
	fn("sort", func(s *Scope, args []jsval.Value) jsval.Value {
		items := s.cloneItems(requireArray(arg(args, 0), "slice"))
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
		jssort.Sort(defined, func(a, b jsval.Value) int {
			s.work(1)
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
	fn("extent", func(s *Scope, args []jsval.Value) jsval.Value { return s.extent(arg(args, 0)) })
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
	// interpolateLinear(values, frac): piecewise-linear interpolation across an array of control points
	// (vega-functions interpolate.js).
	fn("interpolateLinear", func(s *Scope, args []jsval.Value) jsval.Value {
		values := arg(args, 0)
		if !values.IsArr() || values.Len() == 0 {
			return jsval.Undefined
		}
		items := values.Items()
		n := len(items)
		lo := items[0]
		f := s.num(arg(args, 1))
		if n == 1 || !(f > 0) {
			return lo
		}
		if f >= 1 {
			return items[n-1]
		}
		pos := float64(f * float64(n-1))
		i := int(math.Floor(pos))
		t := pos - float64(i)
		if t == 0 {
			return items[i]
		}
		next := jsval.Undefined
		if i+1 < n {
			next = items[i+1]
		}
		return s.add(items[i], jsval.Num(float64(t*(s.num(next)-s.num(items[i])))))
	})
	fn("inrange", func(s *Scope, args []jsval.Value) jsval.Value {
		left, right := arg(args, 2), arg(args, 3)
		return jsval.Bool(s.inRange(arg(args, 0), arg(args, 1),
			left.IsUndefined() || left.IsTruthy(), right.IsUndefined() || right.IsTruthy()))
	})
	// clampRange and flush compare the range's ends as given, strings as
	// strings, and convert them only for the arithmetic (C101).
	fn("clampRange", func(s *Scope, args []jsval.Value) jsval.Value {
		rng := arg(args, 0)
		lo, hi := s.getIndex(rng, jsval.Num(0)), s.getIndex(rng, jsval.Num(1))
		mn, mx := arg(args, 1), arg(args, 2)
		if s.lt(hi, lo) {
			lo, hi = hi, lo
		}
		span := s.num(hi) - s.num(lo)
		if span >= s.num(mx)-s.num(mn) {
			return jsval.ArrOf(mn, mx) // [min, max], as given
		}
		l := jsMin2(jsMax2(s.num(lo), s.num(mn)), s.num(mx)-span)
		return jsval.ArrOf(jsval.Num(l), jsval.Num(l+span))
	})
	// flush(range, value, threshold, left, right, center): pick left, right or
	// center depending on how close value lies to an end of the range.
	fn("flush", func(s *Scope, args []jsval.Value) jsval.Value {
		rng, value, threshold := arg(args, 0), arg(args, 1), arg(args, 2)
		left, right, center := arg(args, 3), arg(args, 4), arg(args, 5)
		if !threshold.IsTruthy() && !(threshold.IsNum() && threshold.NumValue() == 0) {
			return center
		}
		t := s.num(threshold)
		a := s.getIndex(rng, jsval.Num(0))
		b := s.lastOf(rng)
		if b.IsUndefined() {
			return center
		}
		if s.lt(b, a) {
			a, b = b, a
		}
		v := s.num(value)
		l := math.Abs(v - s.num(a))
		r := math.Abs(s.num(b) - v)
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
		var start, stop float64
		step := 1.0
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
			exceeded("sequence length exceeds %d", MaxSequenceLength)
		}
		out := s.makeItems(int(n32))
		for i := range out {
			out[i] = jsval.Num(start + float64(float64(i)*step))
		}
		return jsval.Arr(out)
	})
	fn("pluck", func(s *Scope, args []jsval.Value) jsval.Value {
		data := arg(args, 0)
		// field(name) splits the name as an access path, as vega-util's
		// splitAccessPath does: it reads name.length (a TypeError for
		// undefined and null), then that many code units of String(name),
		// so a value without a length (a number) is the empty path, which
		// reads the item itself, and ['a', 'b'] the path "a,". A path it
		// cannot split is the Error it throws.
		var path []string
		name := arg(args, 1)
		if name.IsNullish() {
			typeError("Cannot read properties of %s (reading 'length')", name.AsString())
		}
		if n := s.num(s.getProp(name, "length")); n > 0 {
			text := s.str(name)
			s.workBytes(len(text))
			var err error
			if units := utf16Len(text); float64(units) > n {
				path, err = splitAccessPathN(toUTF16(text), int(math.Ceil(n)))
			} else {
				path, err = jsval.SplitFieldPath(text)
			}
			if err != nil {
				throw("Error", "%v", err)
			}
		}
		get := func(o jsval.Value) jsval.Value {
			for _, seg := range path {
				o = s.getProp(o, seg)
			}
			return o
		}
		if data.IsArr() {
			items := data.Items()
			out := s.makeItems(len(items))
			s.work(len(items) * len(path))
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
				s.chargeMembers(a.Len())
				for k, v := range a.ObjValue().All() {
					out.Set(k, v)
				}
				// for-in visits the enumerable members a d3 colour inherits
				// (its methods) after its own.
				if t := colourOf(a); t != nil {
					names := colourForIn[t.class]
					s.chargeMembers(len(names))
					for _, k := range names {
						if !a.ObjValue().Has(k) {
							out.Set(k, t.proto.Get(k))
						}
					}
				}
			case jsval.KindArr:
				s.chargeMembers(a.Len())
				for i, v := range a.Items() {
					out.Set(strconv.Itoa(i), v)
				}
			case jsval.KindStr:
				str := a.StrValue()
				s.chargeMembers(utf16Len(str))
				i := 0
				for off := 0; off < len(str); {
					_, w, u := charAt(str, off)
					out.Set(strconv.Itoa(i), jsval.Str(unitString(str, off, false)))
					if u == 2 {
						out.Set(strconv.Itoa(i+1), jsval.Str(unitString(str, off, true)))
					}
					i += u
					off += w
				}
			}
		}
		res := s.jsOrder(out)
		colourMethods(res)
		return jsval.Obj(res)
	})
}

// jsOrder reorders an object's keys the way JavaScript enumerates them:
// integer-like keys ascending, then the rest in insertion order.
func (s *Scope) jsOrder(o *jsval.Object) *jsval.Object {
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
	s.chargeMembers(o.Len())
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

// extent is vega-util's extent(array) without an accessor:
//
//	if (array && (n = array.length)) {
//	  for (v = array[i]; i < n && (v == null || v !== v); v = array[++i]);
//	  min = max = v;
//	  for (; i < n; ++i) { v = array[i]; if (v != null) { if (v < min) min = v; if (v > max) max = v; } }
//	}
//	return [min, max];
//
// For an array-like object it runs over the integer keys the object holds
// rather than over every index below its length: the others are undefined,
// which neither loop looks at, so the result is the same for any length, where
// upstream's loop would take minutes for {length: 3e9} (ADR 0011). A length
// past 2^53, where i++ stops counting and upstream never returns, is refused.
// When no element is valid the first loop leaves v = array[n], which an object
// may hold.
func (s *Scope) extent(a jsval.Value) jsval.Value {
	undef := func() jsval.Value { return jsval.ArrOf(jsval.Undefined, jsval.Undefined) }
	valid := func(v jsval.Value) bool { return !v.IsNullish() && !(v.IsNum() && math.IsNaN(v.NumValue())) }
	var mn, mx jsval.Value
	found := false
	visit := func(v jsval.Value) {
		s.work(1)
		switch {
		case !found:
			if valid(v) {
				mn, mx, found = v, v, true
			}
		case !v.IsNullish():
			if s.lt(v, mn) {
				mn = v
			}
			if s.gt(v, mx) {
				mx = v
			}
		}
	}
	switch a.Kind() {
	case jsval.KindArr:
		items := a.Items()
		if len(items) == 0 {
			return undef()
		}
		for _, v := range items {
			visit(v)
		}
		if !found {
			return undef() // array[n]
		}
	case jsval.KindStr:
		// Every element is a one-unit string, and two of those compare as
		// their code units: the extent is the smallest and the largest unit.
		str := a.StrValue()
		if str == "" {
			return undef()
		}
		s.workBytes(len(str))
		lo, hi := uint16(0xFFFF), uint16(0)
		for off := 0; off < len(str); {
			r, w, u := charAt(str, off)
			units := [2]uint16{uint16(r)}
			if u == 2 {
				units[0], units[1] = pairHalves(str, off)
			}
			for _, c := range units[:u] {
				lo, hi = min(lo, c), max(hi, c)
			}
			off += w
		}
		return jsval.ArrOf(jsval.Str(string(appendUnit(nil, lo))), jsval.Str(string(appendUnit(nil, hi))))
	case jsval.KindObj:
		o := a.ObjValue()
		l := o.Prop("length")
		if !l.IsTruthy() {
			return undef()
		}
		// The indices i < n: none for NaN or n <= 0, else ceil(n).
		x := s.num(l)
		count := 0.0
		if x > 0 {
			count = math.Ceil(x)
		}
		if count > 1<<53 {
			exceeded("extent of an array-like object of length %v", x)
		}
		type elem struct {
			i uint64
			v jsval.Value
		}
		var elems []elem
		s.chargeItems(o.Len())
		for k, v := range o.All() {
			if i, ok := indexKey(k); ok && float64(i) < count {
				elems = append(elems, elem{i, v})
			}
		}
		s.work(len(elems))
		slices.SortFunc(elems, func(p, q elem) int { return cmpUint(p.i, q.i) })
		for _, e := range elems {
			visit(e.v)
		}
		if !found {
			v := o.Prop(strconv.FormatFloat(count, 'f', -1, 64))
			return jsval.ArrOf(v, v)
		}
	default:
		return undef()
	}
	return jsval.ArrOf(mn, mx)
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// indexKey parses a property name that is the string of an integer below
// 2^53 ("0", "17"; not "01", "-1" or "1.5").
func indexKey(k string) (uint64, bool) {
	if k == "" || len(k) > 16 || len(k) > 1 && k[0] == '0' {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(k); i++ {
		if k[i] < '0' || k[i] > '9' {
			return 0, false
		}
		n = n*10 + uint64(k[i]-'0')
	}
	return n, n <= 1<<53
}

// splitAccessPathN is vega-util's splitAccessPath for a value whose length n
// is shorter than its String p (an array of several strings, an object with
// a length): the loop reads the first n code units, and the last segment runs
// one past them (its j++). For a string, n is its length and
// jsval.SplitFieldPath is the same split.
func splitAccessPathN(p []uint16, n int) ([]string, error) {
	var path []string
	var q uint16 // the open quote, 0 for none
	b, i, j := 0, 0, 0
	s := ""
	sub := func(from, to int) string {
		from, to = min(from, len(p)), min(to, len(p))
		if from >= to {
			return ""
		}
		return fromUTF16(p[from:to])
	}
	push := func() {
		path = append(path, s+sub(i, j))
		s = ""
		i = j + 1
	}
	for ; j < n; j++ {
		c := uint16(0xFFFF) // past the end: undefined, no character
		if j < len(p) {
			c = p[j]
		}
		switch {
		case c == '\\':
			s += sub(i, j)
			j++
			i = j
		case q != 0 && c == q:
			push()
			q = 0
			b = -1
		case q != 0:
		case i == b && (c == '"' || c == '\''):
			i = j + 1
			q = c
		case c == '.' && b == 0:
			if j > i {
				push()
			} else {
				i = j + 1
			}
		case c == '[':
			if j > i {
				push()
			}
			b, i = j+1, j+1
		case c == ']':
			if b == 0 {
				return path, &jsval.PathError{Msg: "Access path missing open bracket: " + fromUTF16(p)}
			}
			if b > 0 {
				push()
			}
			b = 0
			i = j + 1
		}
	}
	if b != 0 {
		return path, &jsval.PathError{Msg: "Access path missing closing bracket: " + fromUTF16(p)}
	}
	if q != 0 {
		return path, &jsval.PathError{Msg: "Access path missing closing quote: " + fromUTF16(p)}
	}
	if j > i {
		j++
		push()
	}
	return path, nil
}
