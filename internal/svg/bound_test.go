package svg

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// expansion is a scene whose document is large although it has a single item
// (or none): what the writer makes inside one item, or outside every item,
// from a value. Each grows its output from a much smaller input: a line break
// becomes a <tspan>, an & five bytes, a path command its numbers at full
// precision, a gradient stop or a clip path an element in <defs>.
type expansion struct {
	name  string
	scene func() *scene.Scenegraph
}

func oneItem(typ scene.MarkType, set func(it *scene.Item)) func() *scene.Scenegraph {
	return func() *scene.Scenegraph {
		sg := scene.New()
		m := sg.AddMark(scene.MarkDef{Type: typ, Role: "mark"}, nil, -1)
		set(m.AddItem())
		return sg
	}
}

func set(it *scene.Item, kv ...any) {
	for i := 0; i < len(kv); i += 2 {
		if _, err := it.Set(kv[i].(string), kv[i+1].(jsval.Value)); err != nil {
			panic(err)
		}
	}
}

func nums(n int, v float64) jsval.Value {
	a := make([]jsval.Value, n)
	for i := range a {
		a[i] = jsval.Num(v)
	}
	return jsval.Arr(a)
}

var expansions = []expansion{
	{"text lines", oneItem(scene.MarkText, func(it *scene.Item) {
		set(it, "text", jsval.Str(strings.Repeat("\n", 40_000)), "lineBreak", jsval.Str("\n"))
	})},
	{"text array", oneItem(scene.MarkText, func(it *scene.Item) {
		lines := make([]jsval.Value, 20_000)
		for i := range lines {
			lines[i] = jsval.Str("x")
		}
		set(it, "text", jsval.Arr(lines))
	})},
	{"escaped text", oneItem(scene.MarkText, func(it *scene.Item) {
		set(it, "text", jsval.Str(strings.Repeat("&", 200_000)))
	})},
	{"escaped attribute", oneItem(scene.MarkRect, func(it *scene.Item) {
		set(it, "description", jsval.Str(strings.Repeat("\"", 200_000)))
	})},
	{"path data", oneItem(scene.MarkPath, func(it *scene.Item) {
		set(it, "path", jsval.Str("M0,0"+strings.Repeat("L1,1", 100_000)))
	})},
	{"custom symbol", oneItem(scene.MarkSymbol, func(it *scene.Item) {
		// each 4-byte command is drawn as `L0.866,0.866`
		set(it, "shape", jsval.Str("M0,0"+strings.Repeat("l1 1", 30_000)), "size", jsval.Num(3))
	})},
	{"shape generator", oneItem(scene.MarkShape, func(it *scene.Item) {
		d := "M0,0" + strings.Repeat("L1,1", 100_000)
		it.Shape.Func = func(ctx scene.PathContext, _ *scene.Item) string { return d }
	})},
	{"line of many items", func() *scene.Scenegraph {
		sg := scene.New()
		m := sg.AddMark(scene.MarkDef{Type: scene.MarkLine, Role: "mark"}, nil, -1)
		for i := 0; i < 40_000; i++ {
			set(m.AddItem(), "x", jsval.Num(float64(i)+0.123), "y", jsval.Num(float64(i)+0.456))
		}
		return sg
	}},
	{"stroke dash", oneItem(scene.MarkRect, func(it *scene.Item) {
		set(it, "strokeDash", nums(100_000, 1.5))
	})},
	{"array position", oneItem(scene.MarkSymbol, func(it *scene.Item) {
		set(it, "x", nums(100_000, 1.5))
	})},
	{"gradient stops", oneItem(scene.MarkRect, func(it *scene.Item) {
		stops := make([]scene.GradientStop, 20_000)
		for i := range stops {
			stops[i] = scene.GradientStop{Offset: float64(i) / 20_000, Color: "#4c78a8"}
		}
		it.Fill = scene.GradientPaint(&scene.Gradient{Stops: stops})
	})},
	{"clip path", func() *scene.Scenegraph {
		sg := scene.New()
		d := "M0,0" + strings.Repeat("L1,1", 100_000)
		sg.AddMark(scene.MarkDef{Type: scene.MarkRect, Role: "mark", ClipPath: func(scene.PathContext) string { return d }}, nil, -1)
		return sg
	}},
	{"title caption", func() *scene.Scenegraph {
		sg := scene.New()
		m := sg.AddMark(scene.MarkDef{Type: scene.MarkText, Role: "title-text"}, nil, -1)
		set(m.AddItem(), "text", nums(50_000, 1.5))
		return sg
	}},
	{"marks without items", func() *scene.Scenegraph {
		sg := scene.New()
		for i := 0; i < 5_000; i++ {
			sg.AddMark(scene.MarkDef{Type: scene.MarkRule, Role: "mark", Name: "a-long-mark-name"}, nil, -1)
		}
		return sg
	}},
}

