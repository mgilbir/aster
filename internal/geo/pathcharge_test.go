package geo

import (
	"context"
	"errors"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// TestPathTextChargedAsBuilt is the shape mark's path text (routed from the
// PR06 review): a geoshape's path data is built whole before the SVG
// writer sees it, and a point writes a circle of its radius, a few hundred
// bytes with a long one, so the points budget alone does not bound the
// text. It is charged to the call's ledger as it grows: 200,000 points of
// a 1e-7 radius (about 40 MB of text) stop at a 4 MiB ledger, and a small
// path under it is charged its length.
func TestPathTextChargedAsBuilt(t *testing.T) {
	pts := make([]jsval.Value, 200_000)
	for i := range pts {
		pts[i] = jsval.ArrOf(jsval.Num(float64(i%360-180)+0.123456789), jsval.Num(float64(i%170-85)+0.987654321))
	}
	obj := jsval.Obj(jsval.ObjectOf("type", jsval.Str("MultiPoint"), "coordinates", jsval.Arr(pts)))
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 4 << 20})
	ctx := budget.WithLedger(budget.With(context.Background(), &budget.Budget{MaxPoints: 1 << 30}), l)
	p, _ := NewProjection("mercator")
	path := p.Path()
	path.SetDigits(-1)
	path.SetPointRadius(1.2345678901234567e-7)
	path.Bind(ctx)
	err := func() (err error) {
		defer func() {
			if s, ok := recover().(*budget.Stop); ok {
				err = s.Err
			}
		}()
		s, _ := path.String(obj)
		t.Logf("built %d bytes", len(s))
		return nil
	}()
	if !errors.Is(err, budget.ErrLimit) {
		t.Errorf("40 MB of path text under a 4 MiB ledger: %v; want a limit error", err)
	}

	l = budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: 4 << 20})
	path = p.Path()
	path.Bind(budget.WithLedger(context.Background(), l))
	before := l.Bytes()
	s, ok := path.String(jsval.Obj(jsval.ObjectOf("type", jsval.Str("LineString"), "coordinates",
		jsval.ArrOf(jsval.ArrOf(jsval.Num(0), jsval.Num(0)), jsval.ArrOf(jsval.Num(10), jsval.Num(10))))))
	if !ok || l.Bytes()-before < int64(len(s)) {
		t.Errorf("a path of %d bytes charged %d", len(s), l.Bytes()-before)
	}
}
