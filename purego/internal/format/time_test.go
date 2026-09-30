package format

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// nf is a JSON number that may be null (NaN).
type nf float64

func (n *nf) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*n = nf(math.NaN())
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*n = nf(f)
	return nil
}

func same(a, b float64) bool { return a == b || (math.IsNaN(a) && math.IsNaN(b)) }

func floats(in []nf) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}

type intervalRec struct {
	Name     string `json:"name"`
	UTC      bool   `json:"utc"`
	Null     bool   `json:"null"`
	HasCount bool   `json:"hasCount"`
	HasEvery bool   `json:"hasEvery"`
	Floor    []nf   `json:"floor"`
	Ceil     []nf   `json:"ceil"`
	Round    []nf   `json:"round"`
	Off1     []nf   `json:"off1"`
	Offm1    []nf   `json:"offm1"`
	Off5     []nf   `json:"off5"`
	Offm13   []nf   `json:"offm13"`
	Off0     []nf   `json:"off0"`
	Count    []nf   `json:"count"`
	Ranges   []struct {
		Start, Stop float64
		Step        float64
		Out         []float64
	} `json:"ranges"`
}

type timeGolden struct {
	Instants  []nf `json:"instants"`
	Intervals []intervalRec
	TickCases []struct {
		Start, Stop, Count float64
		UTC                bool
		Out                []float64
	} `json:"tickCases"`
	TickStepCases []struct {
		A, B nf
		C    float64
		Out  string
	} `json:"tickStepCases"`
	Locales map[string]json.RawMessage `json:"locales"`
	Formats []struct {
		Locale string
		UTC    bool
		Spec   string
		Out    []string
	} `json:"formats"`
	Days       []float64 `json:"days"`
	DayFormats []struct {
		Spec string
		UTC  bool
		Out  []string
	} `json:"dayFormats"`
	Parses []struct {
		Locale string
		UTC    bool
		Spec   string
		Cases  [][2]string
	} `json:"parses"`
	DateParse     [][2]json.RawMessage `json:"dateParse"`
	FloorInstants []nf                 `json:"floorInstants"`
	Floors        []struct {
		UTC   bool
		Units []string
		Step  *float64
		Out   []nf
	} `json:"floors"`
	EarlyFloors []struct {
		UTC   bool
		Units []string
		Out   []nf
		Ins   []float64
	} `json:"earlyFloors"`
	Offsets []struct {
		UTC  bool
		Unit string
		Step float64
		Out  []json.RawMessage
	} `json:"offsets"`
	Sequences []struct {
		UTC         bool
		Unit        string
		Start, Stop float64
		Step        float64
		Out         json.RawMessage
	} `json:"sequences"`
	Specs []struct {
		Units []string
		Over  map[string]*string
		Out   json.RawMessage
	} `json:"specs"`
	Bins []struct {
		Start, Stop nf
		Maxbins     *float64
		Units       []string
		Step        nf
	} `json:"bins"`
	Doy      []doyRec `json:"doy"`
	DoyEarly []doyRec `json:"doyEarly"`
	Detect   []struct {
		Set []nf
		UTC bool
		Out struct {
			Units []string
			Step  float64
			Err   string
		}
	} `json:"detect"`
	Multi []struct {
		UTC  bool
		Spec json.RawMessage
		Out  []string
	} `json:"multi"`
	MfInstants  []nf     `json:"mfInstants"`
	FfVals      []string `json:"ffVals"`
	FormatFloat []struct {
		Spec string
		Out  []string
	} `json:"formatFloat"`
	FormatSpan []struct {
		A, B, C float64
		Spec    json.RawMessage
		Out     json.RawMessage
	} `json:"formatSpan"`
	IsoFmt   []string `json:"isoFmt"`
	IsoParse [][2]string
	Extremes []struct {
		T   float64
		Iso string
		Y   nf
	}
	Datetime []struct {
		Args  []nf
		Local nf
		UTC   nf
	}
}

type doyRec struct {
	T     nf
	Doy   nf
	Week  nf
	Udoy  nf
	Uweek nf
}

func loadTimeGolden(t testing.TB, file string) *timeGolden {
	var g timeGolden
	readGolden(t, "time_"+file+".json.gz", &g)
	return &g
}

func zoneFor(t testing.TB, name string, utc bool) Zone {
	if utc {
		return UTC
	}
	return Local(mustLoc(t, name))
}