// TestMaxBytesBoundsEveryExpansion is C7 (audit 2026-10-18): the size bound
// is the writer's, so it holds inside an item, outside every item and after
// the last one. For each expansion the document is rendered whole, then with
// MaxBytes its exact size (it renders the same), one byte less (ErrTooLarge)
// and a small fraction of it, where the render fails with ErrTooLarge
// having held no more than the bound in its buffers: the document's, and the
// path scratch the generators draw into.
func TestMaxBytesBoundsEveryExpansion(t *testing.T) {
	const slack = 8 << 10 // the most one write can pass the bound by before it is checked
	for _, e := range expansions {
		t.Run(e.name, func(t *testing.T) {
			sg := e.scene()
			opt := Options{Width: 10, Height: 10}
			full, err := Render(context.Background(), sg, opt)
			if err != nil {
				t.Fatal(err)
			}
			opt.MaxBytes = len(full)
			if got, err := Render(context.Background(), sg, opt); err != nil || got != full {
				t.Errorf("MaxBytes at the document's size (%d): err %v, same document %v", len(full), err, got == full)
			}
			opt.MaxBytes = len(full) - 1
			if _, err := Render(context.Background(), sg, opt); !errors.Is(err, ErrTooLarge) {
				t.Errorf("MaxBytes one under the document's size: %v; want ErrTooLarge", err)
			}
			opt.MaxBytes = len(full) / 16
			r, err := render(context.Background(), nil, sg, opt)
			if !errors.Is(err, ErrTooLarge) {
				t.Fatalf("a %d-byte document under MaxBytes %d: %v; want ErrTooLarge", len(full), opt.MaxBytes, err)
			}
			if n := len(r.w.buf); n > opt.MaxBytes+slack {
				t.Errorf("the writer held %d bytes when it failed, past MaxBytes %d", n, opt.MaxBytes)
			}
			if n := r.sp.Len(); n > opt.MaxBytes+slack {
				t.Errorf("the path scratch held %d bytes when it failed, past MaxBytes %d", n, opt.MaxBytes)
			}
		})
	}
}

// countCtx counts the polls of its Err, and is cancelled from the first.
type countCtx struct {
	context.Context
	polls int
}

func (c *countCtx) Err() error {
	c.polls++
	return context.Canceled
}

// TestRenderPollsInsideAndBetweenItems is C7 and C24 (audit 2026-10-18): a
// cancelled render stops wherever its output grows, whether the bytes come
// from one item or from marks with none, and the polls follow the work: the
// same scene twice as large is polled about twice as often.
func TestRenderPollsInsideAndBetweenItems(t *testing.T) {
	for _, e := range expansions {
		t.Run(e.name, func(t *testing.T) {
			ctx := &countCtx{Context: context.Background()}
			if _, err := Render(ctx, e.scene(), Options{Width: 10, Height: 10}); !errors.Is(err, context.Canceled) {
				t.Errorf("render under a cancelled context: %v; want context.Canceled", err)
			}
		})
	}
	polls := func(n int) int {
		sg := scene.New()
		for i := 0; i < n; i++ {
			sg.AddMark(scene.MarkDef{Type: scene.MarkRule, Role: "mark"}, nil, -1)
		}
		ctx := &countingCtx{Context: context.Background()}
		if _, err := Render(ctx, sg, Options{Width: 10, Height: 10}); err != nil {
			t.Fatal(err)
		}
		return ctx.polls
	}
	a, b := polls(20_000), polls(40_000)
	if a < 20_000/2048 || 10*b < 18*a || 10*b > 22*a {
		t.Errorf("polls for 20,000 and 40,000 empty marks: %d and %d; want them in proportion, at least one per 2,048 marks", a, b)
	}
}

