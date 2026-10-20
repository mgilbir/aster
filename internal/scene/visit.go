package scene

import "sort"

// Ordered returns the mark's items in paint order: items without a z-index in
// document order, followed by z-indexed items sorted by z-index (ties broken by
// document order), as vega's visit() does. The slice is m.Items itself when no
// item has a z-index; callers must not modify it.
func (m *Mark) Ordered() []*Item {
	anyZ := false
	for _, it := range m.Items {
		if it.truthyProp("zindex", it.Zindex) { // if (item.zindex): as given
			anyZ = true
			break
		}
	}
	if !anyZ {
		return m.Items
	}
	out := make([]*Item, 0, len(m.Items))
	type zItem struct {
		it  *Item
		idx int
	}
	var z []zItem
	for i, it := range m.Items {
		if it.truthyProp("zindex", it.Zindex) {
			z = append(z, zItem{it, i})
		} else {
			out = append(out, it)
		}
	}
	sort.SliceStable(z, func(a, b int) bool {
		// a.zindex - b.zindex || a.index - b.index: a NaN difference
		// falls back to the order, as a zero one does.
		if d := z[a].it.Zindex.Val() - z[b].it.Zindex.Val(); d != 0 && d == d {
			return d < 0
		}
		return z[a].idx < z[b].idx
	})
	for _, e := range z {
		out = append(out, e.it)
	}
	return out
}
