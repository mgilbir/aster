package raster

import (
	"context"
	"strings"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/imageref"
)

// fetchImages fetches through load the image of every <image> and feImage
// element of doc whose href is not a data: URI or a reference into the
// document, each distinct href once (see imageref.Fetch).
func fetchImages(ctx context.Context, l *budget.Ledger, doc *document, load imageref.Loader, lim Limits) (map[string][]byte, error) {
	if load == nil {
		return nil, nil
	}
	var hrefs []string
	seen := map[string]bool{}
	// The walk's stack, as deep as the tree is wide, is charged as it
	// grows; the hrefs stop one past the most Fetch takes.
	rs := reservation{l: l}
	defer rs.release()
	m := budget.NewMeter(ctx)
	stack := []*node{doc.root}
	for len(stack) > 0 && len(hrefs) <= imageref.MaxFetched {
		if err := m.Work(1); err != nil {
			return nil, err
		}
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if len(stack)+len(n.kids) > cap(stack) {
			c := max(2*cap(stack), len(stack)+len(n.kids))
			if err := rs.take(int64(c) * 8); err != nil {
				return nil, overLedger("finding the SVG's images", err)
			}
			stack = append(make([]*node, 0, c), stack...)
		}
		stack = append(stack, n.kids...)
		if n.tag != tagImage && n.tag != tagFeImage {
			continue
		}
		href := strings.TrimSpace(n.str(aHref))
		if href == "" || imageref.IsData(href) || strings.HasPrefix(href, "#") || seen[href] {
			continue
		}
		seen[href] = true
		hrefs = append(hrefs, href)
	}
	return imageref.Fetch(ctx, hrefs, load, lim.imageLimits())
}
