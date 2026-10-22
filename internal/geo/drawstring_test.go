package geo

import (
	"fmt"
	"slices"
	"sort"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// reusableRecordCtx is recordCtx as a context a path keeps its stream (and
// the projection its pipeline) for, from one object to the next.
type reusableRecordCtx struct{ recordCtx }

func (*reusableRecordCtx) ReusableContext() {}

// DrawString makes, from one walk, what Draw draws and what String returns:
// for every recorded projection configuration and every geometry of the
// corpus (the vega-datasets topologies, and the synthetic ones that cross the
// antimeridian, the poles and the clip edges), the context records the same
// calls and the text is the same, with ok as String's. The geometries go
// through both paths in the same order, so a pipeline a projection keeps for
// a reusable sink is reused as it would be, for reusable contexts and not;
// with a point radius, a radius function and none.
func TestDrawStringIsDrawAndString(t *testing.T) {
	var g goldenPaths
	loadGolden(t, "paths.json.gz", &g)
	geoms := corpus(t)
	names := make([]string, 0, len(geoms))
	for k := range geoms {
		names = append(names, k)
	}
	sort.Strings(names)
	objects := append([]jsval.Value{}, jsval.Null, jsval.Undefined)
	for _, k := range names {
		objects = append(objects, geoms[k])
	}
	radii := []func(p *Path){
		func(*Path) {},
		func(p *Path) { p.SetPointRadius(2) },
		func(p *Path) {
			p.SetPointRadiusFunc(func(v jsval.Value) float64 { return 1 + float64(len(jsval.AppendJSON(nil, v))%7) })
		},
	}
	checked := 0
	for ci, cfg := range g.Configs {
		if testing.Short() && ci%4 != 0 {
			continue // every projection type of the configurations is among these
		}
		for ri, radius := range radii {
			if testing.Short() && ri != 2 {
				continue
			}
			for _, reusable := range []bool{false, true} {
				two := projFromGolden(t, cfg.Type, cfg.Props).Path()
				one := projFromGolden(t, cfg.Type, cfg.Props).Path()
				radius(two)
				radius(one)
				var c2, c1 PathContext
				var r2, r1 *recordCtx
				if reusable {
					a, b := &reusableRecordCtx{}, &reusableRecordCtx{}
					c2, c1, r2, r1 = a, b, &a.recordCtx, &b.recordCtx
				} else {
					a, b := &recordCtx{}, &recordCtx{}
					c2, c1, r2, r1 = a, b, a, b
				}
				for i, obj := range objects {
					r2.ops, r1.ops = r2.ops[:0], r1.ops[:0]
					var want, got string
					var wantOK, gotOK bool
					// A geometry upstream throws on throws from the walk
					// that draws it, in both.
					wantErr := thrown(func() {
						two.SetContext(c2)
						defer two.SetContext(nil)
						two.Draw(obj)
					})
					if wantErr == "" {
						wantErr = thrown(func() { want, wantOK = two.String(obj) })
					}
					gotErr := thrown(func() {
						one.SetContext(c1)
						defer one.SetContext(nil)
						got, gotOK = one.DrawString(obj)
					})
					if gotErr != wantErr {
						t.Fatalf("%s radius %d reusable %v, object %d: threw %q, want %q", cfg.ID, ri, reusable, i, gotErr, wantErr)
					}
					if !slices.Equal(r1.ops, r2.ops) {
						t.Fatalf("%s radius %d reusable %v, object %d: drew %d calls, want %d (first %v vs %v)", cfg.ID, ri, reusable, i, len(r1.ops), len(r2.ops), head(r1.ops), head(r2.ops))
					}
					if got != want || gotOK != wantOK {
						t.Fatalf("%s radius %d reusable %v, object %d: text %q %v, want %q %v", cfg.ID, ri, reusable, i, trunc(got), gotOK, trunc(want), wantOK)
					}
					checked++
				}
			}
		}
	}
	if checked < 500 {
		t.Fatalf("only %d cases checked", checked)
	}
}

func head(ops []string) []string { return ops[:min(len(ops), 3)] }

// thrown runs f and returns what it panicked with, as text ("" for none).
func thrown(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	f()
	return ""
}
