package format

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

func TestCyclicTimeLocaleTerminates(t *testing.T) {
	def := DefaultTimeLocale().def
	def.DateTime, def.Date, def.Time = "%x", "%X", "a%cb"
	l, err := NewTimeLocale(def)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []string{"%c", "%x", "%X"} {
		_ = l.Format(spec, UTC).Format(0)
		if _, ok := l.Parse(spec, UTC).Parse("a"); ok {
			t.Errorf("cyclic parse %q succeeded", spec)
		}
	}
}

func TestLocaleValidation(t *testing.T) {
	v, _ := jsval.ParseJSONString(`{"dateTime":"%c"}`)
	if _, err := TimeLocaleFromValue(v); err == nil {
		t.Error("incomplete time locale accepted")
	}
	if _, err := TimeLocaleFromValue(jsval.Num(1)); err == nil {
		t.Error("non-object time locale accepted")
	}
	if _, err := NumberLocaleFromValue(jsval.Str("x")); err == nil {
		t.Error("non-object number locale accepted")
	}
	if _, err := NewLocale(jsval.Undefined, jsval.Null, UTC); err != nil {
		t.Errorf("default locale: %v", err)
	}
}

func TestFormatValueCType(t *testing.T) {
	f, err := DefaultNumberLocale().Format("c")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.FormatValue(jsval.Str("héllo")); got != "héllo" {
		t.Errorf("c format of string = %q", got)
	}
	if got := f.FormatValue(jsval.Num(1.5)); got != "1.5" {
		t.Errorf("c format of number = %q", got)
	}
	g, _ := DefaultNumberLocale().Format(",.1f")
	if got := g.FormatValue(jsval.Str(" 1234.56 ")); got != "1,234.6" {
		t.Errorf("string coerced with Number(): %q", got)
	}
	if got := g.FormatValue(jsval.Null); got != "0.0" {
		t.Errorf("null coerced to 0: %q", got)
	}
	if got := g.FormatValue(jsval.Undefined); got != "NaN" {
		t.Errorf("undefined: %q", got)
	}
}

func TestRangeBounds(t *testing.T) {
	ms := UTC.Millisecond()
	if _, err := ms.Range(context.Background(), 0, 1e13, 1); !errors.Is(err, ErrRangeTooLarge) {
		t.Errorf("huge range: %v", err)
	}
	if _, err := UTC.Ticks(context.Background(), 0, 1e15, 1e9); !errors.Is(err, ErrRangeTooLarge) {
		t.Errorf("huge ticks: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ms.Range(ctx, 0, 1e6, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled range: %v", err)
	}
	// Certainly too long: rejected before stepping (so before the canceled
	// context is consulted), not after millions of local-zone dates.
	day := Local(mustLoc(t, "Europe/Amsterdam")).Day()
	if _, err := day.Range(ctx, 1e12, 2.16e19, 1); !errors.Is(err, ErrRangeTooLarge) {
		t.Errorf("huge day range: %v", err)
	}
	if got, err := ms.Range(context.Background(), 0, MaxRangeLen, 1); err != nil || len(got) != MaxRangeLen {
		t.Errorf("range of exactly MaxRangeLen: %d dates, %v", len(got), err)
	}
	if r, err := ms.Range(context.Background(), 5, 1, 1); err != nil || len(r) != 0 {
		t.Errorf("empty range: %v %v", r, err)
	}
}

