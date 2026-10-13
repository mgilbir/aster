package vegalite

// kidList is a dataflow node's children: upstream's `_children` array, with
// the operations the optimizer uses on it (indexOf, splice at an index, push,
// iteration by a live index). The order is what upstream's array holds after
// the same sequence of operations, so passes that iterate while they splice
// see the same nodes upstream's do.
//
// Upstream's indexOf and splice are linear, so the passes that remove or
// merge each of n siblings are quadratic: a spec with a hundred thousand
// layers or selections ran for minutes in them. A few children (the usual
// case) stay in a flat slice. Past manyKids the list moves into blocks of at
// most kidBlockMax nodes, with a Fenwick tree over the block lengths and an
// index from node to block, so position, membership, insertion and removal
// cost O(log n + kidBlockMax) and the same sequence of operations still
// yields the same order (ADR 0011).
type kidList struct {
	// flat holds the children while there are few (blocks == nil).
	flat []dfNode
	// blocks hold them, in order, once there are many; fen is a Fenwick
	// tree over the blocks' lengths, where maps a child to its block.
	blocks []*kidBlock
	fen    []int
	where  map[dfNode]*kidBlock
	n      int
	// snap caches the children in order for iteration in blocks mode; nil
	// when a change made it stale. A snapshot is never written once handed
	// out, so a caller may keep iterating it while the list changes.
	snap []dfNode
}

type kidBlock struct {
	items []dfNode
	// pos is the block's index in blocks.
	pos int
}

// manyKids is the child count from which the list leaves the flat slice.
const manyKids = 32

// kidBlockMax is the size at which a block splits in two.
const kidBlockMax = 64

func (k *kidList) len() int {
	if k.blocks == nil {
		return len(k.flat)
	}
	return k.n
}

// all returns the children in order. In flat mode it is the live slice (as
// upstream's for..of sees it); in blocks mode a snapshot that later changes
// do not touch. Callers that change the list while iterating copy it or
// iterate by index, as upstream does.
func (k *kidList) all() []dfNode {
	if k.blocks == nil {
		return k.flat
	}
	if k.snap == nil {
		k.snap = make([]dfNode, 0, k.n)
		for _, b := range k.blocks {
			k.snap = append(k.snap, b.items...)
		}
	}
	return k.snap
}

// at is the child at index i (0 <= i < len).
func (k *kidList) at(i int) dfNode {
	if k.blocks == nil {
		return k.flat[i]
	}
	b, off := k.find(i)
	return k.blocks[b].items[off]
}

func (k *kidList) has(c dfNode) bool {
	if k.blocks == nil {
		return k.indexOf(c) >= 0
	}
	_, ok := k.where[c]
	return ok
}

// indexOf is Array.prototype.indexOf: the child's index, or -1.
func (k *kidList) indexOf(c dfNode) int {
	if k.blocks == nil {
		for i, x := range k.flat {
			if x == c {
				return i
			}
		}
		return -1
	}
	b, ok := k.where[c]
	if !ok {
		return -1
	}
	for j, x := range b.items {
		if x == c {
			return k.prefix(b.pos) + j
		}
	}
	return -1
}

// insert puts c at index i (0 <= i <= len), shifting the rest up: splice(i, 0, c).
func (k *kidList) insert(i int, c dfNode) {
	if k.blocks == nil {
		if len(k.flat) < manyKids {
			k.flat = append(k.flat, nil)
			copy(k.flat[i+1:], k.flat[i:])
			k.flat[i] = c
			return
		}
		k.toBlocks()
	}
	k.snap = nil
	var b *kidBlock
	off := 0
	if i == k.n {
		b = k.blocks[len(k.blocks)-1]
		off = len(b.items)
	} else {
		bi, o := k.find(i)
		b, off = k.blocks[bi], o
	}
	b.items = append(b.items, nil)
	copy(b.items[off+1:], b.items[off:])
	b.items[off] = c
	k.where[c] = b
	k.n++
	k.fenAdd(b.pos, 1)
	if len(b.items) > kidBlockMax {
		k.split(b)
	}
}

// removeAt removes the child at index i (0 <= i < len): splice(i, 1).
func (k *kidList) removeAt(i int) {
	if k.blocks == nil {
		k.flat = append(k.flat[:i], k.flat[i+1:]...)
		return
	}
	k.snap = nil
	bi, off := k.find(i)
	b := k.blocks[bi]
	delete(k.where, b.items[off])
	b.items = append(b.items[:off], b.items[off+1:]...)
	k.n--
	k.fenAdd(bi, -1)
}

// clear empties the list.
func (k *kidList) clear() { *k = kidList{} }

func (k *kidList) toBlocks() {
	k.where = make(map[dfNode]*kidBlock, 2*len(k.flat))
	k.n = len(k.flat)
	k.blocks = nil
	for start := 0; start < len(k.flat) || start == 0; start += kidBlockMax / 2 {
		end := min(start+kidBlockMax/2, len(k.flat))
		b := &kidBlock{items: append(make([]dfNode, 0, kidBlockMax+1), k.flat[start:end]...), pos: len(k.blocks)}
		for _, c := range b.items {
			k.where[c] = b
		}
		k.blocks = append(k.blocks, b)
	}
	k.flat = nil
	k.rebuildFen()
}

// split halves an overfull block. Moving the blocks after it and rebuilding
// the tree costs O(blocks); it happens at most once per kidBlockMax/2
// insertions into a block, so it is amortised.
func (k *kidList) split(b *kidBlock) {
	half := len(b.items) / 2
	nb := &kidBlock{items: append(make([]dfNode, 0, kidBlockMax+1), b.items[half:]...)}
	clear(b.items[half:])
	b.items = b.items[:half]
	for _, c := range nb.items {
		k.where[c] = nb
	}
	k.blocks = append(k.blocks, nil)
	copy(k.blocks[b.pos+2:], k.blocks[b.pos+1:])
	k.blocks[b.pos+1] = nb
	for j := b.pos + 1; j < len(k.blocks); j++ {
		k.blocks[j].pos = j
	}
	k.rebuildFen()
}

func (k *kidList) rebuildFen() {
	k.fen = make([]int, len(k.blocks)+1)
	for j, b := range k.blocks {
		k.fen[j+1] += len(b.items)
		if p := j + 1 + (j+1)&-(j+1); p < len(k.fen) {
			k.fen[p] += k.fen[j+1]
		}
	}
}

func (k *kidList) fenAdd(block, d int) {
	for p := block + 1; p < len(k.fen); p += p & -p {
		k.fen[p] += d
	}
}

// prefix is the number of children in the blocks before block.
func (k *kidList) prefix(block int) int {
	s := 0
	for p := block; p > 0; p -= p & -p {
		s += k.fen[p]
	}
	return s
}

// find returns the block holding index i and the offset in it.
func (k *kidList) find(i int) (block, off int) {
	pos, rem := 0, i
	step := 1
	for step*2 < len(k.fen) {
		step *= 2
	}
	for ; step > 0; step /= 2 {
		if next := pos + step; next < len(k.fen) && k.fen[next] <= rem {
			pos = next
			rem -= k.fen[next]
		}
	}
	// pos blocks hold at most i children; skip empty blocks after them.
	for len(k.blocks[pos].items) <= rem {
		rem -= len(k.blocks[pos].items)
		pos++
	}
	return pos, rem
}
