// Package bcache is a cache bounded by the bytes it holds, for state that
// outlives a render and is keyed by what a specification, its data or a font
// chooses: compiled expressions and regular expressions, parsed fonts and
// paths, measured widths. A cache bounded by its number of entries is not a
// bound when an entry can be a megabyte; this one charges each entry its size
// and drops entries not used lately to stay within its byte bound, and keeps
// no entry too large to share it with others.
package bcache

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// Cache is a cache of at most MaxBytes bytes, in two generations of at most
// half of it each, as vega-util's lruCache is in entries: entries are added
// to the current generation; when it is full it becomes the previous one and
// the previous one is dropped whole. A hit in the previous generation copies
// the entry into the current one (lazy promotion), so what is used survives
// the next drop and what is not goes with it.
//
// Hits take no lock. Readers look in a snapshot, an immutable copy of both
// generations published atomically; a hit the snapshot misses is served
// under the lock, and once there have been as many of those as the cache has
// entries the snapshot is rebuilt, so every operation is O(1) amortised. A
// lookup of a key the cache does not hold costs a lookup in the snapshot and
// one under the lock, and never a rebuild. The snapshot is dropped whenever
// an entry leaves the cache, so it holds nothing the bound does not count.
//
// It is safe for concurrent use; a value it returns is shared with every
// other caller and must not be modified.
type Cache[K comparable, V any] struct {
	snap atomic.Pointer[map[K]entry[V]] // nil: none

	mu         sync.Mutex
	curr, prev map[K]entry[V] // nil until used
	currBytes  int64
	prevBytes  int64
	locked     int // hits served under the lock since the snapshot was built
	maxBytes   int64
	genBytes   int64 // what a generation holds at most: maxBytes/2
	maxEntry   int64
}

type entry[V any] struct {
	val  V
	size int64
	prev bool // in the snapshot: the entry is in the previous generation only
}

// New returns a cache holding at most maxBytes bytes, which keeps no entry of
// more than maxEntry bytes (maxEntry <= 0 or past half of maxBytes: half of
// maxBytes, a generation). An entry's bytes are the size given to Put plus
// what the cache itself spends on it (EntryOverhead). The maps are made by
// the first Put: a cache declared at package level allocates nothing until
// it is used.
func New[K comparable, V any](maxBytes, maxEntry int64) *Cache[K, V] {
	maxBytes = max(maxBytes, 0)
	gen := maxBytes / 2
	if maxEntry <= 0 || maxEntry > gen {
		maxEntry = gen
	}
	return &Cache[K, V]{maxBytes: maxBytes, genBytes: gen, maxEntry: maxEntry}
}

// EntryOverhead is what the cache spends on an entry besides the size Put is
// given: the key and the entry in its generation's map and in the snapshot,
// each with the map's own overhead per slot (about a slot's size again). A
// key's own heap memory (a string's bytes) is the caller's to count in the
// size.
func (c *Cache[K, V]) EntryOverhead() int64 {
	var k K
	return 4 * int64(unsafe.Sizeof(k)+unsafe.Sizeof(entry[V]{}))
}

// Get returns the value cached for k. A value found only in the previous
// generation is copied into the current one.
func (c *Cache[K, V]) Get(k K) (V, bool) {
	if s := c.snap.Load(); s != nil {
		// An entry of the previous generation is promoted under the lock.
		if e, ok := (*s)[k]; ok && !e.prev {
			return e.val, true
		}
	}
	return c.getLocked(k)
}

// getLocked is Get past the snapshot.
func (c *Cache[K, V]) getLocked(k K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.curr[k]
	if !ok {
		if e, ok = c.prev[k]; !ok {
			var zero V
			return zero, false
		}
		c.add(k, e)
	}
	if c.locked++; c.locked >= len(c.curr)+len(c.prev) {
		c.publish()
	}
	return e.val, true
}

// publish builds the snapshot from both generations, the current one's
// values over the previous one's. c.mu is held.
func (c *Cache[K, V]) publish() {
	s := make(map[K]entry[V], len(c.curr)+len(c.prev))
	for k, e := range c.prev {
		e.prev = true
		s[k] = e
	}
	for k, e := range c.curr {
		s[k] = e
	}
	c.snap.Store(&s)
	c.locked = 0
}

// unpublish drops the snapshot, when an entry leaves the cache or changes.
// c.mu is held.
func (c *Cache[K, V]) unpublish() {
	if c.snap.Load() != nil {
		c.snap.Store(nil)
	}
	c.locked = 0
}

// Put caches v under k, replacing what k held. size is the bytes v (and k's
// heap memory) hold, as measured or estimated by the caller: it must count
// what the value retains, not its header. An entry larger than the cache's
// maxEntry is not kept, and neither is the value k held before; Put reports
// whether v was cached.
func (c *Cache[K, V]) Put(k K, v V, size int64) bool {
	oh := c.EntryOverhead()
	c.mu.Lock()
	defer c.mu.Unlock()
	if size = max(size, 0); size > c.maxEntry-oh {
		c.drop(k)
		return false
	}
	// A value k holds in the previous generation stays, counted, until that
	// generation goes: lookups find the current one first, and k leaves the
	// current generation only with it (add) or with both (drop).
	if _, ok := c.prev[k]; ok {
		c.unpublish() // the snapshot holds the value replaced
	}
	c.add(k, entry[V]{val: v, size: size + oh})
	return true
}

// add puts e under k in the current generation, first turning the
// generations when it does not fit; e.size is at most genBytes. c.mu is
// held.
func (c *Cache[K, V]) add(k K, e entry[V]) {
	old, replace := c.curr[k]
	if replace {
		c.currBytes -= old.size
		c.unpublish() // the snapshot holds the value replaced
	}
	if c.currBytes+e.size > c.genBytes {
		// The previous generation goes whole, with any value of k, and the
		// current one becomes it, without k. The dropped map is emptied and
		// reused: no allocation per turn.
		if replace {
			delete(c.curr, k)
		}
		clear(c.prev)
		c.prev, c.curr = c.curr, c.prev
		c.prevBytes, c.currBytes = c.currBytes, 0
		c.unpublish()
	}
	if c.curr == nil {
		c.curr = map[K]entry[V]{}
	}
	e.prev = false
	c.curr[k] = e
	c.currBytes += e.size
}

// drop removes k from both generations.
func (c *Cache[K, V]) drop(k K) {
	if e, ok := c.curr[k]; ok {
		delete(c.curr, k)
		c.currBytes -= e.size
		c.unpublish()
	}
	if e, ok := c.prev[k]; ok {
		delete(c.prev, k)
		c.prevBytes -= e.size
		c.unpublish()
	}
}

// Len is the number of entries cached; a value promoted into the current
// generation counts twice until the previous one is dropped.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.curr) + len(c.prev)
}

// Bytes is the bytes the entries cached hold, overhead included.
func (c *Cache[K, V]) Bytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currBytes + c.prevBytes
}

// MaxBytes is the cache's bound.
func (c *Cache[K, V]) MaxBytes() int64 { return c.maxBytes }

// Clear empties the cache.
func (c *Cache[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.curr, c.prev = nil, nil
	c.currBytes, c.prevBytes = 0, 0
	c.unpublish()
}