// intervalFromName parses the generator's "base.every(n).every(m)" names.
func intervalFromName(z Zone, name string) (Interval, bool) {
	parts := strings.Split(name, ".every(")
	var iv Interval
	switch b := parts[0]; {
	case b == "millisecond":
		iv = z.Millisecond()
	case b == "second":
		iv = z.Second()
	case b == "minute":
		iv = z.Minute()
	case b == "hour":
		iv = z.Hour()
	case b == "day":
		iv = z.Day()
	case b == "unixDay":
		iv = z.UnixDay()
	case strings.HasPrefix(b, "week"):
		iv = z.Week(int(b[4] - '0'))
	case b == "month":
		iv = z.Month()
	case b == "year":
		iv = z.Year()
	}
	for _, p := range parts[1:] {
		n, _ := strconv.ParseFloat(strings.TrimSuffix(p, ")"), 64)
		var ok bool
		if iv, ok = iv.Every(n); !ok {
			return Interval{}, false
		}
	}
	return iv, true
}

func TestTimeGolden(t *testing.T) {
	for _, gz := range goldenZones {
		t.Run(gz.file, func(t *testing.T) {
			g := loadTimeGolden(t, gz.file)
			t.Run("intervals", func(t *testing.T) { testIntervals(t, g, gz.name) })
			t.Run("ticks", func(t *testing.T) { testTicks(t, g, gz.name) })
			t.Run("format", func(t *testing.T) { testFormats(t, g, gz.name) })
			t.Run("parse", func(t *testing.T) { testParses(t, g, gz.name) })
			t.Run("dateparse", func(t *testing.T) { testDateParse(t, g, gz.name) })
			t.Run("vegatime", func(t *testing.T) { testVegaTime(t, g, gz.name) })
			t.Run("vegaformat", func(t *testing.T) { testVegaFormat(t, g, gz.name) })
			t.Run("datetime", func(t *testing.T) { testDatetime(t, g, gz.name) })
		})
	}
}

func testIntervals(t *testing.T, g *timeGolden, zoneName string) {
	ins := floats(g.Instants)
	fails := 0
	report := func(format string, args ...any) {
		if fails++; fails <= 25 {
			t.Errorf(format, args...)
		}
	}
	for _, rec := range g.Intervals {
		z := zoneFor(t, zoneName, rec.UTC)
		iv, ok := intervalFromName(z, rec.Name)
		if rec.Null {
			if ok {
				report("%s utc=%v: every should be null", rec.Name, rec.UTC)
			}
			continue
		}
		if !ok {
			report("%s utc=%v: no interval", rec.Name, rec.UTC)
			continue
		}
		if _, has := iv.Count(0, 0); has != rec.HasCount {
			report("%s utc=%v: hasCount = %v, want %v", rec.Name, rec.UTC, has, rec.HasCount)
		}
		if _, has := iv.Every(2); has != rec.HasEvery && rec.HasEvery {
			report("%s utc=%v: hasEvery = %v, want %v", rec.Name, rec.UTC, has, rec.HasEvery)
		}
		ops := []struct {
			name string
			want []nf
			f    func(float64) float64
		}{
			{"floor", rec.Floor, iv.Floor},
			{"ceil", rec.Ceil, iv.Ceil},
			{"round", rec.Round, iv.Round},
			{"offset1", rec.Off1, func(v float64) float64 { return iv.Offset(v, 1) }},
			{"offset-1", rec.Offm1, func(v float64) float64 { return iv.Offset(v, -1) }},
			{"offset5", rec.Off5, func(v float64) float64 { return iv.Offset(v, 5) }},
			{"offset-13", rec.Offm13, func(v float64) float64 { return iv.Offset(v, -13) }},
			{"offset0", rec.Off0, func(v float64) float64 { return iv.Offset(v, 0) }},
		}
		for _, op := range ops {
			for i, v := range ins {
				if got := op.f(v); !same(got, float64(op.want[i])) {
					report("%s utc=%v %s(%v) = %v, want %v", rec.Name, rec.UTC, op.name, v, got, float64(op.want[i]))
				}
			}
		}
		if rec.HasCount {
			for i, v := range ins {
				got, _ := iv.Count(g.pivot(), v)
				if !same(got, float64(rec.Count[i])) {
					report("%s utc=%v count(pivot,%v) = %v, want %v", rec.Name, rec.UTC, v, got, float64(rec.Count[i]))
				}
			}
		}
		for _, r := range rec.Ranges {
			got, err := iv.Range(context.Background(), r.Start, r.Stop, r.Step)
			if err != nil {
				t.Fatal(err)
			}
			if !equalSlices(got, r.Out) {
				report("%s utc=%v range(%v,%v,%v): len %d vs %d, got %v want %v", rec.Name, rec.UTC, r.Start, r.Stop, r.Step, len(got), len(r.Out), head(got), head(r.Out))
			}
		}
	}
}

