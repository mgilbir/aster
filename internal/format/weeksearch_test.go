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
			start := time.Now()
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
			if d := time.Since(start); d > 200*time.Millisecond {
				t.Errorf("%s every(%v) at %v took %v", rec.Zone, c.K, c.T, d)
			}
		}
	}
	if zones < 7 {
		t.Errorf("%d zones recorded, want 7", zones)
	}
}
