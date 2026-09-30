package wordcloud

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

type goldenCase struct {
	Name    string     `json:"name"`
	Size    [2]float64 `json:"size"`
	Spiral  string     `json:"spiral"`
	Rotate  bool       `json:"rotate"`
	Padding float64    `json:"padding"`
	Range   []float64  `json:"range"`
	Seed    uint32     `json:"seedRandom"`
	Words   []struct {
		ID    int    `json:"id"`
		Text  string `json:"text"`
		Value int    `json:"value"`
		Rot   int    `json:"rot"`
	} `json:"words"`
	Placed []struct {
		ID   int     `json:"id"`
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
		Size int     `json:"size"`
	} `json:"placed"`
}

func loadGolden(t testing.TB) []goldenCase {
	t.Helper()
	b, err := os.ReadFile("testdata/wordcloud.json")
	if err != nil {
		t.Fatal(err)
	}
	var cs []goldenCase
	if err := json.Unmarshal(b, &cs); err != nil {
		t.Fatal(err)
	}
	return cs
}

func (c goldenCase) params(fontField bool) (Params, []jsval.Value) {
	data := make([]jsval.Value, len(c.Words))
	for i, w := range c.Words {
		data[i] = jsval.Obj(jsval.ObjectOf("text", jsval.Str(w.Text), "value", jsval.Int(w.Value), "rot", jsval.Int(w.Rot)))
	}
	p := Params{
		Size:    &c.Size,
		Text:    func(d jsval.Value) jsval.Value { return d.Get("text") },
		Spiral:  Spiral(c.Spiral),
		Padding: Const(jsval.Num(c.Padding)),
		Random:  LCG(c.Seed),
	}
	if c.Rotate {
		p.Rotate = func(d jsval.Value) jsval.Value { return d.Get("rot") }
	}
	if c.Range != nil {
		p.FontSize = func(d jsval.Value) jsval.Value { return d.Get("value") }
		p.FontSizeIsField = true
		p.FontSizeRange = c.Range
	}
	return p, data
}

func TestGolden(t *testing.T) {
	for _, c := range loadGolden(t) {
		t.Run(c.Name, func(t *testing.T) {
			p, data := c.params(false)
			if err := Transform(context.Background(), data, p); err != nil {
				t.Fatal(err)
			}
			want := map[int]int{}
			for i, pl := range c.Placed {
				want[pl.ID] = i
			}
			for i, d := range data {
				x, y, fs := d.Get("x").NumValue(), d.Get("y").NumValue(), d.Get("fontSize").NumValue()
				j, ok := want[i]
				if !ok {
					if !math.IsNaN(x) || !math.IsNaN(y) || fs != 0 {
						t.Errorf("word %d should be unplaced, got x=%v y=%v fs=%v", i, x, y, fs)
					}
					continue
				}
				pl := c.Placed[j]
				if x != pl.X || y != pl.Y || int(fs) != pl.Size {
					t.Errorf("word %d: got (%v,%v,%v) want (%v,%v,%v)", i, x, y, fs, pl.X, pl.Y, pl.Size)
				}
			}
		})
	}
}

func TestBadInput(t *testing.T) {
	ctx := context.Background()
	zero := [2]float64{0, 10}
	if err := Transform(ctx, nil, Params{Size: &zero}); err == nil {
		t.Error("zero size must error")
	}
	huge := [2]float64{1e9, 1e9}
	if err := Transform(ctx, nil, Params{Size: &huge}); err == nil {
		t.Error("huge size must error")
	}
	if err := Transform(ctx, []jsval.Value{jsval.Num(1)}, Params{}); err == nil {
		t.Error("non-object tuple must error")
	}
	// Degenerate but legal inputs must not panic.
	odd := [2]float64{0.5, 3}
	d := []jsval.Value{jsval.Obj(jsval.ObjectOf("t", jsval.Str(""))), jsval.Obj(jsval.ObjectOf("t", jsval.Num(math.NaN())))}
	_ = Transform(ctx, d, Params{Size: &odd, Text: func(v jsval.Value) jsval.Value { return v.Get("t") },
		FontSize: Const(jsval.Num(math.Inf(1))), Rotate: Const(jsval.Num(math.NaN()))})
}

func TestCancel(t *testing.T) {
	c := loadGolden(t)[4]
	p, data := c.params(false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Transform(ctx, data, p); err == nil {
		t.Error("expected cancellation error")
	}
}

func BenchmarkLayout(b *testing.B) {
	c := loadGolden(b)[1]
	for b.Loop() {
		p, data := c.params(false)
		if err := Transform(context.Background(), data, p); err != nil {
			b.Fatal(err)
		}
	}
}
