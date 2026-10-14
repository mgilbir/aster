package wordcloud

import (
	"math"
	"math/rand"
	"slices"
	"testing"
)

// referenceScan is scan as first written: the edges sorted by top, and every
// sub-scanline's crossings sorted from scratch. scan must mark the same
// pixels.
func referenceScan(edges []inkEdge, m *Mask) {
	if len(edges) == 0 {
		return
	}
	edges = slices.Clone(edges)
	slices.SortFunc(edges, func(a, b inkEdge) int {
		switch {
		case a.y0 < b.y0:
			return -1
		case a.y0 > b.y0:
			return 1
		}
		return 0
	})
	minX, maxX := math.Inf(1), math.Inf(-1)
	minY, maxY := edges[0].y0, math.Inf(-1)
	for _, e := range edges {
		minX = math.Min(minX, math.Min(e.x0, e.x1))
		maxX = math.Max(maxX, math.Max(e.x0, e.x1))
		maxY = math.Max(maxY, e.y1)
	}
	px0 := int(math.Max(math.Floor(minX), 0))
	px1 := int(math.Min(math.Ceil(maxX), sheetW))
	py0 := int(math.Max(math.Floor(minY), 0))
	py1 := int(math.Min(math.Ceil(maxY), sheetH))
	if px0 >= px1 || py0 >= py1 {
		return
	}
	row := make([]float32, px1-px0)
	span := func(xa, xb float64, w float32) {
		xa = math.Max(xa, float64(px0))
		xb = math.Min(xb, float64(px1))
		if xa >= xb {
			return
		}
		ia, ib := int(xa), int(xb)
		if ia == ib {
			row[ia-px0] += float32(float32(xb-xa) * w)
			return
		}
		row[ia-px0] += float32(float32(float64(ia+1)-xa) * w)
		for i := ia + 1; i < ib; i++ {
			row[i-px0] += w
		}
		if ib-px0 < len(row) {
			row[ib-px0] += float32(float32(xb-float64(ib)) * w)
		}
	}
	next := 0
	var active []int
	var cross []crossing
	const w = float32(1.0 / subRows)
	for y := py0; y < py1; y++ {
		clear(row)
		for k := 0; k < subRows; k++ {
			ys := float64(y) + (float64(k)+0.5)/subRows
			for next < len(edges) && edges[next].y0 <= ys {
				active = append(active, next)
				next++
			}
			cross = cross[:0]
			live := active[:0]
			for _, i := range active {
				e := &edges[i]
				if e.y1 <= ys {
					continue
				}
				live = append(live, i)
				cross = append(cross, crossing{x: e.x0 + float64((ys-e.y0)*e.dxdy), dir: e.dir})
			}
			active = live
			slices.SortFunc(cross, func(a, b crossing) int {
				switch {
				case a.x < b.x:
					return -1
				case a.x > b.x:
					return 1
				}
				return 0
			})
			wind := 0
			for i := 0; i+1 < len(cross); i++ {
				wind += cross[i].dir
				if wind != 0 {
					span(cross[i].x, cross[i+1].x, w)
				}
			}
		}
		for i, v := range row {
			if v >= inkCoverage {
				m.Set(px0+i, y)
			}
		}
	}
}

// checkPasses fills and strokes r's contours and compares each pass's mask
// with referenceScan's.
func checkPasses(t *testing.T, name string, r *inkRaster, width float64) {
	t.Helper()
	got, want := newMask(), newMask()
	r.fill(got)
	referenceScan(r.edges, want)
	if !slices.Equal(got.bits, want.bits) {
		t.Fatalf("%s: fill differs from the reference", name)
	}
	if width > 0 {
		got, want = newMask(), newMask()
		r.stroke(got, width)
		referenceScan(r.edges, want)
		if !slices.Equal(got.bits, want.bits) {
			t.Fatalf("%s: stroke %v differs from the reference", name, width)
		}
	}
}

// TestScanMatchesReference compares scan with referenceScan on random
// polygons (with shared and horizontal edges, coincident crossings and parts
// off the sheet) and on words drawn as the layout draws them.
func TestScanMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	coord := func(lo, hi float64) float64 {
		v := lo + rng.Float64()*(hi-lo)
		switch rng.Intn(3) {
		case 0:
			return math.Round(v) // on pixel boundaries
		case 1:
			return math.Round(v*2) / 2
		}
		return fx(v)
	}
	var r inkRaster
	for range 400 {
		r.reset()
		cx, cy := coord(-40, sheetW+40), coord(-40, sheetH+40)
		for range 1 + rng.Intn(6) {
			var c []pt
			size := 1 + rng.Float64()*60
			for range 3 + rng.Intn(30) {
				c = append(c, pt{coord(cx-size, cx+size), coord(cy-size, cy+size)})
			}
			// Repeat points and edges, as glyph outlines and strokes do.
			if rng.Intn(3) == 0 {
				j := rng.Intn(len(c))
				c = slices.Insert(c, j, c[j])
			}
			r.contours = append(r.contours, c)
		}
		checkPasses(t, "polygons", &r, float64(rng.Intn(4))*rng.Float64()*8)
	}

	cr := NewCanvasRenderer(dejavuShaper(t))
	words := []string{"Hello gy", "W", "wordcloud", "MMMMMMMM", "a.b,c;", "Ölçü ß", "lorem ipsum dolor"}
	weights := []string{"normal", "bold", "300"}
	for range 300 {
		f := Font{Style: "normal", Weight: weights[rng.Intn(len(weights))], Family: "DejaVu Sans", Px: 1 + rng.Intn(100)}
		angle := 0.0
		if rng.Intn(2) == 0 {
			angle = rng.Float64() * 2 * math.Pi
		}
		w := words[rng.Intn(len(words))]
		cr.Draw(newMask(), f, w, rng.Float64()*sheetW, rng.Float64()*sheetH, angle, 0)
		checkPasses(t, w, &cr.ink, 1+rng.Float64()*6)
	}
}
