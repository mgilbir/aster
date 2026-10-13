package vegalite

import (
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// refObj is a JavaScript object kept in order eagerly, the way upstream's
// objects are: a new array-index key goes among the leading index keys,
// ascending; any other new key goes last; a delete removes the key.
type refObj struct {
	keys []string
	vals map[string]int
}

func (r *refObj) set(k string, v int) {
	if r.vals == nil {
		r.vals = map[string]int{}
	}
	if _, ok := r.vals[k]; !ok {
		pos := len(r.keys)
		if n, isIdx := arrayIndex(k); isIdx {
			for i, e := range r.keys {
				if m, ok := arrayIndex(e); !ok || m > n {
					pos = i
					break
				}
			}
		}
		r.keys = slices.Insert(r.keys, pos, k)
	}
	r.vals[k] = v
}

func (r *refObj) del(k string) {
	if _, ok := r.vals[k]; !ok {
		return
	}
	delete(r.vals, k)
	r.keys = slices.DeleteFunc(r.keys, func(e string) bool { return e == k })
}

func randKey(rng *rand.Rand) string {
	switch rng.IntN(4) {
	case 0:
		return "k" + strconv.Itoa(rng.IntN(30))
	case 1:
		return "0" + strconv.Itoa(rng.IntN(5)) // not an index ("01")
	default:
		return strconv.Itoa(rng.IntN(60))
	}
}

// TestLazyJSOrderMatchesEager drives omap, the split's jsObj, jsSet, spread
// and jsOrderKeys with random sequences of sets and deletes and compares the
// keys they list with an object ordered on every operation (C4: the order is
// now kept lazily or in bulk, and must be the same).
func TestLazyJSOrderMatchesEager(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for iter := 0; iter < 3000; iter++ {
		var ref refObj
		om := newOmap[int]()
		jo := newJSObj(nil)
		obj := jsval.NewObject(0) // jsSet without deletes
		var objRef refObj
		bulk := jsval.NewObject(0) // Set, then jsOrderKeys
		ops := 1 + rng.IntN(80)
		for i := 0; i < ops; i++ {
			k := randKey(rng)
			if rng.IntN(4) == 0 {
				ref.del(k)
				om.del(k)
				jo.Delete(k)
			} else {
				ref.set(k, i)
				om.set(k, i)
				jo.Set(k, jsval.Int(i))
				objRef.set(k, i)
				jsSet(nil, obj, k, jsval.Int(i))
				bulk.Set(k, jsval.Int(i))
			}
			if rng.IntN(8) == 0 {
				if got := om.keysView(); !slices.Equal(got, ref.keys) {
					t.Fatalf("omap keys %v, want %v", got, ref.keys)
				}
			}
		}
		if got := om.keyList(); !slices.Equal(got, ref.keys) {
			t.Fatalf("omap keys %v, want %v", got, ref.keys)
		}
		if om.len() != len(ref.keys) {
			t.Fatalf("omap len %d, want %d", om.len(), len(ref.keys))
		}
		if got := jo.object().Keys(); !slices.Equal(got, ref.keys) {
			t.Fatalf("jsObj keys %v, want %v", got, ref.keys)
		}
		for _, k := range ref.keys {
			if jo.Lookup(k).NumValue() != float64(ref.vals[k]) {
				t.Fatalf("jsObj %s = %v, want %d", k, jo.Lookup(k), ref.vals[k])
			}
		}
		if got := obj.Keys(); !slices.Equal(got, objRef.keys) {
			t.Fatalf("jsSet keys %v, want %v", got, objRef.keys)
		}
		jsOrderKeys(nil, bulk)
		if got := bulk.Keys(); !slices.Equal(got, objRef.keys) {
			t.Fatalf("jsOrderKeys keys %v, want %v", got, objRef.keys)
		}
		// spread of obj into a copy of a second ordered object.
		var dstRef refObj
		dst := jsval.NewObject(0)
		for i := rng.IntN(20); i > 0; i-- {
			k := randKey(rng)
			dstRef.set(k, -1)
			jsSet(nil, dst, k, jsval.Int(-1))
		}
		for _, k := range objRef.keys {
			dstRef.set(k, objRef.vals[k])
		}
		spread(dst, jsval.Obj(obj))
		if got := dst.Keys(); !slices.Equal(got, dstRef.keys) {
			t.Fatalf("spread keys %v, want %v", got, dstRef.keys)
		}
	}
}

// TestSplitCombineMatchesSpread checks that combine lists a split's keys as
// `{...explicit, ...implicit}` would, after random sets on both sides.
func TestSplitCombineMatchesSpread(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for iter := 0; iter < 2000; iter++ {
		s := newSplit()
		var exp, imp refObj
		for i := rng.IntN(60); i > 0; i-- {
			k := randKey(rng)
			if rng.IntN(2) == 0 {
				imp.del(k)
				exp.set(k, i)
				s.set(k, jsval.Int(i), true)
			} else {
				exp.del(k)
				imp.set(k, i)
				s.set(k, jsval.Int(i), false)
			}
		}
		var want refObj
		for _, k := range exp.keys {
			want.set(k, exp.vals[k])
		}
		for _, k := range imp.keys {
			want.set(k, imp.vals[k])
		}
		if got := s.combine().Keys(); !slices.Equal(got, want.keys) {
			t.Fatalf("combine keys %v, want %v", got, want.keys)
		}
	}
}

// TestSignalNamerMatchesUpstreamLoop compares signalNamer with upstream's
// loop, which restarts its counter at 1 for every field (C16), on random
// field names that collide in many ways ("a", "a_1", "é", "_", digits).
func TestSignalNamerMatchesUpstreamLoop(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	parts := []string{"a", "_", "1", "é", "a_1", "a_2", "_1", "\U0001F600", "\xe4\xb8", "-", "2"}
	for iter := 0; iter < 2000; iter++ {
		name := parts[rng.IntN(len(parts))]
		taken := newSset()
		upstream := func(suffix string) string {
			sg := varName(name + "_" + suffix)
			for counter := 1; taken.has(sg); counter++ {
				sg = varName(name + "_" + suffix + "_" + jsval.JSNumberString(float64(counter)))
			}
			taken.add(sg)
			return sg
		}
		n := newSignalNamer()
		for i := rng.IntN(40); i > 0; i-- {
			suffix := ""
			for j := rng.IntN(3); j >= 0; j-- {
				suffix += parts[rng.IntN(len(parts))]
			}
			if got, want := n.next(nil, name, suffix), upstream(suffix); got != want {
				t.Fatalf("name %q suffix %q: %q, want %q", name, suffix, got, want)
			}
		}
	}
}

// TestTitleCase: s.charAt(0).toUpperCase() + s.substr(1), on UTF-16 units
// with JavaScript's case mapping (C104: the first byte was upper-cased).
func TestTitleCase(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "median": "Median", "ßa": "SSa", "éte": "Éte", "😀x": "😀x", "ǆ": "Ǆ", "1a": "1a",
	} {
		if got := titleCase(in); got != want {
			t.Errorf("titleCase(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSplitAccessPathNMatchesStringSplit: on a string, the port that takes
// the loop bound of a non-string value splits as the string splitter does,
// errors included.
func TestSplitAccessPathNMatchesStringSplit(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	alphabet := []string{"a", "b", ".", "[", "]", "'", `"`, `\`, "é", "😀"}
	split := func(f func() []string) (out []string, msg string) {
		defer func() {
			if r := recover(); r != nil {
				msg = r.(compileError).msg
			}
		}()
		return f(), ""
	}
	for iter := 0; iter < 20000; iter++ {
		s := ""
		for k := rng.IntN(8); k > 0; k-- {
			s += alphabet[rng.IntN(len(alphabet))]
		}
		a, ea := split(func() []string { return splitAccessPath(s) })
		b, eb := split(func() []string { return splitAccessPathN(s, len(utf16Len(s))) })
		if ea != eb || !slices.Equal(a, b) {
			t.Fatalf("%q: %q %q, port %q %q", s, a, ea, b, eb)
		}
	}
}

// TestJSReplaceAll: util.replaceAll's replacement patterns, against node's
// String.prototype.replace with the same global, escaped expression.
func TestJSReplaceAll(t *testing.T) {
	for _, c := range [][4]string{
		{"a datum.label b datum.label", "datum.label", "x$&y$$z$`w$'v$1$<n>$", "a xdatum.labely$za w b datum.labelv$1$<n>$ b xdatum.labely$za datum.label b wv$1$<n>$"},
		{"datum.label", "datum.label", "$", "$"},
		{"p datum.labelq", "datum.label", "[$`|$']", "p [p |q]q"},
		{"no match", "datum.label", "$&", "no match"},
	} {
		if got := jsReplaceAll(c[0], c[1], c[2]); got != c[3] {
			t.Errorf("jsReplaceAll(%q, %q, %q) = %q, want %q", c[0], c[1], c[2], got, c[3])
		}
	}
}
