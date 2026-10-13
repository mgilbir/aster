package format

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestWeekEveryMatchesD3 is X15: week.every(k) in zones with daylight
// saving (and local mean time, and a skipped day) floors, ceils and ranges as
// d3's walk does, for k from a walk's size to a million weeks (vectors from
// node 24, testdata/gen_weekevery.mjs).
func TestWeekEveryMatchesD3(t *testing.T) {
	f, err := os.Open("testdata/weekevery.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(zr)
	sc.Buffer(nil, 1<<24)
	zones := 0
	for sc.Scan() {
		var rec struct {
			Zone string
			Recs []struct {
				K, T, Floor, Ceil float64
				Range             []float64
			}
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatal(err)
		}
		loc, err := time.LoadLocation(rec.Zone)
		if err != nil {
			t.Fatal(err)
		}
		zones++
		z := Local(loc)
		for _, c := range rec.Recs {
			iv, _ := z.Week(0).Every(c.K)
			if got := iv.Floor(c.T); got != c.Floor {
				t.Errorf("%s every(%v).floor(%v) = %v, want %v", rec.Zone, c.K, c.T, got, c.Floor)
			}
			if got := iv.Ceil(c.T); got != c.Ceil {
				t.Errorf("%s every(%v).ceil(%v) = %v, want %v", rec.Zone, c.K, c.T, got, c.Ceil)
			}
			got, err := iv.Range(context.Background(), c.T, c.T+3.5*c.K*msPerWeek, 1)
			if err != nil || len(got) != len(c.Range) {
				t.Errorf("%s every(%v).range(%v) = %v, %v; want %v", rec.Zone, c.K, c.T, got, err, c.Range)
			} else {
				for i := range got {
					if got[i] != c.Range[i] {
						t.Errorf("%s every(%v).range(%v)[%d] = %v, want %v", rec.Zone, c.K, c.T, i, got[i], c.Range[i])
					}
				}
			}
		}
	}
	if zones < 7 {
		t.Errorf("%d zones recorded, want 7", zones)
	}
}

// TestWeekSearchWorkIsLogarithmic is X15's work guard, counted rather than
// timed: a floor or a next boundary of week.every(k) evaluates a few dozen
// weeks however large k, where d3's walk (and the engine's, before) visits
// up to k of them.
func TestWeekSearchWorkIsLogarithmic(t *testing.T) {
	for _, zone := range []string{"America/New_York", "Pacific/Apia", "Australia/Lord_Howe"} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		z := Local(loc)
		for _, k := range []float64{65, 1000, 65537, 1e6} {
			iv, _ := z.Week(0).Every(k)
			if !iv.weekSearch() {
				t.Fatalf("%s every(%v) is walked", zone, k)
			}
			for _, at := range []float64{-2.5e12, 0, 1.7e12} {
				w := iv.baseFloorUnfiltered(at)
				var floorEvals, nextEvals int
				iv.weekFloorN(w, &floorEvals)
				iv.weekNextN(w, &nextEvals)
				if floorEvals > 200 || nextEvals > 200 {
					t.Errorf("%s every(%v) at %v: %d weeks evaluated to floor, %d to the next boundary", zone, k, at, floorEvals, nextEvals)
				}
			}
		}
	}
}

// baseFloorUnfiltered is the start of t's week, ignoring the filter.
func (iv Interval) baseFloorUnfiltered(t float64) float64 {
	parent := iv
	parent.filter = 0
	return parent.baseFloor(t)
}
