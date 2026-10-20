package format

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/testutil"
)

// TestTimeLabelBoundedBeforeItIsBuilt is C124 (audit 2026-10-18): a guide's
// time label is as long as its specifier and the locale ask, 64 times the
// specifier with a %B of 128-byte month names, so it is built within the
// room the call's ledger has left and refused there, not made whole and
// charged after. The guard counts the bytes allocated: the label of a
// 1,000,000-byte specifier would be 64 MB.
func TestTimeLabelBoundedBeforeItIsBuilt(t *testing.T) {
	if testutil.RaceEnabled {
		t.Skip("allocation counts differ under -race")
	}
	def := DefaultTimeLocale().def
	def.Months = slicesRepeat(strings.Repeat("m", MaxLocaleName), 12)
	tl, err := NewTimeLocale(def)
	if err != nil {
		t.Fatal(err)
	}
	loc := &Locale{Number: DefaultNumberLocale(), Time: tl, Local: UTC}
	const room = 4 << 20
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: room})
	w := jsval.WalkerFor(budget.WithLedger(context.Background(), l))
	spec := jsval.Str(strings.Repeat("%B", 500_000))
	for _, mk := range []func() (TimeFormatter, error){
		func() (TimeFormatter, error) { return loc.TimeFormatSpec(w, spec, true) },
		func() (TimeFormatter, error) {
			o := jsval.NewObject(1)
			o.Set("year", spec)
			return loc.TimeMultiFormat(w, jsval.Obj(o), true)
		},
	} {
		f, err := mk()
		if err != nil {
			t.Fatal(err)
		}
		var ms0, ms1 runtime.MemStats
		runtime.ReadMemStats(&ms0)
		err = func() (err error) {
			defer func() {
				if s, ok := recover().(*budget.Stop); ok {
					err = s.Err
				}
			}()
			_ = f(0)
			return nil
		}()
		runtime.ReadMemStats(&ms1)
		if !errors.Is(err, budget.ErrLimit) {
			t.Errorf("a 64 MB label: %v; want a limit error", err)
		}
		// One buffer of the room and a pattern's length; nothing like 64 MB.
		if got := ms1.TotalAlloc - ms0.TotalAlloc; got > room+room/8 {
			t.Errorf("the refused label allocated %d bytes, more than the room (%d)", got, room)
		}
	}
	// The expression path is bounded the same way.
	if _, err := loc.TimeFormatValue(w, jsval.Num(0), spec, true); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("timeFormat value: %v; want a limit error", err)
	}
}

// TestManyLocalePatternsAllWritten is C166 (audit 2026-10-18): a specifier
// with more than 4,096 %c, %x or %X wrote nothing for the rest. A %c is now
// a step that formats the locale's pattern, read in place, so the specifier
// costs its own length, and every %c is written, as d3 writes it.
func TestManyLocalePatternsAllWritten(t *testing.T) {
	f := DefaultTimeLocale().Format(strings.Repeat("%c|", 5000), UTC)
	out := f.Format(0)
	if n := strings.Count(out, "1/1/1970, 12:00:00 AM|"); n != 5000 {
		t.Errorf("%d of 5000 %%c written", n)
	}
}

func slicesRepeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// A specifier past maxCompiledSpec is read from its text: the same steps as
// the compiled ones, so both write the same label.
func TestCompiledAndReadSpecifiersAgree(t *testing.T) {
	def := DefaultTimeLocale().def
	def.DateTime, def.Date, def.Time = "%a %e %b %Y %X", "%-m/%-d/%y", "%H:%M:%S %p %Z"
	l, err := NewTimeLocale(def)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []string{"%Y-%m-%dT%H:%M:%S.%LZ", "%c|%x|%X", "%G-W%V %g %j %U %W %u %w %q %Q %s %f", "%_d %-d %0e %%%", "ü%Bé%Ä%", "%"} {
		for _, z := range []Zone{UTC, Local(mustLoc(t, "America/New_York"))} {
			a := l.Format(spec, z)
			b := *a
			b.ops = nil
			for _, at := range []float64{0, -0.5, 1.6e12, -6.2e13, 8.64e15, math.NaN()} {
				if x, y := a.Format(at), b.Format(at); x != y {
					t.Errorf("%q at %v: compiled %q, read %q", spec, at, x, y)
				}
			}
		}
	}
}