func (g *timeGolden) pivot() float64 { return UTC.Date(1999, 5, 15, 3, 0, 0, 0) }

func head(a []float64) []float64 { return a[:min(len(a), 6)] }

func equalSlices(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !same(a[i], b[i]) {
			return false
		}
	}
	return true
}

func testTicks(t *testing.T, g *timeGolden, zoneName string) {
	for _, c := range g.TickCases {
		z := zoneFor(t, zoneName, c.UTC)
		got, err := z.Ticks(context.Background(), c.Start, c.Stop, c.Count)
		if err != nil {
			t.Fatal(err)
		}
		if !equalSlices(got, c.Out) {
			t.Errorf("ticks(%v,%v,%v) utc=%v: got %v (%d) want %v (%d)", c.Start, c.Stop, c.Count, c.UTC, head(got), len(got), head(c.Out), len(c.Out))
		}
	}
	for _, c := range g.TickStepCases {
		want := parseJS(t, c.Out)
		if got := TickStep(float64(c.A), float64(c.B), c.C); !same(got, want) {
			t.Errorf("tickStep(%v,%v,%v) = %v, want %v", float64(c.A), float64(c.B), c.C, got, want)
		}
	}
}

func timeLocalesFrom(t testing.TB, g *timeGolden) map[string]*TimeLocale {
	out := map[string]*TimeLocale{"default": DefaultTimeLocale()}
	for name, raw := range g.Locales {
		if string(raw) == "null" {
			continue
		}
		v, err := jsval.ParseJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		l, err := TimeLocaleFromValue(v)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = l
	}
	return out
}

func testFormats(t *testing.T, g *timeGolden, zoneName string) {
	locs := timeLocalesFrom(t, g)
	ins := floats(g.Instants)
	fails := 0
	for _, c := range g.Formats {
		f := locs[c.Locale].Format(c.Spec, zoneFor(t, zoneName, c.UTC))
		for i, v := range ins {
			if got := f.Format(v); got != c.Out[i] {
				if fails++; fails <= 30 {
					t.Errorf("%s utc=%v format(%q)(%v) = %q, want %q", c.Locale, c.UTC, c.Spec, v, got, c.Out[i])
				}
			}
		}
	}
	for _, c := range g.DayFormats {
		f := DefaultTimeLocale().Format(c.Spec, zoneFor(t, zoneName, c.UTC))
		for i, v := range g.Days {
			if got := f.Format(v); got != c.Out[i] {
				if fails++; fails <= 30 {
					t.Errorf("utc=%v format(%q)(%v) = %q, want %q", c.UTC, c.Spec, v, got, c.Out[i])
				}
			}
		}
	}
}

func parseResult(t testing.TB, s string) (float64, bool) {
	switch s {
	case "null":
		return 0, false
	case "NaN":
		return math.NaN(), true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("bad result %q", s)
	}
	return f, true
}

func testParses(t *testing.T, g *timeGolden, zoneName string) {
	locs := timeLocalesFrom(t, g)
	fails := 0
	for _, c := range g.Parses {
		p := locs[c.Locale].Parse(c.Spec, zoneFor(t, zoneName, c.UTC))
		for _, cs := range c.Cases {
			want, wantOK := parseResult(t, cs[1])
			got, ok := p.Parse(cs[0])
			if ok != wantOK || (ok && !same(got, want)) {
				if fails++; fails <= 30 {
					t.Errorf("%s utc=%v parse(%q)(%q) = %v,%v want %v,%v", c.Locale, c.UTC, c.Spec, cs[0], got, ok, want, wantOK)
				}
			}
		}
	}
}

func testDateParse(t *testing.T, g *timeGolden, zoneName string) {
	z := Local(mustLoc(t, zoneName))
	fails := 0
	for _, c := range g.DateParse {
		var s string
		var want nf
		if err := json.Unmarshal(c[0], &s); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(c[1], &want); err != nil {
			t.Fatal(err)
		}
		if got := ParseDate(s, z); !same(got, float64(want)) {
			if fails++; fails <= 30 {
				t.Errorf("ParseDate(%q) = %v, want %v", s, got, float64(want))
			}
		}
	}
}

