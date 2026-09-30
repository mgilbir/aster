package transforms

import (
	"context"
	"fmt"
	"math"

	"github.com/mgilbir/aster/purego/internal/jsmath"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// RegressionParams configures Regression (vega-regression's regression).
type RegressionParams struct {
	X, Y    Field
	GroupBy []Field
	// Method is constant, linear, log, exp, pow, quad or poly; empty means linear.
	Method string
	// Order is the polynomial order for poly. Use DefaultRegressionOrder when
	// the specification gave none.
	Order int
	// Extent is the x domain of the drawn curve; nil computes it per group.
	Extent []float64
	// Params outputs one coefficient tuple per group instead of curve points.
	Params bool
	// As names the output x and y fields; nil or short means the accessor names.
	As []string
}

// DefaultRegressionOrder is upstream's default polynomial order.
const DefaultRegressionOrder = 3

// regressionDOF is the number of fitted parameters a group must exceed.
func regressionDOF(method string, order int) int {
	switch method {
	case "poly":
		return order
	case "quad":
		return 2
	}
	return 1
}

// Regression fits each group and emits either the trend line or the fit
// parameters. Groups with no more points than parameters are skipped.
// Linear and constant fits emit only the two extent end points; other methods
// emit an adaptively sampled curve (25 to 200 points).
func Regression(ctx context.Context, data []jsval.Value, p RegressionParams) ([]jsval.Value, error) {
	method := p.Method
	if method == "" {
		method = "linear"
	}
	if !regressionMethods[method] {
		return nil, fmt.Errorf("Invalid regression method: %s", method)
	}
	if method == "poly" && (p.Order < 0 || p.Order > MaxPolyOrder) {
		return nil, fmt.Errorf("regression: poly order %d out of range [0,%d]", p.Order, MaxPolyOrder)
	}
	dof := regressionDOF(method, p.Order)
	domain := p.Extent
	if domain != nil && method == "log" && len(domain) > 0 && domain[0] <= 0 {
		domain = nil // upstream warns and ignores it
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
	groups := regressionGroups(data, p.GroupBy)
	var out []jsval.Value
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(g.Tuples) <= dof {
			continue
		}
		model, err := FitRegression(method, g.Tuples, p.X.Get, p.Y.Get, p.Order)
		if err != nil {
			return nil, err
		}
		if p.Params {
			o := jsval.NewObject(3)
			if g.Dims != nil {
				o.Set("keys", jsval.Arr(g.Dims))
			}
			coef := make([]jsval.Value, len(model.Coef))
			for i, c := range model.Coef {
				coef[i] = jsval.Num(c)
			}
			o.Set("coef", jsval.Arr(coef))
			o.Set("rSquared", jsval.Num(model.RSquared))
			out = append(out, jsval.Obj(o))
			continue
		}
		var dom []jsval.Value
		if domain != nil {
			for _, d := range domain {
				dom = append(dom, jsval.Num(d))
			}
		} else {
			lo, hi, _ := Extent(len(g.Tuples), func(i int) jsval.Value { return p.X.Get(g.Tuples[i]) })
			dom = []jsval.Value{lo, hi}
		}
		add := func(x jsval.Value, y float64) {
			t := jsval.NewObject(len(names) + 2)
			for i, n := range names {
				t.Set(n, g.Dims[i])
			}
			t.Set(as0, x)
			t.Set(as1, jsval.Num(y))
			out = append(out, jsval.Obj(t))
		}
		if method == "linear" || method == "constant" {
			for _, x := range dom {
				add(x, model.Predict(jsval.ToNumber(x)))
			}
		} else {
			d0, d1 := math.NaN(), math.NaN()
			if len(dom) > 0 {
				d0 = jsval.ToNumber(dom[0])
			}
			if len(dom) > 1 {
				d1 = jsval.ToNumber(dom[1])
			}
			pts := regressionSampleCurve(model.Predict, d0, d1, 25, 200,
				len(dom) > 0 && dom[0].IsTimestamp(), len(dom) > 1 && dom[1].IsTimestamp())
			for i, pt := range pts {
				x := jsval.Num(pt[0])
				// The end points are the extent values themselves (a date stays a date).
				if i == 0 && len(dom) > 0 {
					x = dom[0]
				} else if i == len(pts)-1 && len(dom) > 1 {
					x = dom[1]
				}
				add(x, pt[1])
			}
		}
	}
	return out, nil
}

// regressionGroups is upstream's partition: no group-by (nil) is one group of
// everything; an explicitly empty list is one empty-dims group when there is
// data and none otherwise.
func regressionGroups(data []jsval.Value, groupby []Field) []Group {
	if groupby != nil && len(groupby) == 0 {
		if len(data) == 0 {
			return nil
		}
		return []Group{{Dims: []jsval.Value{}, Tuples: data}}
	}
	return Partition(data, groupby)
}

const regressionMinRadians = 0.5 * math.Pi / 180

// regressionSampleCurve is vega-statistics' sampleCurve: adaptive sampling of f
// over [minX, maxX], subdividing where the curve bends by more than half a
// degree, between minSteps and maxSteps points. Work is capped so a reversed or
// non-finite extent cannot run away.
func regressionSampleCurve(f func(float64) float64, minX, maxX float64, minSteps, maxSteps int, dateMin, dateMax bool) [][2]float64 {
	if minSteps == 0 {
		minSteps = 25
	}
	if maxSteps < minSteps {
		maxSteps = minSteps
	}
	// A date extent end is a Date object upstream: `(a + b) / 2` with a Date
	// operand concatenates strings and yields NaN, so a segment that ends at
	// such a point is never subdivided. The flag reproduces that.
	type pt struct {
		x, y float64
		date bool
	}
	point := func(x float64, date bool) pt { return pt{x, f(x), date} }
	span := maxX - minX
	stop := span / float64(maxSteps)
	prev := []pt{point(minX, dateMin)}
	toArr := func(ps []pt) [][2]float64 {
		out := make([][2]float64, len(ps))
		for i, p := range ps {
			out[i] = [2]float64{p.x, p.y}
		}
		return out
	}
	if minSteps == maxSteps {
		for i := 1; i < maxSteps; i++ {
			prev = append(prev, point(minX+float64(float64(i)/float64(minSteps)*span), false))
		}
		return toArr(append(prev, point(maxX, dateMax)))
	}
	next := []pt{point(maxX, dateMax)}
	for i := minSteps - 1; i > 0; i-- {
		next = append(next, point(minX+float64(float64(i)/float64(minSteps)*span), false))
	}
	p0 := prev[0]
	sx := 1 / span
	ymin, ymax := p0.y, p0.y
	for _, q := range next {
		if q.y < ymin {
			ymin = q.y
		}
		if q.y > ymax {
			ymax = q.y
		}
	}
	sy := 1 / (ymax - ymin)
	budget := 64 * MaxSteps / 1000 // generous iteration cap
	for len(next) > 0 && budget > 0 {
		budget--
		p1 := next[len(next)-1]
		mx := (p0.x + p1.x) / 2
		if p0.date || p1.date {
			mx = math.NaN()
		}
		pm := point(mx, false)
		dx := pm.x-p0.x >= stop
		if dx && regressionAngleDelta([2]float64{p0.x, p0.y}, [2]float64{pm.x, pm.y}, [2]float64{p1.x, p1.y}, sx, sy) > regressionMinRadians && len(prev)+len(next) < MaxSteps {
			next = append(next, pm)
		} else {
			p0 = p1
			prev = append(prev, p1)
			next = next[:len(next)-1]
		}
	}
	return toArr(prev)
}

func regressionAngleDelta(p, q, r [2]float64, sx, sy float64) float64 {
	a0 := jsmath.Atan2(sy*(r[1]-p[1]), sx*(r[0]-p[0]))
	a1 := jsmath.Atan2(sy*(q[1]-p[1]), sx*(q[0]-p[0]))
	return math.Abs(a0 - a1)
}
