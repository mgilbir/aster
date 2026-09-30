package geo

// clipBuffer collects the lines a line clipper emits so a polygon's rings can be
// rejoined afterwards. All points live in one arena slice; a line is a window of
// it, so buffering a ring costs no allocation once the arena has grown. The
// windows returned by result stay valid until reset (next keeps them valid).
type clipBuffer struct {
	pts    []cpoint
	starts []int      // start offset of each line in pts
	views  [][]cpoint // result's return value, reused
	joined []cpoint   // rejoin's product, when the first and last lines merged
	hasJ   bool
}

func (b *clipBuffer) Point(x, y float64) { b.pts = append(b.pts, cpoint{x, y, 0}) }

func (b *clipBuffer) pointM(x, y, m float64) { b.pts = append(b.pts, cpoint{x, y, m}) }

func (b *clipBuffer) LineStart()    { b.starts = append(b.starts, len(b.pts)) }
func (b *clipBuffer) LineEnd()      {}
func (b *clipBuffer) PolygonStart() {}
func (b *clipBuffer) PolygonEnd()   {}
func (b *clipBuffer) Sphere()       {}

// rejoin joins the last line to the first when a ring's first and last
// segments belong together: lines become [middle..., last+first].
func (b *clipBuffer) rejoin() {
	n := len(b.starts)
	if n <= 1 {
		return
	}
	lines := b.lines()
	last, first := lines[n-1], lines[0]
	joined := make([]cpoint, 0, len(last)+len(first))
	joined = append(append(joined, last...), first...)
	b.joined, b.hasJ = joined, true
	// Reorder: drop the first line, replace the last by the joined one. The
	// arena keeps the originals; only the bookkeeping changes.
	b.starts = b.starts[1:n]
}

// lines returns the buffered lines as windows of the arena.
func (b *clipBuffer) lines() [][]cpoint {
	n := len(b.starts)
	b.views = b.views[:0]
	for i := 0; i < n; i++ {
		end := len(b.pts)
		if i+1 < n {
			end = b.starts[i+1]
		}
		b.views = append(b.views, b.pts[b.starts[i]:end:end])
	}
	return b.views
}

// result returns the buffered lines (after a rejoin, with the last replaced by
// the merged line). The buffer must not be written to until reset is called.
func (b *clipBuffer) result() [][]cpoint {
	lines := b.lines()
	if b.hasJ {
		lines[len(lines)-1] = b.joined
	}
	return lines
}

// next forgets the current lines (after result has been consumed) but keeps
// the points, so windows handed out earlier stay valid; reset empties the arena
// too, once no window is needed any more.
func (b *clipBuffer) next() {
	b.starts = b.starts[:0]
	b.joined, b.hasJ = nil, false
}

func (b *clipBuffer) reset() {
	b.next()
	b.pts = b.pts[:0]
}
