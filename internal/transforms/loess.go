package transforms

import (
	"context"
	"math"
	"slices"

	"github.com/mgilbir/aster/internal/jsval"
)

// LoessParams configures Loess (vega-regression's loess).
type LoessParams struct {
	X, Y    Field
	GroupBy []Field
	// Bandwidth is the fraction of points in each local fit; 0 (unset) means 0.3.
	Bandwidth float64
	As        []string
}

// Loess emits the locally weighted regression curve of each group: one point
// per distinct x (points sharing an x are averaged), in ascending x.
func Loess(ctx context.Context, data []jsval.Value, p LoessParams) ([]jsval.Value, error) {
	bw := p.Bandwidth
	if bw == 0 || math.IsNaN(bw) {
		bw = 0.3
	}
	names := make([]string, len(p.GroupBy))
	for i, g := range p.GroupBy {
		names[i] = g.Name
	}
	as0, as1 := p.X.Name, p.Y.Name
	if len(p.As) > 0 {
		as0 = p.As[0]
	}
	if len(p.As) > 1 {
		as1 = p.As[1]
	}
	var out []jsval.Value
	for _, g := range regressionGroups(data, p.GroupBy) {
		pts, err := LoessFit(ctx, g.Tuples, p.X.Get, p.Y.Get, bw)
		if err != nil {
			return nil, err
		}
		for _, pt := range pts {
			t := jsval.NewObject(len(names) + 2)
			for i, n := range names {
				t.Set(n, g.Dims[i])
			}
			t.Set(as0, jsval.Num(pt[0]))
			t.Set(as1, jsval.Num(pt[1]))
			out = append(out, jsval.Obj(t))
		}
	}
	return out, nil
}

const (
	loessMaxIters = 2
	loessEpsilon  = 1e-12
)

// loessToInt32 is JavaScript's ~~x.
func loessToInt32(f float64) int {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	f = math.Trunc(f)
	return int(int32(uint32(int64(math.Mod(f, 4294967296)))))
}

// LoessFit is vega-statistics' regressionLoess: robust local linear
// regression over the k nearest neighbours (k = max(2, ~~(bandwidth*n))),
// with two robustness iterations. Where the neighbourhood runs past the data
// (bandwidth above 1), upstream reads undefined and the fit is NaN; so here.
func LoessFit(ctx context.Context, data []jsval.Value, x, y Accessor, bandwidth float64) ([][2]float64, error) {
	ps := regressionPoints(data, x, y, true)
	xv, yv, ux, uy := ps.x, ps.y, ps.ux, ps.uy
	n := len(xv)
	bw := max(2, loessToInt32(bandwidth*float64(n)))
	yhat := make([]float64, n)
	residuals := make([]float64, n)
	robust := make([]float64, n)
	for i := range robust {
		robust[i] = 1
	}
	at := func(a []float64, i int) float64 {
		if i < 0 || i >= len(a) {
			return math.NaN()
		}
		return a[i]
	}
	for iter := 0; iter <= loessMaxIters; iter++ {
		i0, i1 := 0, bw-1
		for i := 0; i < n; i++ {
			if err := poll(ctx, i); err != nil {
				return nil, err
			}
			dx := xv[i]
			if i1 >= n {
				// Any neighbour past the end contributes NaN weight.
				yhat[i] = math.NaN()
				residuals[i] = math.NaN()
			} else {
				edge := i1
				if dx-xv[i0] > xv[i1]-dx {
					edge = i0
				}
				var W, X, Y, XY, X2 float64
				d := math.Abs(xv[edge] - dx)
				if d == 0 {
					d = 1
				}
				denom := 1 / d
				for k := i0; k <= i1; k++ {
					xk, yk := xv[k], yv[k]
					w := float64(loessTricube(math.Abs(dx-xk)*denom) * robust[k])
					xkw := float64(xk * w)
					W += w
					X += xkw
					Y += float64(yk * w)
					XY += float64(yk * xkw)
					X2 += float64(xk * xkw)
				}
				a, b := regressionOLS(X/W, Y/W, XY/W, X2/W)
				yhat[i] = a + float64(b*dx)
				residuals[i] = math.Abs(yv[i] - yhat[i])
			}
			// updateInterval
			val := at(xv, i+1)
			left, right := i0, i1+1
			if right < n {
				for j := i + 1; j > left && (xv[right]-val) <= (val-xv[left]); {
					left++
					i0, i1 = left, right
					right++
					if right >= n {
						// upstream reads undefined here: NaN comparison ends the loop
						break
					}
				}
			}
		}
		if iter == loessMaxIters {
			break
		}
		med := loessMedian(residuals)
		if math.Abs(med) < loessEpsilon {
			break
		}
		for i := 0; i < n; i++ {
			arg := residuals[i] / (6 * med)
			if arg >= 1 {
				robust[i] = loessEpsilon
			} else {
				w := 1 - float64(arg*arg)
				robust[i] = w * w
			}
		}
	}
	return loessOutput(xv, yhat, ux, uy), nil
}

func loessTricube(x float64) float64 {
	x = 1 - float64(float64(x*x)*x)
	return x * x * x
}

// loessMedian is d3.median: NaNs are dropped; empty gives NaN (undefined).
func loessMedian(v []float64) float64 {
	s := make([]float64, 0, len(v))
	for _, f := range v {
		if f == f {
			s = append(s, f)
		}
	}
	slices.Sort(s)
	m, ok := QuantileSorted(s, 0.5)
	if !ok {
		return math.NaN()
	}
	return m
}

// loessOutput uncentres the fit and averages runs of equal x with upstream's
// online update (whose counter makes the first repeat replace the value).
func loessOutput(xv, yhat []float64, ux, uy float64) [][2]float64 {
	var out [][2]float64
	cnt := 0
	for i, xi := range xv {
		v := xi + ux
		if len(out) > 0 && out[len(out)-1][0] == v {
			cnt++
			last := &out[len(out)-1]
			last[1] += (yhat[i] - last[1]) / float64(cnt)
		} else {
			cnt = 0
			if len(out) > 0 {
				out[len(out)-1][1] += uy
			}
			out = append(out, [2]float64{v, yhat[i]})
		}
	}
	if len(out) > 0 {
		out[len(out)-1][1] += uy
	}
	return out
}
