package format

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// TestCalendarAgainstUpstream replays testdata/calendar.jsonl.gz
// (testdata/gen_calendar.mjs): d3-time, d3-time-format and vega-time at
// instants that check two classes of the 2026-10-18 audit at every entry
// point of the package:
//   - C119: an instant is TimeClip'd as `new Date(+v)` makes it, so -0.5 is
//     1970 and 1.5 is 1 (formats, floors, ceils, offsets, counts, ranges,
//     vega-time's floors and day numbers);
//   - C120, C161: day and week numbers are interval counts, with d3's
//     minute-truncated offsets (Africa/Monrovia 1972, New York 1883), and
//     vega-time's UTC helpers read years 0 to 99 as 1900 plus them.
func TestCalendarAgainstUpstream(t *testing.T) {
	b, err := os.ReadFile("testdata/calendar.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		doc, err := jsval.ParseJSON(line)
		if err != nil {
			t.Fatal(err)
		}
		name := doc.Get("tz").StrValue()
		z := Local(mustLoc(t, name))
		tl := DefaultTimeLocale()
		fmts, floors := doc.Get("fmts").Items(), doc.Get("floors").Items()
		num := func(v jsval.Value) float64 {
			if v.IsStr() {
				return math.NaN()
			}
			return v.NumValue()
		}
		eq := func(what string, at, got float64, want jsval.Value) {
			t.Helper()
			if want.IsNull() {
				return
			}
			if w := num(want); got != w && !(math.IsNaN(got) && math.IsNaN(w)) {
				t.Errorf("%s %s(%v) = %v; upstream %v", name, what, at, got, w)
			}
		}
		for _, row := range doc.Get("rows").Items() {
			at := jsval.StringToNumber(row.Get("t").StrValue())
			for i, f := range fmts {
				spec := f.StrValue()
				if got, want := tl.Format(spec, z).Format(at), row.Get("fmt").Items()[i].StrValue(); got != want {
					t.Errorf("%s timeFormat(%q)(%v) = %q; d3 %q", name, spec, at, got, want)
				}
				if got, want := tl.Format(spec, UTC).Format(at), row.Get("utcFmt").Items()[i].StrValue(); got != want {
					t.Errorf("%s utcFormat(%q)(%v) = %q; d3 %q", name, spec, at, got, want)
				}
			}
			ms10, _ := UTC.Millisecond().Every(10)
			month3, _ := UTC.Month().Every(3)
			minute15, _ := z.Minute().Every(15)
			ivs := map[string]Interval{
				"second": z.Second(), "day": z.Day(), "week": z.Week(0), "month": z.Month(), "year": z.Year(),
				"utcSecond": UTC.Second(), "utcDay": UTC.Day(), "utcMonday": UTC.Week(1),
				"ms10": ms10, "month3": month3, "minute15": minute15,
			}
			spans := map[string]float64{"second": 5e3, "day": 3 * 864e5, "week": 3 * 6048e5, "month": 1e11, "year": 1e11,
				"utcSecond": 5e3, "utcDay": 3 * 864e5, "utcMonday": 3 * 6048e5, "ms10": 100, "month3": 1e11, "minute15": 864e5}
			for _, r := range row.Get("iv").Items() {
				it := r.Items()
				n := it[0].StrValue()
				iv := ivs[n]
				eq(n+".floor", at, iv.Floor(at), it[1])
				eq(n+".ceil", at, iv.Ceil(at), it[2])
				eq(n+".round", at, iv.Round(at), it[3])
				off, err := iv.Offset(ctx, at, 2)
				if err != nil {
					t.Fatal(err)
				}
				eq(n+".offset", at, off, it[4])
				if c, ok := iv.Count(at, at+86400000.7); ok {
					eq(n+".count", at, c, it[5])
				}
				rg, err := iv.Range(ctx, at, at+spans[n], 1)
				if err != nil {
					t.Fatal(err)
				}
				eq(n+".range length", at, float64(len(rg)), it[6])
			}
			vt := row.Get("vt").Items()
			for i, got := range []float64{z.DayOfYear(at), z.WeekOfYear(at), z.ISOWeekOfYear(at), UTC.DayOfYear(at), UTC.WeekOfYear(at), UTC.ISOWeekOfYear(at)} {
				if i < 3 && math.Abs(at) > 8.6e15 {
					// vega-time's localYear sets the fields of one shared
					// Date: after an Invalid Date it starts from the epoch's
					// local time, which in a zone behind UTC is 31 December,
					// so in the last representable year it overflows, or not,
					// depending on the call before. Here each call starts
					// afresh.
					continue
				}
				eq([]string{"dayofyear", "week", "isoweek", "utcdayofyear", "utcweek", "utcisoweek"}[i], at, got, vt[i])
			}
			for i, u := range floors {
				var units []string
				for _, x := range u.Items() {
					units = append(units, x.StrValue())
				}
				eq("timeFloor("+jsval.StringPrefix(u, 80)+")", at, NewFloor(z, units, 1).Floor(at), row.Get("floor").Items()[i])
				eq("utcFloor("+jsval.StringPrefix(u, 80)+")", at, NewFloor(UTC, units, 1).Floor(at), row.Get("utcFloor").Items()[i])
			}
			offs := row.Get("offset").Items()
			for i, c := range []struct {
				z    Zone
				unit string
				step float64
			}{{z, "day", 1}, {UTC, "second", -1}, {UTC, "quarter", 1}} {
				got, _, err := OffsetUnit(ctx, c.z, c.unit, at, c.step)
				if err != nil {
					t.Fatal(err)
				}
				eq("offset "+c.unit, at, got, offs[i])
			}
		}
	}
}
