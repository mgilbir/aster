package layout

import (
	"fmt"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

func TestOverlapAgainstUpstream(t *testing.T) {
	ran := 0
	for _, c := range loadRecords(t).Items() {
		for oi, rec := range c.Get("overlaps").Items() {
			name := fmt.Sprintf("%s/%d", c.Get("name").StrValue(), oi)
			t.Run(name, func(t *testing.T) {
				mark := &scene.Mark{Type: scene.MarkText, Bounds: scene.NewBounds()}
				for _, iv := range rec.Get("pre").Items() {
					it := &scene.Item{Mark: mark}
					if b, ok := jbounds(iv.Get("bounds")); ok {
						it.Bounds = b
					}
					if o := iv.Get("opacity"); !o.IsNullish() {
						it.Opacity = scene.N(jnum(o))
					}
					it.Datum = jsval.Obj(jsval.ObjectOf("index", iv.Get("index")))
					mark.Items = append(mark.Items, it)
				}
				if b, ok := jbounds(rec.Get("markBoundsPre")); ok {
					mark.Bounds = b
				}
				params := rec.Get("params")
				p := OverlapParams{
					Method:     params.Get("method"),
					Separation: jsval.ToNumber(params.Get("separation")),
				}
				if o := params.Get("order"); o.IsStr() {
					p.Order = o.StrValue()
				}
				if b := params.Get("bound"); b.IsObj() {
					p.Bound = &OverlapBound{
						Range:     [2]float64{jnum(b.Get("range").Index(0)), jnum(b.Get("range").Index(1))},
						Orient:    b.Get("orient").StrValue(),
						Tolerance: jnum(b.Get("tolerance")),
					}
				}
				Overlap(mark, p)
				post := rec.Get("post")
				for i, it := range mark.Items {
					got := it.Opacity
					want := post.Index(i)
					if want.IsNullish() {
						if got.Set() {
							t.Fatalf("item %d: opacity %v, want unset", i, got.Val())
						}
						continue
					}
					if !got.Set() || got.Val() != jnum(want) {
						t.Fatalf("item %d: opacity %v (set=%v), want %v", i, got.Val(), got.Set(), jnum(want))
					}
				}
				if wb, ok := jbounds(rec.Get("markBounds")); ok && !sameBounds(mark.Bounds, wb) {
					t.Fatalf("mark bounds %s, want %s", boundsStr(mark.Bounds), boundsStr(wb))
				}
				ran++
			})
		}
	}
	if ran == 0 {
		t.Fatal("no overlap records ran")
	}
}

func labels(n int, width, gap float64) *scene.Mark {
	m := &scene.Mark{Type: scene.MarkText, Bounds: scene.NewBounds()}
	for i := 0; i < n; i++ {
		x := float64(i) * (width + gap)
		it := &scene.Item{Mark: m, Datum: jsval.Obj(jsval.ObjectOf("index", jsval.Int(i)))}
		it.Bounds.Set(x, 0, x+width, 10)
		m.Items = append(m.Items, it)
	}
	return m
}

func opacities(m *scene.Mark) (out []float64) {
	for _, it := range m.Items {
		out = append(out, it.Opacity.Or(-1))
	}
	return out
}

func TestOverlapMethods(t *testing.T) {
	// 6 labels of width 10 overlapping their neighbours by 5: parity keeps every other one
	m := labels(6, 10, -5)
	Overlap(m, OverlapParams{Method: jsval.True})
	if got := opacities(m); fmt.Sprint(got) != "[1 0 1 0 1 0]" {
		// the last item is forced visible when fewer than three remain; here 3 remain
		t.Fatalf("parity: %v", got)
	}

	m = labels(6, 10, -5)
	Overlap(m, OverlapParams{Method: jsval.Str("greedy")})
	if got := opacities(m); fmt.Sprint(got) != "[1 0 1 0 1 0]" {
		t.Fatalf("greedy: %v", got)
	}

	// nothing overlaps: everything visible
	m = labels(6, 10, 5)
	Overlap(m, OverlapParams{Method: jsval.Str("greedy")})
	if got := opacities(m); fmt.Sprint(got) != "[1 1 1 1 1 1]" {
		t.Fatalf("clear: %v", got)
	}

	// separation turns near misses into overlaps
	m = labels(6, 10, 1)
	Overlap(m, OverlapParams{Method: jsval.Str("greedy"), Separation: 5})
	if got := opacities(m); fmt.Sprint(got) != "[1 0 1 0 1 0]" {
		t.Fatalf("separation: %v", got)
	}

	// a falsy method leaves everything alone
	m = labels(4, 10, -5)
	Overlap(m, OverlapParams{Method: jsval.False})
	if got := opacities(m); fmt.Sprint(got) != "[-1 -1 -1 -1]" {
		t.Fatalf("no method: %v", got)
	}

	// heavy overlap keeps the first and last items visible
	m = labels(9, 10, -9)
	Overlap(m, OverlapParams{Method: jsval.True})
	got := opacities(m)
	if got[0] != 1 || got[len(got)-1] != 1 {
		t.Fatalf("endpoints hidden: %v", got)
	}

	// bounds: items leaving the range are hidden
	m = labels(4, 10, 5)
	Overlap(m, OverlapParams{Method: jsval.True, Bound: &OverlapBound{Range: [2]float64{0, 40}, Orient: "bottom", Tolerance: 1}})
	if got := opacities(m); fmt.Sprint(got) != "[1 1 1 0]" {
		t.Fatalf("bound: %v", got)
	}
}

func TestOverlapLarge(t *testing.T) {
	// work stays linear-ish for a huge, fully overlapping label set
	m := labels(200000, 10, -9.5)
	Overlap(m, OverlapParams{Method: jsval.True})
	shown := 0
	for _, o := range opacities(m) {
		if o == 1 {
			shown++
		}
	}
	// every halving doubles the spacing (0.5px) until it reaches the label width
	if shown != 6250 {
		t.Fatalf("%d labels left visible, want 6250", shown)
	}
	m = labels(200000, 10, -9.5)
	Overlap(m, OverlapParams{Method: jsval.Str("greedy")})
}