func TestHugeOffsetsTerminate(t *testing.T) {
	q, _ := IntervalFor(UTC, UnitQuarter)
	start := time.Now()
	for _, step := range []float64{1e9, -1e9, math.Inf(1), math.Inf(-1), 1e300} {
		if got := q.Offset(0, step); !math.IsNaN(got) {
			t.Errorf("quarter offset %v = %v, want NaN", step, got)
		}
	}
	w3, _ := UTC.Week(0).Every(3)
	if got := w3.Offset(0, 1e12); !math.IsNaN(got) {
		t.Errorf("week.every(3) offset huge = %v", got)
	}
	loc := Local(mustLoc(t, "America/New_York"))
	lw, _ := loc.Week(0).Every(1000000)
	lw.Floor(1.6e12)
	lw.Offset(1.6e12, 3)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestUTCFloorEarlyYearQuirk(t *testing.T) {
	// vega-time's utcDate reads d.y from a number for years 0..99 and yields an
	// Invalid Date; timeFloor is fine.
	t50 := UTC.FullYearDate(50, 5, 15, 0, 0, 0, 0)
	if math.IsNaN(t50) {
		t.Fatal("year 50 must be representable")
	}
	if got := NewFloor(UTC, []string{UnitYear}, 1).Floor(t50); !math.IsNaN(got) {
		t.Errorf("utcFloor(year 50) = %v, want NaN", got)
	}
	if got := NewFloor(Local(time.UTC), []string{UnitYear}, 1).Floor(t50); got != UTC.FullYearDate(50, 0, 1, 0, 0, 0, 0) {
		t.Errorf("timeFloor(year 50) = %v", got)
	}
}

func TestNormalizeUnitsErrors(t *testing.T) {
	for _, u := range [][]string{nil, {"bogus"}, {"week", "month"}, {"day", "date"}, {"dayofyear", "month"}, {"week", "dayofyear"}} {
		if _, err := NormalizeUnits(u); err == nil {
			t.Errorf("NormalizeUnits(%v) accepted", u)
		}
	}
	got, err := NormalizeUnits([]string{"hours", "year", "month"})
	if err != nil || !slicesEqual(got, []string{"year", "month", "hours"}) {
		t.Errorf("sort: %v %v", got, err)
	}
	// A null specifier for a unit removes it; upstream loops forever here.
	s, err := TimeUnitSpecifier([]string{"year"}, UnitSpecifiers{"year": nil})
	if err != nil || s != "" {
		t.Errorf("null specifier: %q %v", s, err)
	}
}

func TestMultiFormatErrors(t *testing.T) {
	l := DefaultLocale()
	if _, err := l.TimeMultiFormat(jsval.Num(5), true); err == nil {
		t.Error("numeric multi-format spec accepted")
	}
	f, err := l.TimeMultiFormat(jsval.Undefined, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := f(UTC.Date(2021, 0, 1, 0, 0, 0, 0)); got != "2021" {
		t.Errorf("year tick = %q", got)
	}
	if got := f(UTC.Date(2021, 2, 1, 0, 0, 0, 0)); got != "March" {
		t.Errorf("month tick = %q", got)
	}
}

func TestLocaleExpressionHelpers(t *testing.T) {
	l := DefaultLocale()
	if s, _ := l.FormatValue(jsval.Null, jsval.Str(",")); s != "null" {
		t.Errorf("format(null) = %q", s)
	}
	if s, err := l.FormatValue(jsval.Num(1234.5), jsval.Str("$,.2f")); err != nil || s != "$1,234.50" {
		t.Errorf("format = %q %v", s, err)
	}
	if _, err := l.FormatValue(jsval.Num(1), jsval.Undefined); err == nil {
		t.Error("format with undefined specifier accepted")
	}
	if s, _ := l.TimeFormatValue(jsval.Timestamp(0), jsval.Str("%Y-%m-%d"), true); s != "1970-01-01" {
		t.Errorf("timeFormat = %q", s)
	}
	if v := l.TimeParseValue(jsval.Str("2020-02-03"), jsval.Str("%Y-%m-%d"), true); !v.IsTimestamp() || v.NumValue() != 1580688000000 {
		t.Errorf("timeParse = %v", v)
	}
	if v := l.TimeParseValue(jsval.Str("nope"), jsval.Str("%Y-%m-%d"), true); !v.IsNull() {
		t.Errorf("failed parse = %v", v)
	}
}

func TestZoneBasics(t *testing.T) {
	ny := Local(mustLoc(t, "America/New_York"))
	// 2021-03-14 02:30 does not exist in New York: interpreted with the
	// pre-transition offset, so it reads 03:30 EDT.
	got := ny.Date(2021, 2, 14, 2, 30, 0, 0)
	if want := UTC.Date(2021, 2, 14, 7, 30, 0, 0); got != want {
		t.Errorf("gap time = %v, want %v", got, want)
	}
	// 2021-11-07 01:30 happens twice; the earlier (EDT) wins.
	got = ny.Date(2021, 10, 7, 1, 30, 0, 0)
	if want := UTC.Date(2021, 10, 7, 5, 30, 0, 0); got != want {
		t.Errorf("ambiguous time = %v, want %v", got, want)
	}
	if !UTC.IsUTC() || ny.IsUTC() || (Zone{}).Location() != time.UTC {
		t.Error("zone identity")
	}
	if got := UTC.Date(99, 0, 1, 0, 0, 0, 0); UTC.Fields(got).Year != 1999 {
		t.Errorf("Date(99) year = %d", UTC.Fields(got).Year)
	}
	if got := UTC.FullYearDate(99, 0, 1, 0, 0, 0, 0); UTC.Fields(got).Year != 99 {
		t.Errorf("FullYearDate(99) year = %d", UTC.Fields(got).Year)
	}
}

func TestRandomInputsDoNotPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	alphabet := []rune("0123456789-+:. /%%%abcdefjklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ()<>=^,~$#é \n∞😀")
	rs := func(n int) string {
		b := make([]rune, rng.Intn(n))
		for i := range b {
			b[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return string(b)
	}
	ny := Local(mustLoc(t, "America/New_York"))
	nl, tl := DefaultNumberLocale(), DefaultTimeLocale()
	specials := []float64{0, math.Copysign(0, -1), math.NaN(), math.Inf(1), math.Inf(-1), 1e21, -1e21, 5e-324, math.MaxFloat64, 8.64e15, -8.64e15, 8.64e15 + 1, 1e300, 0.5, 123456.789}
	iters := 4000
	if testing.Short() {
		iters = 500
	}
	for i := 0; i < iters; i++ {
		s := rs(24)
		ParseDate(s, UTC)
		ParseDate(s, ny)
		if f, err := nl.Format(s); err == nil {
			for _, v := range specials {
				f.Format(v)
			}
		}
		if _, err := ParseSpecifier(s); err == nil {
			_ = i
		}
		spec := rs(16)
		for _, z := range []Zone{UTC, ny} {
			p := tl.Parse(spec, z)
			p.Parse(s)
			f := tl.Format(spec, z)
			for _, v := range specials {
				f.Format(v)
			}
		}
		for _, v := range specials {
			for _, z := range []Zone{UTC, ny} {
				z.Fields(v)
				z.Date(v, v, v, v, v, v, v)
				for _, iv := range []Interval{z.Day(), z.Week(3), z.Month(), z.Year(), z.Hour()} {
					iv.Floor(v)
					iv.Ceil(v)
					iv.Offset(v, v)
					iv.Offset(0, v)
				}
				NewFloor(z, []string{UnitYear, UnitWeek, UnitDay}, v).Floor(v)
			}
		}
		if i%50 == 0 {
			Bin(specials[i%len(specials)], specials[(i/7)%len(specials)], specials[(i/3)%len(specials)])
			TickStep(specials[i%len(specials)], specials[(i/7)%len(specials)], specials[(i/3)%len(specials)])
			ISOFormat(specials[i%len(specials)])
		}
	}
}

func FuzzParseDate(f *testing.F) {
	for _, s := range []string{"2000-01-01", "Jan 1 2000 10:00 PM", "1/2/3", "2000-01-01T10:00:00+05:30", "(", "Z", "GMT+"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { ParseDate(s, UTC) })
}

func FuzzNumberFormat(f *testing.F) {
	for _, s := range []string{",.2f", "~s", "*^+#020,.3g", "", "(,.0%"} {
		f.Add(s, 1234.5678)
	}
	f.Fuzz(func(t *testing.T, spec string, v float64) {
		if nf, err := DefaultNumberLocale().Format(spec); err == nil {
			nf.Format(v)
			nf.Format(-v)
		}
	})
}

func FuzzTimeFormatParse(f *testing.F) {
	for _, s := range []string{"%Y-%m-%d %H:%M:%S %Z", "%c", "%-d/%_m/%0Y", "%G-W%V-%u", "%%%"} {
		f.Add(s, "2021-03-14 12:00:00 +0100", 1.6e12)
	}
	f.Fuzz(func(t *testing.T, spec, in string, v float64) {
		DefaultTimeLocale().Format(spec, UTC).Format(v)
		DefaultTimeLocale().Parse(spec, UTC).Parse(in)
	})
}

func BenchmarkTimeFormat(b *testing.B) {
	for _, spec := range []string{"%Y-%m-%d", "%b %d, %Y", "%Y-%m-%dT%H:%M:%S.%LZ", "%c", "%G-W%V"} {
		f := DefaultTimeLocale().Format(spec, UTC)
		b.Run(spec, func(b *testing.B) {
			b.ReportAllocs()
			var buf [64]byte
			for i := 0; i < b.N; i++ {
				_ = f.AppendFormat(buf[:0], 1.6e12+float64(i)*86400000)
			}
		})
	}
	ny := Local(time.UTC)
	f := DefaultTimeLocale().Format("%Y-%m-%d %H:%M", ny)
	b.Run("local", func(b *testing.B) {
		b.ReportAllocs()
		var buf [64]byte
		for i := 0; i < b.N; i++ {
			_ = f.AppendFormat(buf[:0], 1.6e12+float64(i)*86400000)
		}
	})
}

func BenchmarkTimeParse(b *testing.B) {
	p := DefaultTimeLocale().Parse("%Y-%m-%d %H:%M:%S", UTC)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p.Parse("2021-03-14 12:34:56")
	}
}

func BenchmarkParseDate(b *testing.B) {
	for _, s := range []string{"2021-03-14", "2021-03-14T12:34:56.789Z", "Jan 5 2021 10:00 PM"} {
		b.Run(s, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ParseDate(s, UTC)
			}
		})
	}
}

