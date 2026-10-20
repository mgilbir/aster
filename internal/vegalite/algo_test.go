package vegalite

import (
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// The algorithms that replace upstream's super-linear ones (ADR 0011) give
// upstream's results: each is compared here with a port of upstream's loop,
// on inputs made to hit their edge cases.

// replaceAllPort is util.replaceAll as it was ported before C6: a string
// builder written once per match, the patterns expanded as they are met.
func replaceAllPort(s, find, repl string) string {
	if find == "" || !strings.Contains(s, find) {
		return s
	}
	if !strings.Contains(repl, "$") {
		return strings.ReplaceAll(s, find, repl)
	}
	var b []byte
	rest := 0
	for {
		i := strings.Index(s[rest:], find)
		if i < 0 {
			break
		}
		start := rest + i
		end := start + len(find)
		b = append(b, s[rest:start]...)
		for j := 0; j < len(repl); j++ {
			if repl[j] != '$' || j+1 == len(repl) {
				b = append(b, repl[j])
				continue
			}
			switch repl[j+1] {
			case '$':
				b = append(b, '$')
			case '&':
				b = append(b, find...)
			case '`':
				b = append(b, s[:start]...)
			case '\'':
				b = append(b, s[end:]...)
			default:
				b = append(b, '$')
				continue
			}
			j++
		}
		rest = end
	}
	return string(append(b, s[rest:]...))
}

func TestJSReplaceAllMatchesPort(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	pick := func(alphabet string, n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = alphabet[r.IntN(len(alphabet))]
		}
		return string(b)
	}
	for range 20000 {
		find := pick("ab", 1+r.IntN(3))
		s := pick("abx", r.IntN(16))
		repl := pick("ab$&`'1<x", r.IntN(8))
		if got, want := jsReplaceAll(nil, s, find, repl), replaceAllPort(s, find, repl); got != want {
			t.Fatalf("jsReplaceAll(%q, %q, %q) = %q, the port gives %q", s, find, repl, got, want)
		}
	}
}

// prefixIntersection is upstream's fieldIntersection: the prefixes of both
// sets made as strings, compared.
func prefixIntersection(cc *compileCtx, a, b *sset) bool {
	if a == nil || b == nil {
		return true
	}
	pa, pb := prefixGenerator(cc, a), prefixGenerator(cc, b)
	for k := range pa {
		if pb[k] {
			return true
		}
	}
	return false
}

// TestFieldIntersectionMatchesStrings compares fieldIntersection with the
// string form on paths whose joined prefixes collide although their pieces
// differ ("a[b]" as one piece and "a", "b" as two), with escapes, brackets
// and quotes, and empty sets. A field upstream cannot split is excluded: the
// throw is checked separately.
func TestFieldIntersectionMatchesStrings(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	atoms := []string{"a", "b", `a\.b`, `a\[b\]`, "[b]", "['b']", `["a.b"]`, "0", "[0]", ".", "é"}
	path := func() string {
		var b strings.Builder
		for range 1 + r.IntN(4) {
			b.WriteString(atoms[r.IntN(len(atoms))])
			if r.IntN(2) == 0 {
				b.WriteByte('.')
			}
		}
		return strings.TrimSuffix(b.String(), ".")
	}
	set := func() *sset {
		s := newSset()
		for range r.IntN(4) {
			p := path()
			if _, err := jsval.SplitFieldPath(p); err == nil {
				s.add(p)
			}
		}
		return s
	}
	agree, differ := 0, 0
	for range 20000 {
		a, b := set(), set()
		got, want := fieldIntersection(nil, a, b), prefixIntersection(nil, a, b)
		if got != want {
			t.Fatalf("fieldIntersection(%v, %v) = %v, the prefixes give %v", a.values(), b.values(), got, want)
		}
		if got {
			agree++
		} else {
			differ++
		}
	}
	if agree < 1000 || differ < 1000 {
		t.Fatalf("the cases are lopsided: %d intersect, %d do not", agree, differ)
	}
	// "a[b]" is one piece, and also the joined prefix of "a" then "b".
	if !fieldIntersection(nil, newSset(`a\[b\]`), newSset("a.b")) {
		t.Error(`a piece "a[b]" and the path a.b share the prefix "a[b]"`)
	}
	// Upstream splits a's members first: the first that cannot be split throws.
	for _, c := range []struct {
		a, b []string
		want string
	}{
		{[]string{"a]"}, []string{"b["}, "Access path missing open bracket: a]"},
		{nil, []string{"b["}, "Access path missing closing bracket: b["},
	} {
		func() {
			defer func() {
				e, _ := recover().(compileError)
				if e.msg != c.want {
					t.Errorf("fieldIntersection(%q, %q) threw %q, want %q", c.a, c.b, e.msg, c.want)
				}
			}()
			fieldIntersection(nil, newSset(c.a...), newSset(c.b...))
		}()
	}
}

// TestParseMergeMatchesSpread compares ParseNode.merge, made in place on
// the larger side (parseMap), with upstream's `{...a, ...b}` (merged), on
// parses with keys in common, array-index keys, and chains of merges in
// both directions, and the fieldIntersectsParse shortcut with the general
// form along the way.
func TestParseMergeMatchesSpread(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	keys := []string{"a", "b", "c", "0", "1", "10", "x.y", "__proto__", "constructor", "d"}
	obj := func() *Object {
		o := jsval.NewObject(0)
		for range r.IntN(6) {
			k := keys[r.IntN(len(keys))]
			if r.IntN(3) == 0 {
				k += strconv.Itoa(r.IntN(50))
			}
			jsSet(nil, o, k, jsval.Str("t"+strconv.Itoa(r.IntN(3))))
		}
		return o
	}
	same := func(x, y *Object) bool {
		return slices.Equal(x.Keys(), y.Keys()) && jsval.Equal(jsval.Obj(x), jsval.Obj(y))
	}
	for range 5000 {
		var want *Object
		var pm *parseMap
		for i := range 1 + r.IntN(6) {
			o := obj()
			if i == 0 {
				want, pm = o.Clone(), newParseMap(nil, o)
				continue
			}
			if r.IntN(2) == 0 {
				want = merged(jsval.Obj(want), jsval.Obj(o))
				pm.mergeFrom(nil, newParseMap(nil, o))
			} else {
				want = merged(jsval.Obj(o), jsval.Obj(want))
				other := newParseMap(nil, o)
				other.mergeFrom(nil, pm)
				pm = other
			}
			if !same(pm.object(nil), want) {
				t.Fatalf("merge: %q, the spread gives %q", pm.object(nil).Keys(), want.Keys())
			}
			pn := &parseNode{pm: pm}
			a := newSset(keys[r.IntN(len(keys))])
			if got, want := fieldIntersectsParse(nil, a, pn), fieldIntersection(nil, a, pn.dependentFields()); got != want {
				t.Fatalf("fieldIntersectsParse(%v, %q) = %v, want %v", a.values(), pm.object(nil).Keys(), got, want)
			}
		}
		if r.IntN(3) == 0 {
			drop := keys[r.IntN(len(keys))]
			pm.deleteFunc(nil, func(k string) bool { return k == drop })
			want.DeleteFunc(func(k string, _ Value) bool { return k == drop })
			if !same(pm.object(nil), want) {
				t.Fatalf("delete %q: %q, want %q", drop, pm.object(nil).Keys(), want.Keys())
			}
		}
	}
}
