package raster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mgilbir/aster/internal/budget"
)

const (
	// maxFetchedImages bounds the distinct images one document may have
	// fetched: each is a request to wherever the loader reaches.
	maxFetchedImages = 1024
	// fetchParallelism is how many images are fetched at once.
	fetchParallelism = 8
)

// fetchImages fetches, through load, the image of every <image> element of doc
// whose href is not a data: URI, each distinct href once and several at a
// time. Each fetch's context carries a budget.Budget whose MaxLoadBytes is the
// image size limit, which the loaders read only up to. A fetch that fails
// leaves its image out, and the element is drawn as a broken image is: not at
// all. A fetch that fails over a limit fails them all.
func fetchImages(ctx context.Context, doc *document, load func(context.Context, string) ([]byte, error), lim Limits) (map[string][]byte, error) {
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
		if href == "" || strings.HasPrefix(href, "data:") || strings.HasPrefix(href, "#") || seen[href] {
			continue
		}
		if len(hrefs) == maxFetchedImages {
			return nil, fmt.Errorf("%w: more than %d images to fetch", errLimit, maxFetchedImages)
		}
		seen[href] = true
		hrefs = append(hrefs, href)
	}
	if len(hrefs) == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	data := make([][]byte, len(hrefs))
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		limitErr error
	)
	sem := make(chan struct{}, fetchParallelism)
	for i, href := range hrefs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			// The context's budget tells the loader how much it may read.
			b, err := load(budget.With(ctx, &budget.Budget{MaxLoadBytes: int64(lim.MaxImageBytes)}), href)
			if err == nil && len(b) > lim.MaxImageBytes {
				err = fmt.Errorf("%w: image %q is over %d bytes", errLimit, href, lim.MaxImageBytes)
			}
			if err != nil {
				if errors.Is(err, budget.ErrLimit) {
					mu.Lock()
					if limitErr == nil {
						limitErr = err
						cancel()
					}
					mu.Unlock()
				}
				return
			}
			data[i] = b
		}()
	}
	wg.Wait()
	if limitErr != nil {
		return nil, limitErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(hrefs))
	for i, href := range hrefs {
		if data[i] != nil {
			out[href] = data[i]
		}
	}
	return out, nil
}