func BenchmarkFloorAndInterval(b *testing.B) {
	f := NewFloor(UTC, []string{UnitYear, UnitMonth}, 1)
	b.Run("floor-year-month", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			f.Floor(1.6e12 + float64(i)*3600000)
		}
	})
	day := UTC.Day()
	b.Run("day-floor", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			day.Floor(1.6e12 + float64(i))
		}
	})
	mon := UTC.Month()
	b.Run("month-range", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = mon.Range(context.Background(), 0, 1.6e12, 1)
		}
	})
	ny := Local(time.UTC).Day()
	b.Run("local-day-floor", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ny.Floor(1.6e12 + float64(i))
		}
	})
}

func BenchmarkMultiFormat(b *testing.B) {
	f, _ := DefaultLocale().TimeMultiFormat(jsval.Undefined, true)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f(1.6e12 + float64(i%1000)*86400000)
	}
}

func TestLocaleConcurrentUse(t *testing.T) {
	l := DefaultLocale()
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func(g int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 300; i++ {
				spec := []string{",.2f", "~s", "$,.0f", ".3g"}[(i+g)%4]
				f, err := l.NumberFormat(spec)
				if err != nil {
					t.Error(err)
					return
				}
				f.Format(float64(i))
				l.TimeFormat("%Y-%m-%d").Format(float64(i) * 86400000)
				l.UTCParse("%Y").Parse("2020")
				if ff, err := l.FormatFloat(spec); err == nil {
					ff(1.5)
				}
			}
		}(g)
	}
	for g := 0; g < 8; g++ {
		<-done
	}
}
