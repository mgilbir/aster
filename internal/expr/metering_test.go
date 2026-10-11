package expr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// The class guard of ADR 0010 for the expression library: every function of
// the table, called with hostile arguments a specification can make (strings
// and arrays of millions of elements, an array-like object of length 3e9,
// counts of 1e9), must
//
//   - return soon after the call's deadline, however long the work it was
//     asked for: its loops meter their steps;
//   - allocate within a small multiple of what it charged to the string
//     budget and the ledger: what a specification sizes is charged before it
//     is made;
//   - report an engine limit or the deadline as such, never as a JavaScript
//     exception an expression could catch (C43).
//
// A function added to the table is covered without being named here.

type hostileEnv map[string]jsval.Value

func (e hostileEnv) Signal(name string) (jsval.Value, bool) { v, ok := e[name]; return v, ok }

// hostileValues are the arguments, by signal name: each holds more than
// room, the string budget the calls run under, by a factor k (1.5 by default;
// ASTER_METERING_FULL runs a 1 MiB budget with arguments four times larger
// and the full matrix of argument lists).
func hostileValues(room int, k float64) hostileEnv {
	b := int(k * float64(room))
	nums := make([]jsval.Value, b/16)
	for i := range nums {
		nums[i] = jsval.Num(float64(i%977) + 0.123456789) // 16 bytes as a string
	}
	strs := make([]jsval.Value, b/40)
	for i := range strs {
		strs[i] = jsval.Str(fmt.Sprintf("%040d", i))
	}
	arrayLike := jsval.NewObject(1)
	arrayLike.Set("length", jsval.Num(3e9))
	return hostileEnv{
		"ascii":  jsval.Str(strings.Repeat("a", b)),
		"latin":  jsval.Str(strings.Repeat("é", b/2)),
		"emoji":  jsval.Str(strings.Repeat("\U0001F600", b/4)),
		"nums":   jsval.Arr(nums),
		"strs":   jsval.Arr(strs),
		"alike":  jsval.Obj(arrayLike),
		"huge":   jsval.Num(1e9),
		"big":    jsval.Num(4e6),
		"one":    jsval.Str("a"),
		"unit":   jsval.Str("milliseconds"),
		"needle": jsval.Str(strings.Repeat("a", b/6) + "b"),
	}
}

// hostileCalls are the argument lists every function is called with.
func hostileCalls(short bool) [][]string {
	first := []string{"ascii", "latin", "emoji", "nums", "strs", "alike", "huge", "one", "unit"}
	second := []string{"ascii", "emoji", "nums", "alike", "huge", "big", "one", "needle"}
	third := []string{"huge", "one", "ascii"}
	if short {
		second = []string{"ascii", "nums", "huge", "needle"}
		third = []string{"huge"}
	}
	var calls [][]string
	for _, a := range first {
		calls = append(calls, []string{a})
		for _, b := range second {
			calls = append(calls, []string{a, b})
		}
	}
	for _, a := range []string{"ascii", "nums", "unit"} {
		for _, b := range second {
			for _, c := range third {
				calls = append(calls, []string{a, b, c}, []string{a, "huge", b, c})
			}
		}
	}
	return calls
}

// meteringExempt are the functions whose cost belongs to another package's
// bound, and slowExempt those whose deadline does: goecma262 compiles and
// matches with no way to charge or interrupt it (a search of a long string
// runs to its step budget, 100 steps a byte, about a second for 6 MB).
var meteringExempt = map[string]string{
	// Number and time formats are package format's, and their caches.
	"format": "format", "timeFormat": "format", "utcFormat": "format", "timeParse": "format", "utcParse": "format",
	"monthFormat": "format", "monthAbbrevFormat": "format", "dayFormat": "format", "dayAbbrevFormat": "format",
	"timeUnitSpecifier": "format",
	// A pattern compiled from a hostile string: goecma262 builds up to its
	// program cap (about 50 MB) before refusing it, with no hook to charge it
	// first.
	"regexp": "goecma262", "test": "goecma262",
}

var slowExempt = map[string]bool{"regexp": true, "test": true}

func TestEveryBuiltinIsMetered(t *testing.T) {
	full := os.Getenv("ASTER_METERING_FULL") != ""
	stringRoom, allocSlack, k := 256<<10, int64(512<<10), 1.5
	if full {
		stringRoom, allocSlack, k = 1<<20, 2<<20, 6
	}
	env := hostileValues(stringRoom, k)
	names := make([]string, 0, len(funcTable))
	for name, d := range funcTable {
		if d.math1 == nil && d.math2 == nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	const (
		deadline = 20 * time.Millisecond
		slack    = 450 * time.Millisecond
		// The budgets charge what is kept (a string, an array); making it
		// also makes garbage (the string of each element of a joined array,
		// a buffer outgrown), a few times as much at most.
		allocRatio = 8
	)
	for _, name := range names {
		for _, call := range hostileCalls(!full) {
			src := name + "(" + strings.Join(call, ", ") + ")"
			p, err := Compile(src)
			if err != nil {
				continue // the call is a compile error (too few arguments, a literal required)
			}
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			led := budget.NewLedger(ctx, budget.LedgerLimits{MaxBytes: 64 << 20})
			s := NewScope(env)
			s.Context = budget.WithLedger(ctx, led)
			s.Strings = NewStringBudget(int64(stringRoom))
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			_, err = evalOrPanic(p, s)
			took := time.Since(start)
			runtime.ReadMemStats(&after)
			cancel()
			if took > deadline+slack && !slowExempt[name] {
				t.Errorf("%s: took %v under a %v deadline (err %.80v)", src, took, deadline, err)
			}
			var je *Error
			if errors.As(err, &je) && (strings.Contains(je.Msg, "context") || strings.Contains(je.Msg, "limit")) {
				t.Errorf("%s: an engine limit or the deadline as a JavaScript exception: %v", src, err)
			}
			if _, ok := meteringExempt[name]; ok {
				continue
			}
			charged := int64(stringRoom) - s.Strings.left + led.PeakBytes()
			if got := int64(after.TotalAlloc - before.TotalAlloc); got > allocRatio*charged+allocSlack {
				t.Errorf("%s: allocated %d KiB, charged %d KiB (err %.80v)", src, got>>10, charged>>10, err)
			}
		}
	}
}

// evalOrPanic is Eval, reporting a panic as an error so that one bad call
// does not end the test.
func evalOrPanic(p *Program, s *Scope) (v jsval.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return p.Eval(s)
}
