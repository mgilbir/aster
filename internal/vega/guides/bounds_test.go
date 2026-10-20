package guides

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
)

func utcScale(t *testing.T, lo, hi float64) scale.Scale {
	t.Helper()
	s, _ := scale.New(scale.TypeUTC)
	s.SetDomain([]jsval.Value{jsval.Num(lo), jsval.Num(hi)})
	s.SetRange([]jsval.Value{jsval.Num(0), jsval.Num(200)})
	return s
}

func objOf(kv ...any) jsval.Value {
	o := jsval.NewObject(2)
	for i := 0; i < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1].(jsval.Value))
	}
	return jsval.Obj(o)
}

// TestTimeTicksPastTheBoundFail is C53: ticks every two minutes over 95
// years are refused with a limit error, at once, where they were dropped and
// the axis drawn without ticks.
func TestTimeTicksPastTheBoundFail(t *testing.T) {
	// The walk is counted, not timed: the ledger's work is the dates the
	// calendar walk visits, refused before it passes the guide's bound
	// (it walked 4,194,304 dates, format.MaxRangeLen, before it failed).
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{})
	env := &Env{Locale: format.DefaultLocale(), Context: budget.WithLedger(context.Background(), l)}
	_, err := env.AxisTicks(AxisTicksInput{
		Scale: utcScale(t, 0, 3e12),
		Count: objOf("interval", jsval.Str("minutes"), "step", jsval.Num(2)),
	})
	if !errors.Is(err, budget.ErrLimit) {
		t.Errorf("AxisTicks = %v; want a limit error", err)
	}
	// a filtered interval is walked to the bound (one unit a date), a plain
	// one refused before it starts
	for name, count := range map[string]jsval.Value{
		"every 2 minutes": objOf("interval", jsval.Str("minutes"), "step", jsval.Num(2)),
		"minutes":         jsval.Str("minutes"),
	} {
		l := budget.NewLedger(context.Background(), budget.LedgerLimits{})
		env.Context = budget.WithLedger(context.Background(), l)
		if _, err := env.AxisTicks(AxisTicksInput{Scale: utcScale(t, 0, 3e12), Count: count}); !errors.Is(err, budget.ErrLimit) {
			t.Errorf("%s: %v; want a limit error", name, err)
		}
		if w := l.WorkDone(); w > 4*scale.MaxTicks {
			t.Errorf("%s: walked %d units of work before refusing, more than the guide's bound allows", name, w)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env.Context = ctx
	if _, err := env.AxisTicks(AxisTicksInput{Scale: utcScale(t, 0, 3e12), Count: jsval.Str("day")}); !errors.Is(err, context.Canceled) {
		t.Errorf("AxisTicks after the call ended = %v; want its error", err)
	}
}

// TestGuideTickCountFollowsUpstream is C101: the guides use vega-scale's one
// tickCount, so the counts the old copy refused or misread behave as
// upstream's (node: an unknown unit and every("0") are the default count,
// and true is the count 1).
func TestGuideTickCountFollowsUpstream(t *testing.T) {
	env := &Env{Locale: format.DefaultLocale()}
	day := 864e5
	for name, count := range map[string]jsval.Value{
		"unknown unit": jsval.Str("fortnight"),
		"step '0'":     objOf("interval", jsval.Str("day"), "step", jsval.Str("0")),
		"step -2":      objOf("interval", jsval.Str("day"), "step", jsval.Num(-2)),
	} {
		got, err := env.AxisTicks(AxisTicksInput{Scale: utcScale(t, 0, 10*day), Count: count})
		if err != nil || len(got) != 11 { // the default of ten ticks: the eleven midnights
			t.Errorf("%s: %d ticks, %v; want the default count's 11", name, len(got), err)
		}
	}
	lin, _ := scale.New(scale.TypeLinear)
	lin.SetDomain([]jsval.Value{jsval.Num(0), jsval.Num(10)})
	lin.SetRange([]jsval.Value{jsval.Num(0), jsval.Num(100)})
	got, err := env.AxisTicks(AxisTicksInput{Scale: lin, Count: jsval.True})
	if err != nil || len(got) != 2 { // ticks(0, 10, 1): 0 and 10
		t.Errorf("count true: %d ticks, %v; want 2", len(got), err)
	}
}

// TestLabelsChargedToTheLedger is C9: the text of the labels is charged to
// the call's ledger, so labels a format makes long cannot outgrow the memory
// limit.
func TestLabelsChargedToTheLedger(t *testing.T) {
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 1 << 20})
	env := &Env{Locale: format.DefaultLocale(), Context: budget.WithLedger(context.Background(), l)}
	lin, _ := scale.New(scale.TypeLinear)
	lin.SetDomain([]jsval.Value{jsval.Num(0), jsval.Num(100)})
	lin.SetRange([]jsval.Value{jsval.Num(0), jsval.Num(100)})
	_, err := env.AxisTicks(AxisTicksInput{Scale: lin, Count: jsval.Num(20), FormatSpecifier: jsval.Str(">60000")})
	if !errors.Is(err, budget.ErrLimit) {
		t.Errorf("21 labels of 60000 bytes under a 1 MiB ledger: %v; want a limit error", err)
	}
	l = budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 1 << 20})
	env.Context = budget.WithLedger(context.Background(), l)
	if _, err := env.AxisTicks(AxisTicksInput{Scale: lin, Count: jsval.Num(10)}); err != nil {
		t.Fatal(err)
	}
	if b := l.Bytes(); b != int64(len(strings.Join([]string{"0", "10", "20", "30", "40", "50", "60", "70", "80", "90", "100"}, ""))) {
		t.Errorf("ledger charged %d bytes for the labels 0..100", b)
	}
	_, err = env.LegendEntries(LegendEntriesInput{Scale: lin, Count: jsval.Num(5), Type: jsval.Str("gradient"), FormatSpecifier: jsval.Str(">300000")})
	if !errors.Is(err, budget.ErrLimit) {
		t.Errorf("legend labels: %v; want a limit error", err)
	}
}