// countingCtx counts the polls of its Err and is never done.
type countingCtx struct {
	context.Context
	polls int
}

func (c *countingCtx) Err() error {
	c.polls++
	return nil
}

// TestOrderedMarksSortsInLinearithmicSteps is C24 (audit 2026-10-18): a
// group's child marks are put in z-index order with O(n log n) comparisons,
// as upstream's Array.prototype.sort does, and the order is stable.
func TestOrderedMarksSortsInLinearithmicSteps(t *testing.T) {
	comparisons := func(n int) int {
		ms := make([]*scene.Mark, n)
		for i := range ms {
			ms[i] = &scene.Mark{Zindex: float64(n - i/2)} // descending, in pairs of ties
		}
		c := 0
		out := orderedMarksBy(ms, func(a, b *scene.Mark) int {
			c++
			return compareZindex(a, b)
		})
		at := make(map[*scene.Mark]int, n)
		for i, m := range ms {
			at[m] = i
		}
		for i := 1; i < len(out); i++ {
			if out[i-1].Zindex > out[i].Zindex || out[i-1].Zindex == out[i].Zindex && at[out[i-1]] > at[out[i]] {
				t.Fatalf("n=%d: marks %d and %d out of order", n, i-1, i)
			}
		}
		return c
	}
	a, b := comparisons(4096), comparisons(8192)
	if b > 5*a/2 {
		t.Errorf("comparisons for 4,096 and 8,192 marks: %d and %d; a sort in O(n log n) at most about doubles them", a, b)
	}
}

// TestChunkedEscapingIsWhole: the writer escapes a long value a chunk at a
// time, and the text is the one escaping it whole gives, whatever characters
// (multi-byte, invalid, forbidden) fall on a cut.
func TestChunkedEscapingIsWhole(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	pieces := []string{"a", "&", "<", "\"", "\n", "\x01", "é", "€", "😀", "\xe2\x82", "\x82", "\xff", "￾", "\xed\xa0\x80"}
	for iter := 0; iter < 300; iter++ {
		var b strings.Builder
		for b.Len() < 3*escapeChunk+r.Intn(escapeChunk) {
			b.WriteString(pieces[r.Intn(len(pieces))])
		}
		s := b.String()
		for _, attr := range []bool{false, true} {
			var w writer
			w.reset(context.Background(), nil, 0)
			w.escaped(s, attr)
			if want := appendEscaped(nil, s, attr); string(w.bytes()) != string(want) {
				t.Fatalf("attr=%v: chunked escaping of %q differs from escaping it whole", attr, s)
			}
		}
	}
}

// TestArrayValuesWriteAsStringDoes: an array written element by element is
// String(array), nesting, nullish elements and the depth bound included.
func TestArrayValuesWriteAsStringDoes(t *testing.T) {
	deep := jsval.ArrOf(jsval.Num(1))
	for i := 0; i < jsval.MaxValueDepth+3; i++ {
		deep = jsval.ArrOf(deep, jsval.Str("<"), jsval.Num(2))
	}
	for _, v := range []jsval.Value{
		jsval.ArrOf(), jsval.ArrOf(jsval.Null), jsval.ArrOf(jsval.Undefined, jsval.Num(1.5), jsval.Str("a&b")),
		jsval.ArrOf(jsval.ArrOf(jsval.Num(1), jsval.ArrOf(jsval.Num(2), jsval.Null)), jsval.True, jsval.Obj(jsval.NewObject(0))),
		jsval.ArrOf(jsval.Num(math.NaN()), jsval.Num(math.Inf(-1)), jsval.Num(math.Copysign(0, -1)), jsval.Num(1e21)),
		deep,
	} {
		var w writer
		w.reset(context.Background(), nil, 0)
		w.valJS(v)
		if want := appendEscaped(nil, v.AsString(), true); string(w.bytes()) != string(want) {
			t.Errorf("valJS(%.80v) = %.80q; want %.80q", v, w.bytes(), want)
		}
	}
}
