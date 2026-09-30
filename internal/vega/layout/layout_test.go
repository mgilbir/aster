package layout

import (
	"compress/gzip"
	"fmt"
	"io"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

func loadRecords(t *testing.T) jsval.Value {
	t.Helper()
	f, err := os.Open("testdata/layout.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsval.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// jnum decodes a number as the recorder wrote it ({$num} for non-finite).
func jnum(v jsval.Value) float64 {
	if v.IsObj() {
		switch v.Get("$num").StrValue() {
		case "Infinity":
			return math.Inf(1)
		case "-Infinity":
			return math.Inf(-1)
		}
		return math.NaN()
	}
	return jsval.ToNumber(v)
}

func jbounds(v jsval.Value) (b scene.Bounds, ok bool) {
	if !v.IsArr() || v.Len() != 4 {
		return b, false
	}
	b.X1, b.Y1, b.X2, b.Y2 = jnum(v.Index(0)), jnum(v.Index(1)), jnum(v.Index(2)), jnum(v.Index(3))
	return b, true
}

// attachBounds copies the recorded bounds onto the freshly loaded scene (the
// loader never reads them).
func attachBounds(m *scene.Mark, v jsval.Value) {
	if b, ok := jbounds(v.Get("bounds")); ok {
		m.Bounds = b
	}
	items := v.Get("items")
	for i, it := range m.Items {
		iv := items.Index(i)
		if b, ok := jbounds(iv.Get("bounds")); ok {
			it.Bounds = b
		}
		children := iv.Get("items")
		for j, cm := range it.Items {
			attachBounds(cm, children.Index(j))
		}
	}
}

func sameNum(a, b float64) bool {
	// layout arithmetic is reproduced bit for bit (see the package comment on
	// fused multiply-add), so no tolerance is applied
	return a == b || (math.IsNaN(a) && math.IsNaN(b))
}

func sameBounds(a, b scene.Bounds) bool {
	return sameNum(a.X1, b.X1) && sameNum(a.Y1, b.Y1) && sameNum(a.X2, b.X2) && sameNum(a.Y2, b.Y2)
}

func boundsStr(b scene.Bounds) string {
	return fmt.Sprintf("[%v %v %v %v]", b.X1, b.Y1, b.X2, b.Y2)
}

func checkNum(path, name string, got scene.Num, want jsval.Value) string {
	if want.IsUndefined() || want.IsNull() {
		if got.Set() {
			return fmt.Sprintf("%s.%s: got %v, want unset", path, name, got.Val())
		}
		return ""
	}
	if !got.Set() || !sameNum(got.Val(), jnum(want)) {
		return fmt.Sprintf("%s.%s: got %v (set=%v), want %v", path, name, got.Val(), got.Set(), jnum(want))
	}
	return ""
}

// compareMark reports the first difference between a laid-out mark and its
// recorded post-layout state: item geometry and every bounds box.
func compareMark(path string, m *scene.Mark, want jsval.Value) string {
	if wb, ok := jbounds(want.Get("bounds")); ok && !sameBounds(m.Bounds, wb) {
		return fmt.Sprintf("%s(%s).bounds: got %s, want %s", path, m.Role, boundsStr(m.Bounds), boundsStr(wb))
	}
	wantItems := want.Get("items")
	if len(m.Items) != wantItems.Len() {
		return fmt.Sprintf("%s: %d items, want %d", path, len(m.Items), wantItems.Len())
	}
	for i, it := range m.Items {
		iv := wantItems.Index(i)
		p := fmt.Sprintf("%s(%s).items[%d]", path, m.Role, i)
		for _, f := range []struct {
			name string
			n    scene.Num
		}{{"x", it.X}, {"y", it.Y}, {"width", it.Width}, {"height", it.Height}} {
			if d := checkNum(p, f.name, f.n, iv.Get(f.name)); d != "" {
				return d
			}
		}
		if wb, ok := jbounds(iv.Get("bounds")); ok && !sameBounds(it.Bounds, wb) {
			return fmt.Sprintf("%s.bounds: got %s, want %s", p, boundsStr(it.Bounds), boundsStr(wb))
		}
		children := iv.Get("items")
		if children.IsArr() {
			if len(it.Items) != children.Len() {
				return fmt.Sprintf("%s: %d child marks, want %d", p, len(it.Items), children.Len())
			}
			for j, cm := range it.Items {
				if d := compareMark(fmt.Sprintf("%s.marks[%d]", p, j), cm, children.Index(j)); d != "" {
					return d
				}
			}
		}
	}
	return ""
}

func TestViewLayoutAgainstUpstream(t *testing.T) {
	cases := loadRecords(t)
	total, exact := 0, 0
	for _, c := range cases.Items() {
		name := c.Get("name").StrValue()
		for li, rec := range c.Get("layouts").Items() {
			t.Run(fmt.Sprintf("%s/%d", name, li), func(t *testing.T) {
				pre := jsval.AppendJSON(nil, rec.Get("pre"))
				sg, err := scene.FromJSON(pre)
				if err != nil {
					t.Fatal(err)
				}
				mark := sg.Root
				attachBounds(mark, rec.Get("pre"))

				params := rec.Get("params")
				p := Params{Legends: params.Get("legends")}
				if l := params.Get("layout"); l.IsObj() {
					p.Grid = ParseGridSpec(l)
				}
				if a := params.Get("autosize"); a.IsObj() {
					au := ParseAutosize(a)
					p.Autosize = &au
				}
				vw := rec.Get("view")
				pad := vw.Get("padding")
				view := &View{
					Width: jnum(vw.Get("width")), Height: jnum(vw.Get("height")),
					Padding:        Padding{Left: jnum(pad.Get("left")), Right: jnum(pad.Get("right")), Top: jnum(pad.Get("top")), Bottom: jnum(pad.Get("bottom"))},
					AutosizeActive: jnum(vw.Get("autosize")) >= 1,
				}
				sizes := ViewLayout(mark, view, p)

				want := rec.Get("sizes")
				if len(sizes) != want.Len() {
					t.Fatalf("%d size adjustments, want %d", len(sizes), want.Len())
				}
				for i, s := range sizes {
					w := want.Index(i)
					got := []float64{s.ViewWidth, s.ViewHeight, s.Width, s.Height, s.Origin[0], s.Origin[1]}
					exp := []float64{jnum(w.Index(0)), jnum(w.Index(1)), jnum(w.Index(2)), jnum(w.Index(3)), jnum(w.Index(4).Index(0)), jnum(w.Index(4).Index(1))}
					for k := range got {
						if !sameNum(got[k], exp[k]) {
							t.Fatalf("size[%d] %v, want %v", i, got, exp)
						}
					}
					if s.Resize != w.Index(5).IsTruthy() {
						t.Fatalf("resize %v, want %v", s.Resize, w.Index(5))
					}
				}
				if d := compareMark("root", mark, rec.Get("post")); d != "" {
					t.Fatal(d)
				}
				total++
			})
		}
	}
	_ = exact
	if total == 0 {
		t.Fatal("no layouts ran")
	}
}
