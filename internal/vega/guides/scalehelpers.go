package guides

import (
	"context"
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
)

// Env is what the tick and entry generators need from the runtime. The
// vega-scale helpers they call (tickCount, tickValues, validTicks,
// tickFormat, labelValues, labelFormat, labelFraction) are package scale's.
type Env struct {
	// Locale supplies number and time formats; nil selects format.DefaultLocale.
	Locale *format.Locale
	// Warn receives upstream's dataflow warnings (may be nil).
	Warn func(msg string)
	// Context is the call's: listing ticks (a calendar walk) stops with its
	// error, and the label text is charged to the ledger it carries. Nil
	// means context.Background().
	Context context.Context
}

func (e *Env) warn(msg string) {
	if e != nil && e.Warn != nil {
		e.Warn(msg)
	}
}

func (e *Env) locale() *format.Locale {
	if e == nil || e.Locale == nil {
		return format.DefaultLocale()
	}
	return e.Locale
}

func (e *Env) ctx() context.Context {
	if e == nil || e.Context == nil {
		return context.Background()
	}
	return e.Context
}

// tickCount is vega-scale's tickCount, with the guide's bound: a numeric
// count past scale.MaxTicks (after the minimum step has lowered it) asks for
// more ticks or entries than a guide may list.
func (e *Env) tickCount(s scale.Scale, count, minStep jsval.Value) (scale.TickCount, error) {
	tc, err := scale.TickCountFor(s, count, minStep)
	if err != nil {
		return tc, err
	}
	if tc.HasN && math.Abs(tc.N) > scale.MaxTicks {
		return tc, fmt.Errorf("%w: tick count %v exceeds the limit of %d", budget.ErrLimit, tc.N, scale.MaxTicks)
	}
	return tc, nil
}

// labelMeter charges the text of the labels a guide makes to the ledger in
// the call's context. A label's length is chosen by the specification
// through its format (a width, a locale's numerals and separators, the
// patterns %c expands into), up to about a megabyte each, and there may be
// scale.MaxTicks of them; they live as long as the scene.
type labelMeter struct {
	l *budget.Ledger
	w jsval.Walker
}

func (e *Env) labelMeter() labelMeter {
	return labelMeter{budget.LedgerFrom(e.ctx()), jsval.WalkerFor(e.ctx())}
}

// charge charges the bytes of label, a string or an array of lines.
func (m labelMeter) charge(label jsval.Value) error {
	if m.l == nil {
		return nil
	}
	var n int
	switch {
	case label.IsStr():
		n = len(label.StrValue())
	case label.IsArr():
		var buf []byte
		for _, it := range label.Items() {
			if it.IsStr() {
				n += len(it.StrValue())
				continue
			}
			buf = m.w.AppendString(buf[:0], it)
			n += len(buf)
		}
	}
	return m.l.Alloc(int64(n))
}

func peekValue(v []jsval.Value) jsval.Value {
	if len(v) == 0 {
		return jsval.Undefined
	}
	return v[len(v)-1]
}

func domain0(d []jsval.Value) jsval.Value {
	if len(d) == 0 {
		return jsval.Undefined
	}
	return d[0]
}

// jsMax is Math.max over coerced numbers: NaN wins.
func jsMax(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	return math.Max(a, b)
}
