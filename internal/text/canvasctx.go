package text

import "sync"

// CanvasContext measures text the way vega-scenegraph does in node: through
// one canvas context whose font is only changed when the font string parses
// (see ValidCanvasFont), behind a width cache keyed on the font string and
// the text. A font the context rejects therefore measures with the font it
// held before, which is the last valid one a cache miss set; a cache hit does
// not touch the context. The context starts at the canvas default, 10px
// sans-serif.
//
// Upstream's context and cache live as long as the process; one
// CanvasContext per render is the same in a fresh process.
type CanvasContext struct {
	m *Measurer

	mu    sync.Mutex
	font  string
	cache lruCache
	// valid remembers which font strings ValidCanvasFont accepted: a chart
	// measures thousands of texts in a few fonts, and parsing the font is
	// dearer than finding the width.
	valid map[string]bool
}

// maxValidFonts bounds the memory of valid; a specification can name any
// number of fonts.
const (
	maxValidFonts   = 256
	maxValidFontLen = 256
)

// canvasDefaultFont is a new canvas context's font.
const canvasDefaultFont = "10px sans-serif"

// NewCanvasContext returns a context that measures with m.
func NewCanvasContext(m *Measurer) *CanvasContext {
	return &CanvasContext{m: m, font: canvasDefaultFont, cache: newLRUCache(10000)}
}

// MeasureText is textMetrics.measureWidth's _measureWidth(text, font).
func (c *CanvasContext) MeasureText(text, cssFont string) float64 {
	key := "(" + cssFont + ") " + text
	c.mu.Lock()
	defer c.mu.Unlock()
	if w, ok := c.cache.get(key); ok {
		return w
	}
	ok, seen := c.valid[cssFont]
	if !seen {
		ok = ValidCanvasFont(cssFont)
		if len(cssFont) <= maxValidFontLen {
			if c.valid == nil || len(c.valid) >= maxValidFonts {
				c.valid = make(map[string]bool)
			}
			c.valid[cssFont] = ok
		}
	}
	if ok {
		c.font = cssFont
	}
	w := c.m.MeasureText(text, c.font)
	c.cache.set(key, w)
	return w
}

// lruCache is vega-util's lruCache: two generations of at most max entries,
// the older one dropped when the newer fills up.
type lruCache struct {
	curr, prev map[string]float64
	size, max  int
}

func newLRUCache(max int) lruCache {
	return lruCache{curr: map[string]float64{}, prev: map[string]float64{}, max: max}
}

func (l *lruCache) update(key string, v float64) {
	if l.size++; l.size > l.max {
		l.prev, l.curr, l.size = l.curr, map[string]float64{}, 1
	}
	l.curr[key] = v
}

func (l *lruCache) get(key string) (float64, bool) {
	if v, ok := l.curr[key]; ok {
		return v, true
	}
	if v, ok := l.prev[key]; ok {
		l.update(key, v)
		return v, true
	}
	return 0, false
}

func (l *lruCache) set(key string, v float64) {
	if _, ok := l.curr[key]; ok {
		l.curr[key] = v
		return
	}
	l.update(key, v)
}