// TestExplicitValuesPastTheTickBoundFail is C97 (audit 2026-10-18): explicit
// axis values are the ticks the axis lists unless a positive count thins
// them, so more than scale.MaxTicks of them are refused as legend entries
// are, whatever the count says (zero, negative, NaN, a calendar interval),
// instead of becoming datums, labels and marks.
func TestExplicitValuesPastTheTickBoundFail(t *testing.T) {
	lin, _ := scale.New(scale.TypeLinear)
	lin.SetDomain([]jsval.Value{jsval.Num(0), jsval.Num(1)})
	lin.SetRange([]jsval.Value{jsval.Num(0), jsval.Num(100)})
	values := func(n int) jsval.Value {
		vs := make([]jsval.Value, n)
		for i := range vs {
			vs[i] = jsval.Num(float64(i) / float64(n))
		}
		return jsval.Arr(vs)
	}
	env := &Env{Locale: format.DefaultLocale()}
	utc := utcScale(t, 0, 1)
	for _, c := range []struct {
		name  string
		s     scale.Scale
		count jsval.Value
	}{
		{"0", lin, jsval.Num(0)}, {"-1", lin, jsval.Num(-1)}, {"NaN", lin, jsval.Num(math.NaN())}, {"false", lin, jsval.False},
		{"interval", utc, jsval.Str("day")}, {"{interval}", utc, objOf("interval", jsval.Str("day"))},
	} {
		if _, err := env.AxisTicks(AxisTicksInput{Scale: c.s, Count: c.count, Values: values(scale.MaxTicks + 1)}); !errors.Is(err, budget.ErrLimit) {
			t.Errorf("count %s, %d values: %v; want a limit error", c.name, scale.MaxTicks+1, err)
		}
		// fewer are listed
		if got, err := env.AxisTicks(AxisTicksInput{Scale: c.s, Count: c.count, Values: values(1000)}); err != nil || len(got) != 1000 {
			t.Errorf("count %s, 1000 values: %d ticks, %v", c.name, len(got), err)
		}
	}
	if got, err := env.AxisTicks(AxisTicksInput{Scale: lin, Count: jsval.Num(10), Values: values(scale.MaxTicks + 1)}); err != nil || len(got) > 10 {
		t.Errorf("count 10 over %d values: %d ticks, %v; want them thinned to at most 10", scale.MaxTicks+1, len(got), err)
	}
	// values outside the range are not listed, so they do not count
	out := values(scale.MaxTicks + 1).Items()
	out = append(out, jsval.Num(0.5))
	for i := range out[:len(out)-1] {
		out[i] = jsval.Num(2 + float64(i))
	}
	if got, err := env.AxisTicks(AxisTicksInput{Scale: lin, Count: jsval.Num(0), Values: jsval.Arr(out)}); err != nil || len(got) != 1 {
		t.Errorf("one of %d values in range: %d ticks, %v", len(out), len(got), err)
	}
}
