// Package confusion is the type-confusion mutator: see Confuse.
package confusion

import (
	"math"
	"math/rand"
	"slices"
	"strconv"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// The type-confusion mutator, shared by internal/vegalite's TestTypeConfusion
// (no panic) and the root package's TestVegaLiteTypeConfusionMatchesNode (the
// differential against node, ADR 0013): one vocabulary, so the two cannot
// drift.
//
// A mutant replaces one value of a specification, or renames one key. The
// replacement is drawn either from ConfusedValues, values of the wrong type
// for almost every slot, or from the value it replaces. The derived forms
// are what a fixed list cannot produce and where JavaScript and Go readings
// of a value part (the 2026-10-18 audit, C54):
//   - [orig] and [[orig]], whose String is orig's: upstream's ===, Set.has and
//     switch refuse them, its property lookups and template literals do not;
//   - String(orig), and Number(orig) for a numeric text or a boolean;
//   - [...orig, null] (and [orig, null]): Array.prototype.join writes null as "";
//   - a key renamed to "undefined" (the key upstream reads for an undefined
//     name), "", or one of Object.prototype's members: "__proto__",
//     "constructor", and "toString" and "valueOf", which an object holding
//     them as data cannot be converted to text by;
//   - case-mapped spellings: upper and lower case, and the characters whose
//     JavaScript and Go case mappings differ (dotted İ, dotless ı, the Kelvin
//     sign, long ſ);
//   - another string of the specification, as it is or as [text], and
//     [...orig, [orig[i]]]: an array and its text where upstream gathers
//     both into one Set, which keeps them apart (SameValueZero), as a
//     string-keyed set would not;
//   - a "__proto__" member of null, {} or an object holding another member
//     of the specification, added to an object: JSON.parse makes it an own
//     member, and upstream's assignments of it replace a prototype (null
//     leaves none), its for…in walks the prototype's members.

// ConfusedValues are values of the wrong type for almost every slot of a
// specification.
var ConfusedValues = []string{
	"null", "true", "false", `""`, `"x"`, `"1e999"`, `"NaN"`, `"__proto__"`, `"constructor"`, `"datum.x"`,
	"0", "-1", "1e308", "[]", "{}", "[1,2]", `["a","b"]`, `{"a":1}`, "[[1]]", "[{}]",
	`{"signal":"1/0"}`, `{"signal":"datum"}`, `{"field":"nofield"}`, `{"value":null}`, `{"type":{}}`,
}

// renamedKeys are the names a key is renamed to.
var renamedKeys = []string{"undefined", "", "__proto__", "constructor", "toString", "valueOf"}

// ConfusionSlot is a settable position in a parsed specification: a member
// of an object (Obj, Key) or an element of an array (Arr, Index).
type ConfusionSlot struct {
	Obj   *jsval.Object
	Key   string
	Arr   jsval.Value
	Index int
	Path  []string
}

// Get is the slot's value.
func (s ConfusionSlot) Get() jsval.Value {
	if s.Obj != nil {
		return s.Obj.Lookup(s.Key)
	}
	return s.Arr.Index(s.Index)
}

// Set replaces the slot's value.
func (s ConfusionSlot) Set(v jsval.Value) {
	if s.Obj != nil {
		s.Obj.Set(s.Key, v)
		return
	}
	s.Arr.Items()[s.Index] = v
}

// Rename renames an object member's key to name, in place (the member keeps
// its position, as a JSON text with the key spelled otherwise would). It
// reports false for an array element, or when the object already has name.
func (s ConfusionSlot) Rename(name string) bool {
	if s.Obj == nil || s.Obj.Has(name) {
		return false
	}
	o := s.Obj
	keys := append([]string(nil), o.Keys()...)
	vals := make([]jsval.Value, len(keys))
	for i := range keys {
		vals[i] = o.ValueAt(i)
	}
	o.DeleteFunc(func(string, jsval.Value) bool { return true })
	for i, k := range keys {
		if k == s.Key {
			k = name
		}
		o.Set(k, vals[i])
	}
	return true
}

// ConfusionSlots lists every slot below root (the root itself is not one).
func ConfusionSlots(root jsval.Value) []ConfusionSlot {
	var out []ConfusionSlot
	var walk func(v jsval.Value, path []string)
	walk = func(v jsval.Value, path []string) {
		switch {
		case v.IsObj():
			o := v.ObjValue()
			for i := 0; i < o.Len(); i++ {
				k := o.KeyAt(i)
				p := append(append([]string(nil), path...), k)
				out = append(out, ConfusionSlot{Obj: o, Key: k, Path: p})
				walk(o.ValueAt(i), p)
			}
		case v.IsArr():
			for i := 0; i < v.Len(); i++ {
				p := append(append([]string(nil), path...), strconv.Itoa(i))
				out = append(out, ConfusionSlot{Arr: v, Index: i, Path: p})
				walk(v.Index(i), p)
			}
		}
	}
	walk(root, nil)
	return out
}

// Confuse mutates root in place at one slot drawn with rng: half the time a
// value of ConfusedValues, otherwise a mutation derived from the value (or
// the key) it replaces. It returns the slot and a label of the mutation for
// grouping differences (the replacement's JSON for a fixed value, the form
// for a derived one, e.g. "[orig]" or "key→undefined"); ok is false when
// root has no slot.
func Confuse(rng *rand.Rand, root jsval.Value) (slot ConfusionSlot, label string, ok bool) {
	slots := ConfusionSlots(root)
	if len(slots) == 0 {
		return slot, "", false
	}
	slot = slots[rng.Intn(len(slots))]
	if rng.Intn(2) == 0 {
		val := ConfusedValues[rng.Intn(len(ConfusedValues))]
		nv, err := jsval.ParseJSONString(val)
		if err != nil {
			panic("bad confused value " + val)
		}
		slot.Set(nv)
		return slot, val, true
	}
	orig := slot.Get()
	for {
		switch rng.Intn(12) {
		case 0:
			slot.Set(jsval.ArrOf(orig))
			return slot, "[orig]", true
		case 1:
			slot.Set(jsval.ArrOf(jsval.ArrOf(orig)))
			return slot, "[[orig]]", true
		case 2:
			if orig.IsStr() {
				continue
			}
			slot.Set(jsval.Str(orig.AsString()))
			return slot, "String(orig)", true
		case 3:
			if n, ok := numberOf(orig); ok {
				slot.Set(jsval.Num(n))
				return slot, "Number(orig)", true
			}
		case 4:
			items := []jsval.Value{orig}
			if orig.IsArr() {
				items = append([]jsval.Value(nil), orig.Items()...)
			}
			slot.Set(jsval.Arr(append(items, jsval.Null)))
			return slot, "[...orig, null]", true
		case 5:
			if slot.Obj == nil {
				continue
			}
			name := renamedKeys[rng.Intn(len(renamedKeys))]
			if slot.Rename(name) {
				return slot, "key→" + strconv.Quote(name), true
			}
		case 6:
			if slot.Obj == nil {
				continue
			}
			if name, form, ok := respell(rng, slot.Key); ok && slot.Rename(name) {
				return slot, "key→" + form, true
			}
		case 7, 8:
			if !orig.IsStr() {
				continue
			}
			if s, form, ok := respell(rng, orig.StrValue()); ok {
				slot.Set(jsval.Str(s))
				return slot, form, true
			}
		case 9:
			if other, ok := anotherString(rng, slots, slot); ok {
				if rng.Intn(2) == 0 {
					slot.Set(jsval.ArrOf(other))
					return slot, "[another]", true
				}
				slot.Set(other)
				return slot, "another", true
			}
		case 10:
			if !orig.IsArr() || orig.Len() == 0 {
				continue
			}
			items := append([]jsval.Value(nil), orig.Items()...)
			e := items[rng.Intn(len(items))]
			slot.Set(jsval.Arr(append(items, jsval.ArrOf(e))))
			return slot, "[...orig, [orig[i]]]", true
		case 11:
			if label, ok := addProto(rng, slots, slot); ok {
				return slot, label, true
			}
		}
		// The form drawn does not apply to this slot: fall back to the
		// array wrappings, which always do.
		if rng.Intn(3) == 0 {
			slot.Set(jsval.ArrOf(orig))
			return slot, "[orig]", true
		}
	}
}

// anotherString is a string value of another slot, drawn with rng.
func anotherString(rng *rand.Rand, slots []ConfusionSlot, not ConfusionSlot) (jsval.Value, bool) {
	var strs []jsval.Value
	for _, s := range slots {
		if v := s.Get(); v.IsStr() && !slices.Equal(s.Path, not.Path) {
			strs = append(strs, v)
		}
	}
	if len(strs) == 0 {
		return jsval.Value{}, false
	}
	return strs[rng.Intn(len(strs))], true
}

// addProto adds a "__proto__" member to the object the slot's value is, or
// else to the object holding the slot, at a position drawn with rng. Its
// value is null, {}, or an object of one member of the specification (a
// key and value of another slot), which upstream's reads find through the
// prototype once an assignment made it one.
func addProto(rng *rand.Rand, slots []ConfusionSlot, slot ConfusionSlot) (string, bool) {
	var o *jsval.Object
	switch v := slot.Get(); {
	case v.IsObj() && (slot.Obj == nil || rng.Intn(2) == 0):
		o = v.ObjValue()
	case slot.Obj != nil:
		o = slot.Obj
	default:
		return "", false
	}
	if o.Has("__proto__") {
		return "", false
	}
	var val jsval.Value
	var label string
	switch rng.Intn(3) {
	case 0:
		val, label = jsval.Null, "__proto__:null"
	case 1:
		val, label = jsval.Obj(jsval.NewObject(0)), "__proto__:{}"
	default:
		var named []ConfusionSlot
		for _, s := range slots {
			if s.Obj != nil {
				named = append(named, s)
			}
		}
		if len(named) == 0 {
			return "", false
		}
		from := named[rng.Intn(len(named))]
		// A copy: the member may hold o itself.
		cp, err := jsval.ParseJSON(jsval.AppendJSON(nil, from.Get()))
		if err != nil {
			panic("confusion: a parsed value does not round-trip: " + err.Error())
		}
		p := jsval.NewObject(1)
		p.Set(from.Key, cp)
		val, label = jsval.Obj(p), "__proto__:{k}"
	}
	insertAt(o, rng.Intn(o.Len()+1), "__proto__", val)
	return label, true
}

// insertAt puts the member k: v at position at of o, which lacks k.
func insertAt(o *jsval.Object, at int, k string, v jsval.Value) {
	keys := append([]string(nil), o.Keys()...)
	vals := make([]jsval.Value, len(keys))
	for i := range keys {
		vals[i] = o.ValueAt(i)
	}
	o.DeleteFunc(func(string, jsval.Value) bool { return true })
	for i := range keys {
		if i == at {
			o.Set(k, v)
		}
		o.Set(keys[i], vals[i])
	}
	if at == len(keys) {
		o.Set(k, v)
	}
}

// numberOf is Number(v) for a numeric text or a boolean, when finite (JSON
// has no other numbers).
func numberOf(v jsval.Value) (float64, bool) {
	switch {
	case v.IsBool():
		if v.BoolValue() {
			return 1, true
		}
		return 0, true
	case v.IsStr():
		if jsval.JSTrim(v.StrValue()) == "" {
			return 0, false
		}
		n := jsval.ToNumber(v)
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// respell is s spelled with a case mapping, or with one of the characters
// whose JavaScript and Go case mappings differ: dotted İ for i or I
// (toLowerCase gives "i̇", Go's ToLower "i"), dotless ı for i
// (toUpperCase gives "I"), the Kelvin sign K for k or K (toLowerCase gives
// "k"), long ſ for s (toUpperCase gives "S"). form names the spelling.
func respell(rng *rand.Rand, s string) (out, form string, ok bool) {
	if s == "" {
		return "", "", false
	}
	switch rng.Intn(6) {
	case 0:
		if u := jsval.JSUpper(s); u != s {
			return u, "upper(orig)", true
		}
	case 1:
		if u := jsval.UpperFirst(s); u != s {
			return u, "upperFirst(orig)", true
		}
	case 2:
		if r := replaceOne(rng, s, "iI", "İ"); r != s {
			return r, "dotted İ", true
		}
	case 3:
		if r := replaceOne(rng, s, "i", "ı"); r != s {
			return r, "dotless ı", true
		}
	case 4:
		if r := replaceOne(rng, s, "kK", "K"); r != s {
			return r, "Kelvin K", true
		}
	case 5:
		if r := replaceOne(rng, s, "s", "ſ"); r != s {
			return r, "long ſ", true
		}
	}
	return "", "", false
}

// replaceOne replaces one occurrence (drawn with rng) of a byte of set in s
// by with.
func replaceOne(rng *rand.Rand, s, set, with string) string {
	var at []int
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(set, s[i]) >= 0 {
			at = append(at, i)
		}
	}
	if len(at) == 0 {
		return s
	}
	i := at[rng.Intn(len(at))]
	return s[:i] + with + s[i+1:]
}
