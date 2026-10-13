package raster

import "github.com/mgilbir/aster/internal/budget"

// renderCache is a cache that lives for one render, keyed by what the
// document or a font controls (a gradient and an opacity, a glyph, an SVG
// glyph document). What it keeps is bounded by its bytes, not only its
// entries, and charged to the call's ledger (X4): when an entry would take
// it past its bytes or entries, or past what the ledger allows, it is
// emptied first; an entry the ledger cannot hold even then, or larger than
// the whole cache, is not kept, and is rebuilt where it is used again.
type renderCache[K comparable, V any] struct {
	m          map[K]V
	bytes      int64
	maxBytes   int64
	maxEntries int
}

func (c *renderCache[K, V]) get(k K) (V, bool) {
	v, ok := c.m[k]
	return v, ok
}

// put keeps v, of n bytes, under k, within the bounds above.
func (c *renderCache[K, V]) put(l *budget.Ledger, k K, v V, n int64) {
	if n > c.maxBytes {
		return
	}
	if c.m == nil || c.bytes+n > c.maxBytes || len(c.m) >= c.maxEntries || l.Alloc(n) != nil {
		c.drop(l)
		if l.Alloc(n) != nil {
			return
		}
		c.m = map[K]V{}
	}
	c.m[k] = v
	c.bytes += n
}

// drop empties the cache and gives its bytes back to the ledger.
func (c *renderCache[K, V]) drop(l *budget.Ledger) {
	l.Free(c.bytes)
	c.m, c.bytes = nil, 0
}

// The bounds of the per-render caches: far above what a chart uses (a few
// gradients, a few hundred glyphs), and below what a document that makes a
// gradient per element, or a font of huge glyphs, could make them hold.
const (
	maxGradCacheBytes    = 8 << 20 // ~2,000 gradients of a 4 KiB colour table
	maxGradCacheEntries  = 4096
	maxOutlineCacheBytes = 32 << 20
	maxOutlineEntries    = 1 << 14
	maxSVGGlyphBytes     = 64 << 20
)

// gradientBytes is what a cached gradient holds.
const gradientBytes = int64(lutSize*4 + 128)

// pathBytes is what a path holds: its points, its verbs and its header.
func pathBytes(p *path) int64 { return int64(16*cap(p.pts) + cap(p.verbs) + 64) }

// initCaches sets the bounds of the render's caches.
func (r *renderer) initCaches() {
	r.grads = renderCache[gradKey, *gradient]{maxBytes: maxGradCacheBytes, maxEntries: maxGradCacheEntries}
	r.outlines = renderCache[outlineKey, *path]{maxBytes: maxOutlineCacheBytes, maxEntries: maxOutlineEntries}
	r.svgGlyphs = renderCache[colourImageKey, *document]{maxBytes: maxSVGGlyphBytes, maxEntries: maxSVGGlyphDocs}
}

// dropCaches gives back what the caches are charged.
func (r *renderer) dropCaches() {
	r.grads.drop(r.ledger)
	r.outlines.drop(r.ledger)
	r.svgGlyphs.drop(r.ledger)
}
