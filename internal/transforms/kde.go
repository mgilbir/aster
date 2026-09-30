package transforms

import (
	"context"
	"math"

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
	Resolve  string
	Steps    float64
	MinSteps float64 // default 25
	MaxSteps float64 // default 200
	// As are the output fields; empty strings mean "value" and "density".
	As [2]string
}

// KDE computes kernel density estimates per group. Output tuples are new and
// hold the group-by fields (under their accessor names), then value and
// density.
func KDE(ctx context.Context, source []jsval.Value, p KDEParams) ([]jsval.Value, error) {
	minsteps, maxsteps := stepBounds(p.Steps, p.MinSteps, p.MaxSteps)
	domainValid := true
	var domain *[2]float64
	if p.Extent != nil {
		d := *p.Extent
		domain = &d
	}
	get := p.Field.Get
	if p.Resolve == "shared" {
		if domain == nil {
			lo, hi, ok := Extent(len(source), func(i int) jsval.Value { return get(source[i]) })
			d := [2]float64{math.NaN(), math.NaN()}
			if ok && !lo.IsUndefined() {
				d = [2]float64{jsval.ToNumber(lo), jsval.ToNumber(hi)}
			} else {
				domainValid = false
			}
			domain = &d
		}
		// minsteps = maxsteps = _.steps || maxsteps
		minsteps = maxsteps
	}
	names := make([]string, len(p.GroupBy))
	for i, g := range p.GroupBy {
		names[i] = g.Name
	}
	var out []jsval.Value
	for _, g := range Partition(source, p.GroupBy) {
		vals := make([]jsval.Value, len(g.Tuples))
		for i, t := range g.Tuples {
			vals[i] = get(t)
		}
		kde := NewKernelDensity(vals, p.Bandwidth)
		f := kde.PDF
		if p.Cumulative {
			f = kde.CDF
		}
		scale := 1.0
		if p.Counts {
			scale = float64(len(vals))
		}
		var local [2]float64
		valid := domainValid
		if domain != nil {
			local = *domain
		} else {
			local, valid = valuesExtent(vals)
		}
		if err := reserveOut(ctx, len(out)+int(min(maxsteps, MaxSteps))+1, len(source)); err != nil {
			return nil, err
		}
		pts, err := sampleTuples(ctx, f, local, valid, minsteps, maxsteps, names, g.Dims, p.As, scale)
		if err != nil {
			return nil, err
		}
		out = append(out, pts...)
	}
	return out, nil
}
