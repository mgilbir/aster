package transforms

import (
	"context"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

func TestTimeUnitGolden(t *testing.T) {
	// timeunit.json was recorded with TZ=America/New_York.
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tz database unavailable:", err)
	}
	for _, c := range loadGolden(t, "timeunit.json") {
		t.Run(c.Name, func(t *testing.T) {
			spec := param(c, 0)
			p := TimeUnitParams{
				Field:      FieldOf(spec.Get("field").StrValue()),
				NoInterval: spec.Get("interval").IsBool() && !spec.Get("interval").BoolValue(),
				Units:      strList(spec.Get("units")),
				Step:       spec.Get("step").NumValue(),
				MaxBins:    spec.Get("maxbins").NumValue(),
				InferUnits: spec.Get("inferUnits").IsTruthy(),
				Zone:       format.Local(ny),
			}
			if spec.Get("timezone").StrValue() == "utc" {
				p.Zone = format.UTC
			}
			if e := spec.Get("extent"); e.IsArr() {
				p.Extent = &[2]float64{e.Index(0).NumValue(), e.Index(1).NumValue()}
			}
			if as := strList(spec.Get("as")); len(as) == 2 {
				p.As = [2]string{as[0], as[1]}
			}
			got, _, err := TimeUnit(context.Background(), cloneTuples(c.Input), p)
			if c.Error != "" {
				if err == nil {
					t.Fatalf("want error %q", c.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if d := diffValues("out", tupleArr(got), c.Output, 0); d != "" {
				t.Fatal(d)
			}
		})
	}
}

func TestTimeUnitInfo(t *testing.T) {
	d := func(ms float64) jsval.Value { return obj("t", jsval.Timestamp(ms)) }
	data := []jsval.Value{d(1e12), d(1.1e12), obj("t", jsval.Null)}
	_, info, err := TimeUnit(context.Background(), data, TimeUnitParams{Field: FieldOf("t"), Units: []string{"month", "year"}})
	if err != nil {
		t.Fatal(err)
	}
	if info.Unit != "month" || info.Step != 1 || len(info.Units) != 2 || info.Units[0] != "year" {
		t.Errorf("info = %+v", info)
	}
	if !(info.Start < info.Stop) {
		t.Errorf("start/stop = %v/%v", info.Start, info.Stop)
	}
	_, info, _ = TimeUnit(context.Background(), nil, TimeUnitParams{Field: FieldOf("t"), Units: []string{"year"}})
	if info.Start <= info.Stop {
		t.Error("empty input must leave start=+Inf, stop=-Inf")
	}
}
