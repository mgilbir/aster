package scene

import "sort"

// Ordered returns the mark's items in paint order: items without a z-index in
// document order, followed by z-indexed items sorted by z-index (ties broken by
// document order), as vega's visit() does. The slice is m.Items itself when no
// item has a z-index; callers must not modify it.
func (m *Mark) Ordered() []*Item {
	anyZ := false
	for _, it := range m.Items {
		if it.Zindex.Truthy() {
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
		if it.Zindex.Truthy() {
			z = append(z, zItem{it, i})
		} else {
			out = append(out, it)
		}
	}
	sort.SliceStable(z, func(a, b int) bool {
		za, zb := z[a].it.Zindex.Val(), z[b].it.Zindex.Val()
		if za != zb {
			return za < zb
		}
		return z[a].idx < z[b].idx
	})
	for _, e := range z {
		out = append(out, e.it)
	}
	return out
}
