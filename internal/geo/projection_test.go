package geo

import (
	"encoding/json"
	"math"
	"testing"
)

type goldenPoints struct {
	Points [][2]float64 `json:"points"`
	Xys    [][2]float64 `json:"xys"`
	Cases  []struct {
		ID        string          `json:"id"`
		Type      string          `json:"type"`
		Props     json.RawMessage `json:"props"`
		Scale     any             `json:"scale"`
		Translate []any           `json:"translate"`
		Forward   []any           `json:"forward"`
		Invert    []any           `json:"invert"`
	} `json:"cases"`
}

func pairOfGolden(v any) (x, y float64, ok bool) {
	arr, isArr := v.([]any)
	if !isArr {
		return 0, 0, false
	}
	return gnum(arr[0]), gnum(arr[1]), true
}

func TestProjectionForwardInvert(t *testing.T) {
	var g goldenPoints
	loadGolden(t, "points.json.gz", &g)
	var exact, total int
	inexact := map[string]int{}
	for _, c := range g.Cases {
		p := projFromGolden(t, c.Type, c.Props)
		if !same(p.Scale(), gnum(c.Scale)) {
			t.Errorf("%s: scale %v want %v", c.ID, p.Scale(), gnum(c.Scale))
		}
		tx, ty := p.Translate()
		if !same(tx, gnum(c.Translate[0])) || !same(ty, gnum(c.Translate[1])) {
			t.Errorf("%s: translate [%v %v] want %v", c.ID, tx, ty, c.Translate)
		}
		bad := 0
		for i, pt := range g.Points {
			x, y, ok := p.Forward(pt[0], pt[1])
			wx, wy, wok := pairOfGolden(c.Forward[i])
			if ok != wok {
				if bad < 3 {
					t.Errorf("%s: forward %v ok=%v want %v", c.ID, pt, ok, wok)
				}
				bad++
				continue
			}
			if !wok {
				continue
			}
			total += 2
			if same(x, wx) {
				exact++
			}
			if same(y, wy) {
				exact++
			}
			if !same(x, wx) || !same(y, wy) {
				inexact[c.ID]++
			}
			if !same(x, wx) || !same(y, wy) {
				if bad < 3 {
					t.Errorf("%s: forward %v = [%v %v] want [%v %v]", c.ID, pt, x, y, wx, wy)
				}
				bad++
			}
		}
		for i, xy := range g.Xys {
			lon, lat, ok := p.Invert(xy[0], xy[1])
			want := c.Invert[i]
			if s, isStr := want.(string); isStr && s == "undefined" {
				if ok {
					t.Errorf("%s: invert %v should be unavailable", c.ID, xy)
				}
				continue
			}
			wl, wp, wok := pairOfGolden(want)
			if !wok {
				if ok && !bad2(lon, lat) {
					t.Errorf("%s: invert %v = [%v %v] want null", c.ID, xy, lon, lat)
				}
				continue
			}
			if !ok {
				t.Errorf("%s: invert %v unavailable", c.ID, xy)
				continue
			}
			// Positions off the map (out of the raw projection's domain) invert to
			// numerically meaningless values that amplify rounding differences.
			if math.Abs(wl) > 180 || math.Abs(wp) > 90 {
				continue
			}
			total += 2
			if same(lon, wl) {
				exact++
			}
			if same(lat, wp) {
				exact++
			}
			// The antimeridian may be reached from either side.
			lonOK := near(lon, wl, 1e-7) || near(lon+360, wl, 1e-7) || near(lon-360, wl, 1e-7)
			if math.Abs(wp) >= 90-1e-9 { // longitude is undefined at a pole
				lonOK = true
			}
			if !lonOK || !near(lat, wp, 1e-7) {
				if bad < 6 {
					t.Errorf("%s: invert %v = [%v %v] want [%v %v]", c.ID, xy, lon, lat, wl, wp)
				}
				bad++
			}
		}
	}
	t.Logf("bit-exact: %d of %d numbers; inexact forward cases: %v", exact, total, inexact)
}

func bad2(a, b float64) bool { return math.IsNaN(a) && math.IsNaN(b) }
