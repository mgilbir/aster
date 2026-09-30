// Package voronoi implements Vega's voronoi transform (vega-voronoi), which
// draws one clipped Voronoi cell path per datum. The geometry follows
// d3-delaunay 6 on top of delaunator 5 and Shewchuk's robust orient2d, and the
// path text is formatted the way JavaScript stringifies the numbers so the
// output can be compared with upstream byte for byte.
package voronoi

import (
	"context"
	"errors"
	"fmt"

	"github.com/mgilbir/aster/internal/jsval"
)

// Params are the voronoi transform parameters.
type Params struct {
	// X and Y read a datum's coordinates; they are required. The result is
	// coerced with JavaScript's Number(), as assigning to a Float64Array does.
	X, Y func(jsval.Value) jsval.Value
	// Size, when it has two elements, sets the clip extent to [0,0,w,h] and
	// takes precedence over Extent.
	Size []float64
	// Extent is the clip extent xmin, ymin, xmax, ymax. When empty the
	// upstream default of ±1e5 is used.
	Extent []float64
	// As is the output field; the default is "path".
	As string
}

var defaultExtent = [4]float64{-1e5, -1e5, 1e5, 1e5}

// cancelStride is how many cells are drawn between context checks.
const cancelStride = 256

// Transform writes each datum's cell path (a string, or null for an empty or
// degenerate cell) into field p.As of the datum. Data that are not objects
// are skipped. Like upstream it does nothing for empty data.
func Transform(ctx context.Context, data []jsval.Value, p Params) (err error) {
	if len(data) == 0 {
		return nil
	}
	if p.X == nil || p.Y == nil {
		return errors.New("voronoi: x and y accessors are required")
	}
	// The triangulation code indexes typed arrays; on non-finite coordinates
	// upstream silently reads undefined where Go would fault, so convert any
	// such fault into an error rather than crash the host.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("voronoi: degenerate input: %v", r)
		}
	}()

	as := p.As
	if as == "" {
		as = "path"
	}
	b := defaultExtent
	switch {
	case len(p.Size) >= 2:
		b = [4]float64{0, 0, p.Size[0], p.Size[1]}
	case len(p.Extent) >= 4:
		copy(b[:], p.Extent)
	}

	coords := make([]float64, 2*len(data))
	for i, d := range data {
		coords[2*i] = jsval.ToNumber(p.X(d))
		coords[2*i+1] = jsval.ToNumber(p.Y(d))
	}
	dg, err := newDiagram(newDelaunay(coords), b[0], b[1], b[2], b[3])
	if err != nil {
		return err
	}

	var buf []byte
	for i, d := range data {
		if i%cancelStride == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		o := d.ObjValue()
		if o == nil {
			continue
		}
		poly := dg.cellPolygon(int32(i))
		if len(poly) == 0 || isPoint(poly) {
			o.Set(as, jsval.Null)
			continue
		}
		buf = appendPath(buf[:0], poly)
		o.Set(as, jsval.Str(string(buf)))
	}
	return nil
}

// isPoint reports a polygon that is a single repeated point.
func isPoint(p []float64) bool {
	return len(p) == 4 && p[0] == p[2] && p[1] == p[3]
}

// appendPath renders the polygon as "M x,y L x,y ... Z", dropping the closing
// duplicates of the first vertex (upstream's toPathString).
func appendPath(dst []byte, p []float64) []byte {
	x, y := p[0], p[1]
	n := len(p)/2 - 1
	for n > 0 && p[2*n] == x && p[2*n+1] == y {
		n--
	}
	dst = append(dst, 'M')
	for i := 0; i <= n; i++ {
		if i > 0 {
			dst = append(dst, 'L')
		}
		dst = jsval.AppendJSNumber(dst, p[2*i])
		dst = append(dst, ',')
		dst = jsval.AppendJSNumber(dst, p[2*i+1])
	}
	return append(dst, 'Z')
}
