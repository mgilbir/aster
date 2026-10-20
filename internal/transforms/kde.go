package transforms

import (
	"context"

	"github.com/mgilbir/aster/internal/jsval"
)

// KDEParams configures the KDE transform.
type KDEParams struct {
	GroupBy []Field
	Field   Field
	// Cumulative outputs the CDF instead of the density.
	Cumulative bool
	// Counts scales the density by the group size (smoothed counts).
	Counts bool
	// Bandwidth of the Gaussian kernel; 0 means estimate per group.
	Bandwidth float64
	// Extent is the domain to plot; nil means each group's data extent (or the
	// overall one when Resolve is "shared").
	Extent *[2]float64
	// Resolve is "independent" (default) or "shared". Shared densities use one
	// domain and exactly MaxSteps (or Steps) uniform samples so they can stack.
	Resolve string
	// ExtentValues, StepsValues: see DensityParams. BandwidthGiven reports a
	// truthy bandwidth, kept as it is (NaN for one that is no number).
	ExtentValues   *[2]jsval.Value
	StepsValues    *StepsSpec
	BandwidthGiven bool
	Steps          float64
	MinSteps       float64 // default 25
	MaxSteps       float64 // default 200
	// As are the output fields; empty strings mean "value" and "density".
	As [2]string
	// AsExact is DensityParams.AsExact.
	AsExact bool
}

// KDE computes kernel density estimates per group. Output tuples are new and
// hold the group-by fields (under their accessor names), then value and
// density.
func KDE(ctx context.Context, source []jsval.Value, p KDEParams) ([]jsval.Value, error) {
	if p.GroupBy == nil && p.Field.Uncallable { // partition: data.map(field)
		return nil, &jsval.Thrown{Name: "TypeError", Msg: "object is not a function"}
	}
	w := jsval.WalkerFor(ctx)
	steps := stepsSpecOf(p.Steps, p.MinSteps, p.MaxSteps)
	if p.StepsValues != nil {
		steps = *p.StepsValues
	}
	var domain *[2]jsval.Value
	switch {
	case p.ExtentValues != nil:
		d := *p.ExtentValues
		domain = &d
	case p.Extent != nil:
		domain = &[2]jsval.Value{jsval.Num(p.Extent[0]), jsval.Num(p.Extent[1])}
	}
	get := p.Field.Get
	if p.Resolve == "shared" {
		if domain == nil {
			// extent(source, _.field): the field's least and greatest
			// values as they are
			lo, hi, ok, err := ExtentOf(w, source, p.Field)
			if err != nil {
				return nil, err
			}
			if !ok {
				lo, hi = jsval.Undefined, jsval.Undefined
			}
			domain = &[2]jsval.Value{lo, hi}
		}
		// minsteps = maxsteps = _.steps || maxsteps
		steps.Min = steps.Max
	}
	names := make([]string, len(p.GroupBy))
	for i, g := range p.GroupBy {
		names[i] = g.Key()
	}
	groups, err := Partition(keysOf(ctx), source, p.GroupBy)
	if err != nil {
		return nil, err
	}
	var out []jsval.Value
	for _, g := range groups {
		vals := make([]jsval.Value, len(g.Tuples))
		for i, t := range g.Tuples {
			vals[i] = get(t)
		}
		kde, err := NewKernelDensity(ctx, vals, p.Bandwidth)
		if err != nil {
			return nil, err
		}
		if p.BandwidthGiven {
			kde.Bandwidth = p.Bandwidth // a truthy bandwidth is kept, NaN or not
		}
		f := kde.PDF
		if p.Cumulative {
			f = kde.CDF
		}
		scale := 1.0
		if p.Counts {
			scale = float64(len(vals))
		}
		var lo, hi jsval.Value
		if domain != nil {
			lo, hi = domain[0], domain[1]
		} else {
			lo, hi = rawExtent(w, vals)
		}
		pts, err := sampleTuples(ctx, f, lo, hi, steps, names, g.Dims, p.As, p.AsExact, scale, len(out))
		if err != nil {
			return nil, err
		}
		out = append(out, pts...)
	}
	return out, nil
}