func testDatetime(t *testing.T, g *timeGolden, zoneName string) {
	z := Local(mustLoc(t, zoneName))
	for _, c := range g.Datetime {
		args := floats(c.Args)
		if got := z.DatetimeArgs(args...); !same(got, float64(c.Local)) {
			t.Errorf("new Date(%v) = %v, want %v", args, got, float64(c.Local))
		}
		if got := UTCArgs(args...); !same(got, float64(c.UTC)) {
			t.Errorf("Date.UTC(%v) = %v, want %v", args, got, float64(c.UTC))
		}
	}
	for _, e := range g.Extremes {
		s, ok := ISOFormat(e.T)
		if (s == "" && ok) || (!ok && e.Iso != "ERR") || (ok && s != e.Iso) {
			t.Errorf("ISOFormat(%v) = %q,%v want %q", e.T, s, ok, e.Iso)
		}
	}
	for i, want := range g.IsoFmt {
		s, ok := ISOFormat(float64(g.Instants[i]))
		if !ok {
			s = "ERR"
		}
		if s != want {
			t.Errorf("ISOFormat(%v) = %q want %q", float64(g.Instants[i]), s, want)
		}
	}
}

func testVegaTime(t *testing.T, g *timeGolden, zoneName string) {
	fails := 0
	report := func(format string, args ...any) {
		if fails++; fails <= 30 {
			t.Errorf(format, args...)
		}
	}
	fi := floats(g.FloorInstants)
	for _, c := range g.Floors {
		f := NewFloor(zoneFor(t, zoneName, c.UTC), c.Units, stepOf(c.Step))
		for i, v := range fi {
			if got := f.Floor(v); !same(got, float64(c.Out[i])) {
				report("floor utc=%v %v step=%v (%v) = %v, want %v", c.UTC, c.Units, c.Step, v, got, float64(c.Out[i]))
			}
		}
	}
	for _, c := range g.EarlyFloors {
		f := NewFloor(zoneFor(t, zoneName, c.UTC), c.Units, 1)
		for i, v := range c.Ins {
			if got := f.Floor(v); !same(got, float64(c.Out[i])) {
				report("early floor utc=%v %v (%v) = %v, want %v", c.UTC, c.Units, v, got, float64(c.Out[i]))
			}
		}
	}
	ins := floats(g.Instants)
	for _, c := range g.Offsets {
		z := zoneFor(t, zoneName, c.UTC)
		for i, v := range ins {
			got, ok := OffsetUnit(z, c.Unit, v, c.Step)
			var want string
			if !ok {
				want = "undef"
				if string(c.Out[i]) != `"undef"` {
					report("offset utc=%v %s: unknown unit but golden %s", c.UTC, c.Unit, c.Out[i])
				}
				continue
			}
			_ = want
			var w nf
			if err := json.Unmarshal(c.Out[i], &w); err != nil {
				report("offset utc=%v %s(%v,%v): golden %s but got %v", c.UTC, c.Unit, v, c.Step, c.Out[i], got)
				continue
			}
			if !same(got, float64(w)) {
				report("offset utc=%v %s(%v,%v) = %v, want %v", c.UTC, c.Unit, v, c.Step, got, float64(w))
			}
		}
	}
	for _, c := range g.Sequences {
		z := zoneFor(t, zoneName, c.UTC)
		got, ok, err := SequenceUnit(context.Background(), z, c.Unit, c.Start, c.Stop, c.Step)
		if err != nil {
			t.Fatal(err)
		}
		if string(c.Out) == `"undef"` {
			if ok {
				report("sequence %s should be undefined", c.Unit)
			}
			continue
		}
		var want []float64
		if err := json.Unmarshal(c.Out, &want); err != nil {
			t.Fatal(err)
		}
		if !ok || !equalSlices(got, want) {
			report("sequence utc=%v %s(%v,%v,%v): got %v (%d) want %v (%d)", c.UTC, c.Unit, c.Start, c.Stop, c.Step, head(got), len(got), head(want), len(want))
		}
	}
	for _, c := range g.Specs {
		var over UnitSpecifiers
		if c.Over != nil {
			over = UnitSpecifiers(c.Over)
		}
		got, err := TimeUnitSpecifier(c.Units, over)
		var want string
		if json.Unmarshal(c.Out, &want) != nil {
			if err == nil {
				report("timeUnitSpecifier(%v) should fail: %s", c.Units, c.Out)
			} else if !strings.Contains(string(c.Out), err.Error()) {
				report("timeUnitSpecifier(%v) error %q, golden %s", c.Units, err, c.Out)
			}
			continue
		}
		if err != nil || got != want {
			report("timeUnitSpecifier(%v, %v) = %q,%v want %q", c.Units, c.Over, got, err, want)
		}
	}
	for _, c := range g.Bins {
		mb := 0.0
		if c.Maxbins != nil {
			mb = *c.Maxbins
		}
		got := Bin(float64(c.Start), float64(c.Stop), mb)
		if !slicesEqual(got.Units, c.Units) || !same(got.Step, float64(c.Step)) {
			report("bin(%v,%v,%v) = %v,%v want %v,%v", float64(c.Start), float64(c.Stop), mb, got.Units, got.Step, c.Units, float64(c.Step))
		}
	}
	for _, list := range [][]doyRec{g.Doy, g.DoyEarly} {
		for _, c := range list {
			lz, uz := Local(mustLoc(t, zoneName)), UTC
			v := float64(c.T)
			if got := lz.DayOfYear(v); !same(got, float64(c.Doy)) {
				report("dayofyear(%v) = %v want %v", v, got, float64(c.Doy))
			}
			if got := lz.WeekOfYear(v); !same(got, float64(c.Week)) {
				report("week(%v) = %v want %v", v, got, float64(c.Week))
			}
			if got := uz.DayOfYear(v); !same(got, float64(c.Udoy)) {
				report("utcdayofyear(%v) = %v want %v", v, got, float64(c.Udoy))
			}
			if got := uz.WeekOfYear(v); !same(got, float64(c.Uweek)) {
				report("utcweek(%v) = %v want %v", v, got, float64(c.Uweek))
			}
		}
	}
	for _, c := range g.Detect {
		z := zoneFor(t, zoneName, c.UTC)
		dates := floats(c.Set)
		got, err := DetectUnits(dates, z)
		if c.Out.Err != "" {
			if err == nil || !strings.Contains(err.Error(), "Invalid date") {
				report("detect(%v) = %v,%v want error %s", dates, got, err, c.Out.Err)
			}
			continue
		}
		if err != nil || !slicesEqual(got.Units, c.Out.Units) || got.Step != c.Out.Step {
			report("detect(%v, utc=%v) = %v,%v want %v", dates, c.UTC, got, err, c.Out)
		}
	}
}

