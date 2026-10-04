package raster

import (
	"context"
	"strings"

	"github.com/mgilbir/aster/internal/imageref"
)

// fetchImages fetches through load the image of every <image> element of doc
// whose href is not a data: URI, each distinct href once (see imageref.Fetch).
func fetchImages(ctx context.Context, doc *document, load imageref.Loader, lim Limits) (map[string][]byte, error) {
	if load == nil {
		return nil, nil
	}
	var hrefs []string
	seen := map[string]bool{}
	stack := []*node{doc.root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		stack = append(stack, n.kids...)
		if n.tag != tagImage {
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
