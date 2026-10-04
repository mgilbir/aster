package aster

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/imageref"
)

// The bounds of one image, those of the rasterizer and the PDF writer.
const (
	maxImageBytes  = 32 << 20
	maxImagePixels = 64 << 20
)

// callImages are the images one call fetches through the Loader, as every
// resource of a call is fetched: the href sanitized, then loaded, under the
// call's context. Each is fetched once, whichever stages need it: the SVG
// writer reads the size of one an item has none for, PNG and PDF output draw
// them. An image is read up to the per-image limit, and the images of one call
// together up to the data allowance (64 MiB, or half of WithMemoryLimit). A
// rejected or failed load is remembered too, and leaves the image undrawn, as
// a broken image is.
type callImages struct {
	c         *Converter
	allowance int64

	mu     sync.Mutex
	used   int64
	got    map[string][]byte // by sanitized URL
	failed map[string]error
}

type callImagesKey struct{}

func (c *Converter) newCallImages() *callImages {
	allowance := c.limits().MaxLoadBytes
	if allowance <= 0 {
		allowance = 64 << 20
	}
	return &callImages{c: c, allowance: allowance, got: map[string][]byte{}, failed: map[string]error{}}
}

// imagesFrom is the call's images, from its context (see opContext).
func imagesFrom(ctx context.Context) *callImages {
	ci, _ := ctx.Value(callImagesKey{}).(*callImages)
	return ci
}

// load fetches the image href refers to, for raster.Options.Images and
// svgpdf.Options.Images.
func (ci *callImages) load(ctx context.Context, href string) ([]byte, error) {
	if ci == nil {
		return nil, errors.New("aster: no images for this call")
	}
	u, err := ci.c.cfg.loader.Sanitize(ctx, href)
	if err != nil {
		return nil, err
	}
	return ci.fetch(ctx, u)
}

// fetch loads the sanitized URL u, or answers it from what the call fetched.
// The loader reads up to what the budget in ctx allows, and what is left of
// the allowance.
func (ci *callImages) fetch(ctx context.Context, u string) ([]byte, error) {
	ci.mu.Lock()
	if b, ok := ci.got[u]; ok {
		ci.mu.Unlock()
		return b, nil
	}
	if err, ok := ci.failed[u]; ok {
		ci.mu.Unlock()
		return nil, err
	}
	left := ci.allowance - ci.used
	ci.mu.Unlock()
	if left <= 0 {
		return nil, fmt.Errorf("%w: images over %d bytes in total", ErrLimit, ci.allowance)
	}
	b, err := ci.c.cfg.loader.Load(budget.With(ctx, &budget.Budget{MaxLoadBytes: min(left, budget.From(ctx).LoadLeft())}), u)
	ci.mu.Lock()
	defer ci.mu.Unlock()
	if prev, ok := ci.got[u]; ok { // fetched meanwhile by another stage's fetcher
		return prev, nil
	}
	if err != nil {
		ci.failed[u] = err
		return nil, err
	}
	if ci.used += int64(len(b)); ci.used > ci.allowance {
		return nil, fmt.Errorf("%w: images over %d bytes in total", ErrLimit, ci.allowance)
	}
	ci.got[u] = b
	return b, nil
}

// size is the natural size of the image at the sanitized URL u, read from its
// header, or ok false when it cannot be fetched or is not a PNG, JPEG or GIF
// within the limits.
func (ci *callImages) size(ctx context.Context, u string) (w, h float64, ok bool) {
	if ci == nil {
		return 0, 0, false
	}
	lim := imageref.Limits{MaxBytes: maxImageBytes, MaxPixels: maxImagePixels, Err: ErrLimit}
	var data []byte
	var err error
	if imageref.IsData(u) {
		data, err = imageref.DataURI(u, lim)
	} else {
		data, err = ci.fetch(budget.With(ctx, &budget.Budget{MaxLoadBytes: maxImageBytes}), u)
	}
	if err != nil {
		return 0, 0, false
	}
	cfg, _, err := imageref.Config(data, lim)
	if err != nil {
		return 0, 0, false
	}
	return float64(cfg.Width), float64(cfg.Height), true
}
