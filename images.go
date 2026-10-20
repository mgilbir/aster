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

// callImages are the images of one call: each URL the specification (or the
// SVG given to SVGToPNG and SVGToPDF) names, what the Loader's Sanitize made
// of it, and the bytes its Load returned or the error that refused it. It is
// the one table every stage of the call consults (C35), so that the Loader
// sees each resource once per call, whichever outputs draw it:
//
//   - the SVG writer resolves an image mark's URL, writes what Sanitize
//     returned as the <image>'s href, and fetches the image when the mark
//     has no size to read it from;
//   - PNG and PDF output, reading that SVG back, look the href up and draw
//     what the SVG stage resolved, rather than sanitize their own answer a
//     second time (a rewriting Loader would see it, a Loader of logical
//     names would refuse it);
//   - an href no stage of the call resolved, as in SVG given to SVGToPNG,
//     is resolved then, once.
//
// Every failure is reported once, as the URL the input names. An image is
// read up to the per-image limit, and the data and images of one call
// together up to its loaded-bytes allowance (C80). A link (an item's href)
// is resolved once per URL too, in a table of its own.
//
// The allowance is charged as the bytes are read, not after the fact (C19):
// PNG and PDF output fetch several images at once (imageref.Fetch), and each
// read up to what was left when it started, so eight of them read together
// several times the allowance. Each Load's budget now shares the call's
// allowance (budget.Allowance), which the package's loaders charge a chunk
// at a time as they read, so the fetches together stop where it ends, all of
// them in flight at once; what a Loader of the caller's own returns, unread
// by any charge, is charged once it returns, before it is decoded. Bytes
// charged are kept, even those of a load that then fails, so a call goes
// past the allowance only when the bytes its loads read do, whatever order
// they ran in.
type callImages struct {
	c *Converter

	mu sync.Mutex
	// allowance is what the call may load, data and images together (see
	// chargeData), charged as it is read.
	allowance *budget.Allowance
	byURL     map[string]*resource // by the URL the input names
	byHref    map[string]*resource // by the href the SVG stage wrote
	links     map[string]linkResolution
	fetched   map[string]*fetch // by sanitized URL
}

// resource is one image URL of the call, resolved.
type resource struct {
	url string // as the input names it
	u   string // what Sanitize returned; url itself for an inline image
	err error  // Sanitize's refusal
}

// fetch is what one Load of a sanitized URL gave.
type fetch struct {
	done chan struct{} // closed once data and err are set
	data []byte
	err  error
}

type linkResolution struct {
	href string
	err  error
}

type callImagesKey struct{}

func (c *Converter) newCallImages() *callImages {
	return &callImages{c: c, allowance: budget.NewAllowance(c.lim.vega.MaxLoadBytes),
		byURL: map[string]*resource{}, byHref: map[string]*resource{}, links: map[string]linkResolution{}, fetched: map[string]*fetch{}}
}

// imagesFrom is the call's images, from its context (see opContext).
func imagesFrom(ctx context.Context) *callImages {
	ci, _ := ctx.Value(callImagesKey{}).(*callImages)
	return ci
}

// chargeData counts the n bytes the call's data loaded against the
// allowance images draw from, once the Vega run that loaded them is over.
func (ci *callImages) chargeData(n int64) {
	// Within the allowance already: the Vega run's own budget bounded it.
	ci.allowance.Force(n)
}

// entryBytes is what one table entry is charged to the call's ledger: its
// strings and the maps' own share of an entry.
func entryBytes(s ...string) int64 {
	n := int64(96)
	for _, x := range s {
		n += int64(len(x))
	}
	return n
}

// resolve is the image at url resolved for this call: sanitized by the Loader
// the first time it is asked for, or taken as it is when it is a data: URI of
// an image the engine draws, which is part of the document, not a resource.
func (ci *callImages) resolve(ctx context.Context, url string) (*resource, error) {
	ci.mu.Lock()
	r, ok := ci.byURL[url]
	ci.mu.Unlock()
	if ok {
		return r, nil
	}
	r = &resource{url: url, u: url}
	if !imageref.InlineImage(url) {
		r.u, r.err = ci.c.loader.Sanitize(ctx, url)
	}
	if err := budget.LedgerFrom(ctx).Alloc(entryBytes(r.url, r.u)); err != nil {
		return nil, err
	}
	ci.mu.Lock()
	defer ci.mu.Unlock()
	if prev, ok := ci.byURL[url]; ok { // resolved meanwhile on another goroutine
		return prev, nil
	}
	ci.byURL[url] = r
	return r, nil
}

// written records that the SVG stage wrote href for r, so that PNG and PDF
// output of the same call draw r from it.
func (ci *callImages) written(ctx context.Context, href string, r *resource) error {
	ci.mu.Lock()
	_, ok := ci.byHref[href]
	ci.mu.Unlock()
	if ok {
		return nil
	}
	if err := budget.LedgerFrom(ctx).Alloc(entryBytes(href)); err != nil {
		return err
	}
	ci.mu.Lock()
	ci.byHref[href] = r
	ci.mu.Unlock()
	return nil
}

// forHref is the resource an <image> href stands for: the one the SVG stage
// wrote it for, or else href resolved as a URL of its own.
func (ci *callImages) forHref(ctx context.Context, href string) (*resource, error) {
	ci.mu.Lock()
	r, ok := ci.byHref[href]
	ci.mu.Unlock()
	if ok {
		return r, nil
	}
	return ci.resolve(ctx, href)
}

// url is the URL the input names for an <image> href, which reports use.
func (ci *callImages) url(href string) string {
	if ci == nil {
		return href
	}
	ci.mu.Lock()
	defer ci.mu.Unlock()
	if r, ok := ci.byHref[href]; ok {
		return r.url
	}
	return href
}

