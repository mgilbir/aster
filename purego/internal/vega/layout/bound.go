package layout

import "github.com/mgilbir/aster/purego/internal/scene"

// Bound is vega-view-transforms' Bound: it recomputes the bounds of a mark and
// its items and clips them to the group when the mark is clipped. Child marks
// of group items must already be bounded (scene.Bounder.BoundTree bounds a
// whole subtree bottom-up). It exists so the runtime can wire the operator
// next to ViewLayout and Overlap; the geometry lives in the scene package.
func Bound(bd *scene.Bounder, mark *scene.Mark) error { return bd.BoundMark(mark) }
