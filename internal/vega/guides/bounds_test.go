package guides

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
	env := &Env{Locale: format.DefaultLocale()}
	start := time.Now()
	_, err := env.AxisTicks(AxisTicksInput{
		Scale: utcScale(t, 0, 3e12),
		Count: objOf("interval", jsval.Str("minutes"), "step", jsval.Num(2)),
	})
	if !errors.Is(err, budget.ErrLimit) {
		t.Errorf("AxisTicks = %v; want a limit error", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v", d)
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
	_, err = env.LegendEntries(LegendEntriesInput{Scale: lin, Count: jsval.Num(5), Type: "gradient", FormatSpecifier: jsval.Str(">300000")})
	if !errors.Is(err, budget.ErrLimit) {
		t.Errorf("legend labels: %v; want a limit error", err)
	}
}