// load fetches the image an <image> href refers to, for raster.Options.Images
// and svgpdf.Options.Images: each a failure the call reports once, as the URL
// the input names.
func (ci *callImages) load(ctx context.Context, href string) ([]byte, error) {
	if ci == nil {
		return nil, errors.New("aster: no images for this call")
	}
	r, err := ci.forHref(ctx, href)
	if err != nil {
		return nil, err
	}
	if r.err != nil {
		reportFrom(ctx).image(r.url, r.err)
		return nil, r.err
	}
	return ci.fetch(ctx, r)
}

// imageError is raster.Options.ImageError and svgpdf.Options.ImageError: a
// stage's report of an image it could not draw, under the URL the input
// names, so that a failure the fetch already reported is not reported again.
func (ci *callImages) imageError(ctx context.Context) func(href string, err error) {
	rep := reportFrom(ctx)
	return func(href string, err error) { rep.image(ci.url(href), err) }
}

// fetch returns the bytes of r: loaded once per sanitized URL for the call,
// up to the per-image limit and what is left of the call's allowance. A
// failure is reported as r's URL.
func (ci *callImages) fetch(ctx context.Context, r *resource) ([]byte, error) {
	if imageref.IsData(r.u) {
		// Decoded in place, as PNG and PDF output decode a data: href.
		data, err := imageref.DataURI(r.u, ci.limits())
		if err != nil {
			reportFrom(ctx).image(r.url, err)
		}
		return data, err
	}
	ci.mu.Lock()
	f, ok := ci.fetched[r.u]
	if !ok {
		f = &fetch{done: make(chan struct{})}
		ci.fetched[r.u] = f
	}
	ci.mu.Unlock()
	if !ok {
		f.data, f.err = ci.loadBytes(ctx, r)
		close(f.done)
	} else {
		select {
		case <-f.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil && ctx.Err() == nil { // a canceled call is not a failed image
		reportFrom(ctx).image(r.url, f.err)
	}
	return f.data, f.err
}

// loadBytes is one Load of r, the first for its sanitized URL in the call,
// charged to the call's allowance as it reads (see callImages).
func (ci *callImages) loadBytes(ctx context.Context, r *resource) ([]byte, error) {
	if ci.allowance.Left() <= 0 {
		return nil, ci.overAllowance(r)
	}
	bud := &budget.Budget{MaxLoadBytes: maxImageBytes, Shared: ci.allowance}
	b, err := ci.c.loader.Load(budget.With(ctx, bud), r.u)
	if err != nil {
		if errors.Is(err, budget.ErrLimit) && !bud.SharedBound() {
			// The Loader read up to what it was given, which was the
			// per-image limit, not what its message says (C88).
			return nil, fmt.Errorf("aster: image %q is over %d bytes, the limit of one image: %w", r.url, maxImageBytes, ErrLimit)
		}
		return nil, err
	}
	if int64(len(b)) > maxImageBytes {
		return nil, fmt.Errorf("aster: image %q is over %d bytes, the limit of one image: %w", r.url, maxImageBytes, ErrLimit)
	}
	// What the Loader returned beyond what it charged as it read: all of it
	// for a Loader of the caller's own, which does not read the budget.
	if extra := int64(len(b)) - bud.Streamed(); extra > 0 && !ci.allowance.Take(extra) {
		return nil, ci.overAllowance(r)
	}
	return b, nil
}

func (ci *callImages) overAllowance(r *resource) error {
	return fmt.Errorf("aster: image %q: %w: the data and images of the call are over %d bytes (loaded bytes; WithMemoryLimit raises it)", r.url, ErrLimit, ci.allowance.Max())
}

// limits are the bounds of one image, as every stage applies them.
func (ci *callImages) limits() imageref.Limits {
	return imageref.Limits{MaxBytes: maxImageBytes, MaxPixels: ci.c.imagePixels(), Err: ErrLimit}
}

// size is the natural size of the image r, read from its header, or ok false
// when it cannot be fetched or is not a PNG, JPEG or GIF within the limits.
// A failure is reported, and one over a limit ends the call (C34) once the
// SVG stage is over (see callReport.image).
func (ci *callImages) size(ctx context.Context, r *resource) (w, h float64, ok bool) {
	if ci == nil {
		return 0, 0, false
	}
	data, err := ci.fetch(ctx, r)
	if err != nil {
		return 0, 0, false // fetch reported it
	}
	cfg, _, err := imageref.Config(data, ci.limits())
	if err != nil {
		reportFrom(ctx).image(r.url, err)
		return 0, 0, false
	}
	return float64(cfg.Width), float64(cfg.Height), true
}

// link is what the Loader's Sanitize made of a link's URL, once per URL for
// the call.
func (ci *callImages) link(ctx context.Context, uri string) (string, error) {
	ci.mu.Lock()
	l, ok := ci.links[uri]
	ci.mu.Unlock()
	if ok {
		return l.href, l.err
	}
	l.href, l.err = ci.c.loader.Sanitize(ctx, uri)
	if err := budget.LedgerFrom(ctx).Alloc(entryBytes(uri, l.href)); err != nil {
		return "", err
	}
	ci.mu.Lock()
	ci.links[uri] = l
	ci.mu.Unlock()
	return l.href, l.err
}

// imagePixels bounds the pixels of one image every stage decodes (the SVG
// writer reading a size, PNG and PDF output): a sixteenth of WithMemoryLimit
// as the rasterizer has it, or maxImagePixels without one, so that no stage
// admits an image another refuses.
func (c *Converter) imagePixels() int {
	if p := c.lim.raster.MaxImagePixels; p > 0 {
		return p
	}
	return maxImagePixels
}
