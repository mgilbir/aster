package force

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

type goldenCase struct {
	Name  string            `json:"name"`
	Nodes []json.RawMessage `json:"nodes"`
	Edges []json.RawMessage `json:"edges"`
	Spec  struct {
		Forces        []map[string]any `json:"forces"`
		Static        bool             `json:"static"`
		Iterations    *int             `json:"iterations"`
		Alpha         *float64         `json:"alpha"`
		AlphaMin      *float64         `json:"alphaMin"`
		VelocityDecay *float64         `json:"velocityDecay"`
	} `json:"spec"`
	Out []struct {
		X, Y, Vx, Vy float64
		Index        int
	} `json:"out"`
	Bound map[string]float64 `json:"bound"`
	Links []struct {
		Index          int
		Source, Target int
	} `json:"links"`
}

func field(name string) Accessor {
	return func(v jsval.Value) jsval.Value { return v.Get(name) }
}

// exprs maps the expression strings used by the generator to Go closures.
var exprs = map[string]Accessor{
	"datum.r": field("r"),
	"datum.k * 20": func(v jsval.Value) jsval.Value {
		return jsval.Num(v.Get("k").NumValue() * 20)
	},
	"0.3": func(jsval.Value) jsval.Value { return jsval.Num(0.3) },
	"-datum.r * 3": func(v jsval.Value) jsval.Value {
		return jsval.Num(-v.Get("r").NumValue() * 3)
	},
}

func param(m map[string]any, key string, def Param) Param {
	switch v := m[key].(type) {
	case nil:
		return def
	case float64:
		return Constant(v)
	case map[string]any:
		return Param{Fn: exprs[v["expr"].(string)]}
	}
	panic("bad param " + key)
}

func num(m map[string]any, key string, def float64) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return def
}

func buildParams(c *goldenCase, links []jsval.Value) Params {
	p := DefaultParams()
	p.Static = c.Spec.Static
	if c.Spec.Iterations != nil {
		p.Iterations = *c.Spec.Iterations
	}
	if c.Spec.Alpha != nil {
		p.Alpha = *c.Spec.Alpha
	}
	if c.Spec.AlphaMin != nil {
		p.AlphaMin = *c.Spec.AlphaMin
	}
	if c.Spec.VelocityDecay != nil {
		p.VelocityDecay = *c.Spec.VelocityDecay
	}
	if c.Bound != nil {
		p.Bound = BoundAlpha | BoundAlphaMin | BoundAlphaTarget | BoundVelocityDecay
		p.AlphaTarget = c.Bound["alphaTarget"]
	}
	for _, f := range c.Spec.Forces {
		switch f["force"] {
		case "center":
			p.Forces = append(p.Forces, Center{X: num(f, "x", 0), Y: num(f, "y", 0)})
		case "collide":
			cf := NewCollide()
			cf.Radius = param(f, "radius", cf.Radius)
			cf.Strength = num(f, "strength", cf.Strength)
			cf.Iterations = int(num(f, "iterations", 1))
			p.Forces = append(p.Forces, cf)
		case "nbody":
			nb := NewNBody()
			nb.Strength = param(f, "strength", nb.Strength)
			nb.Theta = num(f, "theta", nb.Theta)
			nb.DistanceMin = num(f, "distanceMin", nb.DistanceMin)
			nb.DistanceMax = num(f, "distanceMax", nb.DistanceMax)
			p.Forces = append(p.Forces, nb)
		case "link":
			l := NewLink(links)
			if id, ok := f["id"].(string); ok {
				l.ID = field(id)
			}
			l.Distance = param(f, "distance", l.Distance)
			if _, ok := f["strength"]; ok {
				s := param(f, "strength", Param{})
				l.Strength = &s
			}
			l.Iterations = int(num(f, "iterations", 1))
			p.Forces = append(p.Forces, l)
		case "x":
			x := NewX()
			x.Strength = num(f, "strength", x.Strength)
			if s, ok := f["x"].(string); ok {
				x.X = Param{Fn: field(s)}
			}
			p.Forces = append(p.Forces, x)
		case "y":
			y := NewY()
			y.Strength = num(f, "strength", y.Strength)
			p.Forces = append(p.Forces, y)
		}
	}
	return p
}

func parseAll(t testing.TB, raws []json.RawMessage) []jsval.Value {
	out := make([]jsval.Value, len(raws))
	for i, r := range raws {
		v, err := jsval.ParseJSON(r)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = v
	}
	return out
}

func near(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

func TestGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/force.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []goldenCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for i := range cases {
		c := &cases[i]
		t.Run(c.Name, func(t *testing.T) {
			nodes, links := parseAll(t, c.Nodes), parseAll(t, c.Edges)
			if err := Run(context.Background(), nodes, buildParams(c, links)); err != nil {
				t.Fatal(err)
			}
			for i, want := range c.Out {
				n := nodes[i]
				got := [4]float64{n.Get("x").NumValue(), n.Get("y").NumValue(), n.Get("vx").NumValue(), n.Get("vy").NumValue()}
				w := [4]float64{want.X, want.Y, want.Vx, want.Vy}
				for k := range got {
					if !near(got[k], w[k]) {
						t.Fatalf("node %d comp %d: got %v want %v", i, k, got[k], w[k])
					}
				}
				if int(n.Get("index").NumValue()) != want.Index {
					t.Fatalf("node %d index", i)
				}
			}
			for i, want := range c.Links {
				l := links[i]
				if int(l.Get("index").NumValue()) != want.Index ||
					int(l.Get("source").Get("index").NumValue()) != want.Source ||
					int(l.Get("target").Get("index").NumValue()) != want.Target {
					t.Fatalf("link %d wrong resolution", i)
				}
			}
		})
	}
}

func TestLCG(t *testing.T) {
	l := lcg{s: 1}
	// (1664525*1 + 1013904223) / 2^32
	if got, want := l.next(), 1015568748.0/4294967296; got != want {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	obj := func(kv ...any) jsval.Value { return jsval.Obj(jsval.ObjectOf(kv...)) }
	S, N := jsval.Str, jsval.Num
	nodes := []jsval.Value{obj("id", S("a"))}
	links := []jsval.Value{obj("source", S("a"), "target", S("zzz"))}
	p := DefaultParams()
	l := NewLink(links)
	l.ID = field("id")
	p.Forces = []Force{l}
	if err := Run(ctx, nodes, p); err == nil || err.Error() != "node not found: zzz" {
		t.Fatalf("got %v", err)
	}
	p = DefaultParams()
	p.Iterations = MaxIterations + 1
	if err := Run(ctx, nodes, p); err == nil {
		t.Fatal("expected iteration limit error")
	}
	if err := Run(ctx, []jsval.Value{jsval.Num(1)}, DefaultParams()); err == nil {
		t.Fatal("expected non-object error")
	}
	// Empty input and infinite/NaN coordinates must not panic or hang.
	p = DefaultParams()
	p.Static = true
	p.Forces = []Force{NewNBody(), NewCollide(), Center{}}
	if err := Run(ctx, nil, p); err != nil {
		t.Fatal(err)
	}
	bad := []jsval.Value{obj("x", N(math.Inf(1)), "y", N(0)), obj("x", N(1), "y", N(2)), obj("x", N(1), "y", N(2))}
	if err := Run(ctx, bad, p); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := Run(cctx, nodes, p); err == nil {
		t.Fatal("expected cancellation")
	}
}