func stepOf(p *float64) float64 {
	if p == nil {
		return 0 // undefined and 0 both mean the default step of 1
	}
	return *p
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func testVegaFormat(t *testing.T, g *timeGolden, zoneName string) {
	fails := 0
	report := func(format string, args ...any) {
		if fails++; fails <= 30 {
			t.Errorf(format, args...)
		}
	}
	loc := DefaultLocale()
	loc.Local = Local(mustLoc(t, zoneName))
	mi := floats(g.MfInstants)
	for _, c := range g.Multi {
		spec := jsval.Null
		if string(c.Spec) != "null" {
			v, err := jsval.ParseJSON(c.Spec)
			if err != nil {
				t.Fatal(err)
			}
			spec = v
		}
		f, err := loc.TimeFormatSpec(spec, c.UTC)
		if err != nil {
			t.Fatal(err)
		}
		for i, v := range mi {
			if got := f(v); got != c.Out[i] {
				report("multi utc=%v spec=%s (%v) = %q, want %q", c.UTC, c.Spec, v, got, c.Out[i])
			}
		}
	}
	vals := make([]float64, len(g.FfVals))
	for i, s := range g.FfVals {
		vals[i] = parseJS(t, s)
	}
	for _, c := range g.FormatFloat {
		f, err := loc.FormatFloat(c.Spec)
		if err != nil {
			report("formatFloat(%q): %v", c.Spec, err)
			continue
		}
		for i, v := range vals {
			if got := f(v); got != c.Out[i] {
				report("formatFloat(%q)(%v) = %q, want %q", c.Spec, v, got, c.Out[i])
			}
		}
	}
	for _, c := range g.FormatSpan {
		spec := ",f" // undefined selects ",f"; the empty specifier is a specifier
		if string(c.Spec) != "null" {
			_ = json.Unmarshal(c.Spec, &spec)
		}
		var want []string
		if err := json.Unmarshal(c.Out, &want); err != nil {
			if _, e := loc.FormatSpan(c.A, c.B, c.C, spec); e == nil {
				report("formatSpan(%v,%v,%v,%q) should fail: %s", c.A, c.B, c.C, spec, c.Out)
			}
			continue
		}
		f, err := loc.FormatSpan(c.A, c.B, c.C, spec)
		if err != nil {
			report("formatSpan(%v,%v,%v,%q): %v", c.A, c.B, c.C, spec, err)
			continue
		}
		for i, v := range vals {
			if got := f(v); got != want[i] {
				report("formatSpan(%v,%v,%v,%q)(%v) = %q, want %q", c.A, c.B, c.C, spec, v, got, want[i])
			}
		}
	}
}
