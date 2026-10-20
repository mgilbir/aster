package layout

import (
	"fmt"
	"math"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scene"
)

// jsArray is a JavaScript array as gridLayout reads and writes it: by
// property key, so an index that is a fraction, a negative number or a text
// ("03") is a key of its own, and an index never written reads undefined.
type jsArray map[string]jsval.Value

func indexKey(k jsval.Value) string {
	s, _ := k.PropertyKey(64)
	return s
}

func (a jsArray) get(k jsval.Value) jsval.Value {
	if v, ok := a[indexKey(k)]; ok {
		return v
	}
	return jsval.Undefined
}

func (a jsArray) set(k, v jsval.Value) { a[indexKey(k)] = v }

// gridLayoutExact is gridLayout for a `columns` given as text, a list or an
// object, line by line as upstream's runs: ncols is that value, so
// `Array(ncols)` is an array of the one value, `i < ncols`, `i % ncols` and
// `i / ncols` read it as a number, and `i += ncols` and `m = i + ncols`
// concatenate. The extents and offsets are indexed by whatever key that
// arithmetic produces (jsArray); a cell index that is no array key reads an
// undefined group, whose y is upstream's TypeError. A NaN count of rows is
// Array(NaN)'s RangeError. Loops the text keeps from ending stop at
// loopGuard's bound.
// maxDenseArray is the length V8 stops growing a dense array one element at
// a time (the loop that writes index 134217728 throws).
const maxDenseArray = 1 << 27

func gridLayoutExact(groups []*scene.Item, opt *gridOptions) (scene.Bounds, error) {
	n := len(groups)
	bounds := newBoundsSet(0, 0, 0, 0)
	ncols := opt.columnsRaw
	N := num(ncols)
	nrows := 1.0
	if !(N <= 0) {
		nrows = math.Ceil(float64(n) / N)
	}
	// yExtent = Array(nrows): a number that is no array length throws
	if nrows != math.Floor(nrows) || nrows < 0 || nrows > maxArrayLength || math.IsNaN(nrows) {
		return bounds, ErrInvalidArrayLength
	}
	// for (i=0; i<ncols; ++i) xExtent[i] = 0 grows the one-element array
	// xExtent one element at a time, which V8 refuses past 2^27 elements
	if N > maxDenseArray {
		return bounds, ErrInvalidArrayLength
	}
	if n == 0 {
		return bounds, nil
	}
	flushBounds := opt.bounds == flush
	alignCol, alignRow := opt.alignCol, opt.alignRow
	wk := groups[0].Walker()
	add := func(a, b jsval.Value) jsval.Value { return jsAdd(wk, a, b) }
	numv := func(f float64) jsval.Value { return jsval.Num(f) }
	nv := numv(float64(n))
	zero := numv(0)
	guard := loopGuard{max: gridLoopPerCell*n + gridLoopBase}
	mod := func(i int) float64 { return math.Mod(float64(i), N) }

	xOffset, yOffset := jsArray{}, jsArray{}
	xExtent := jsArray{"0": ncols} // Array(ncols) of a value that is no number
	yExtent := jsArray{}
	dx, dy := make([]float64, n), make([]float64, n)
	boxes := make([]scene.Bounds, n)
	var xMax, yMax float64

	// for (i=0; i<ncols; ++i) xExtent[i] = 0: keys past n are never read
	for i := 0; float64(i) < N && i < n; i++ {
		xExtent.set(numv(float64(i)), zero)
	}
	// for (i=0; i<nrows; ++i) yExtent[i] = 0, read lazily: an integer key
	// below nrows not yet written is 0
	yExt := func(k jsval.Value) jsval.Value {
		if v, ok := yExtent[indexKey(k)]; ok {
			return v
		}
		if f := num(k); k.IsNum() && f >= 0 && f < nrows && f == math.Floor(f) {
			return zero
		}
		return jsval.Undefined
	}

	// determine offsets for each group
	for i, g := range groups {
		b := gridBox(g, flushBounds)
		boxes[i] = b
		setPos(g, "x", orZeroV(prop(g, "x")))
		setPos(g, "y", orZeroV(prop(g, "y")))
		c := numv(mod(i))
		r := numv(float64(toInt32(float64(i) / N)))
		px, py := math.Ceil(b.X2), math.Ceil(b.Y2)
		xMax = jsMax(xMax, px)
		yMax = jsMax(yMax, py)
		xExtent.set(c, numv(jsMax(num(xExtent.get(c)), px)))
		yExtent.set(r, numv(jsMax(num(yExt(r)), py)))
		xOffset.set(numv(float64(i)), add(opt.padCol, numv(offsetValue(b.X1))))
		yOffset.set(numv(float64(i)), add(opt.padRow, numv(offsetValue(b.Y1))))
	}

	// set initial alignment offsets
	for i := 0; i < n; i++ {
		if mod(i) == 0 {
			xOffset.set(numv(float64(i)), zero)
		}
		if float64(i) < N {
			yOffset.set(numv(float64(i)), zero)
		}
	}

	// `for (i=c; i<n; i += ncols)`
	cells := func(c int, f func(i jsval.Value)) error {
		for i := numv(float64(c)); jsLess(i, nv); i = add(i, ncols) {
			if err := guard.step(); err != nil {
				return err
			}
			f(i)
		}
		return nil
	}

	// enforce column alignment constraints (columns past n hold no cell)
	switch alignCol {
	case each:
		for c := 1; float64(c) < N && c < n; c++ {
			offset := zero
			if err := cells(c, func(i jsval.Value) {
				if xo := xOffset.get(i); jsLess(offset, xo) {
					offset = xo
				}
			}); err != nil {
				return bounds, err
			}
			ext := xExtent.get(numv(float64(c - 1)))
			if err := cells(c, func(i jsval.Value) { xOffset.set(i, add(offset, ext)) }); err != nil {
				return bounds, err
			}
		}
	case all:
		offset := zero
		for i := 0; i < n; i++ {
			if m := mod(i); m != 0 && !math.IsNaN(m) {
				if xo := xOffset.get(numv(float64(i))); jsLess(offset, xo) {
					offset = xo
				}
			}
		}
		for i := 0; i < n; i++ {
			if m := mod(i); m != 0 && !math.IsNaN(m) {
				xOffset.set(numv(float64(i)), add(offset, numv(xMax)))
			}
		}
	default:
		alignCol = ""
		for c := 1; float64(c) < N && c < n; c++ {
			ext := xExtent.get(numv(float64(c - 1)))
			if err := cells(c, func(i jsval.Value) { xOffset.set(i, add(xOffset.get(i), ext)) }); err != nil {
				return bounds, err
			}
		}
	}

	// `for (i=r*ncols, m=i+ncols; i<m; ++i)`
	row := func(r int, f func(i jsval.Value)) error {
		start := float64(r) * N
		m := add(numv(start), ncols)
		for i := start; jsLess(numv(i), m); i++ {
			if err := guard.step(); err != nil {
				return err
			}
			f(numv(i))
		}
		return nil
	}
	// enforce row alignment constraints
	switch alignRow {
	case each:
		for r := 1; float64(r) < nrows; r++ {
			if err := guard.step(); err != nil {
				return bounds, err
			}
			offset := zero
			if err := row(r, func(i jsval.Value) {
				if yo := yOffset.get(i); jsLess(offset, yo) {
					offset = yo
				}
			}); err != nil {
				return bounds, err
			}
			ext := yExt(numv(float64(r - 1)))
			if err := row(r, func(i jsval.Value) { yOffset.set(i, add(offset, ext)) }); err != nil {
				return bounds, err
			}
		}
	case all:
		// for (offset=0, i=ncols; i<n; ++i): i starts as the value itself
		offset := zero
		for i := ncols; jsLess(i, nv); i = numv(num(i) + 1) {
			if err := guard.step(); err != nil {
				return bounds, err
			}
			if yo := yOffset.get(i); jsLess(offset, yo) {
				offset = yo
			}
		}
		for i := ncols; jsLess(i, nv); i = numv(num(i) + 1) {
			if err := guard.step(); err != nil {
				return bounds, err
			}
			yOffset.set(i, add(offset, numv(yMax)))
		}
	default:
		alignRow = ""
		for r := 1; float64(r) < nrows; r++ {
			if err := guard.step(); err != nil {
				return bounds, err
			}
			ext := yExt(numv(float64(r - 1)))
			if err := row(r, func(i jsval.Value) { yOffset.set(i, add(yOffset.get(i), ext)) }); err != nil {
				return bounds, err
			}
		}
	}

	// perform horizontal grid layout: x = xOffset[i] + (i % ncols ? x : 0)
	x := zero
	for i := 0; i < n; i++ {
		prev := zero
		if m := mod(i); m != 0 && !math.IsNaN(m) {
			prev = x
		}
		x = add(xOffset.get(numv(float64(i))), prev)
		dx[i] += num(x) - num(prop(groups[i], "x"))
	}

	// perform vertical grid layout: dy[i] += y - groups[i].y, which throws
	// for an index that is no cell
	for c := 0; float64(c) < N && c < n; c++ {
		y := zero
		var failed bool
		if err := cells(c, func(i jsval.Value) {
			if failed {
				return
			}
			y = add(y, yOffset.get(i))
			g := groupAt(groups, i)
			if g == nil {
				failed = true
				return
			}
			k := int(num(i))
			dy[k] += num(y) - num(prop(g, "y"))
		}); err != nil {
			return bounds, err
		}
		if failed {
			return bounds, fmt.Errorf("Cannot read properties of undefined (reading 'y')")
		}
	}

	// perform horizontal centering
	if alignCol != "" && opt.centerCol && nrows > 1 {
		for i := 0; i < n; i++ {
			b := xMax
			if alignCol != all {
				b = num(xExtent.get(numv(mod(i))))
			}
			x := b - boxes[i].X2 - num(prop(groups[i], "x")) - dx[i]
			if x > 0 {
				dx[i] += x / 2
			}
		}
	}

	// perform vertical centering (ncols !== 1: a text never is)
	if alignRow != "" && opt.centerRow {
		for i := 0; i < n; i++ {
			b := yMax
			if alignRow != all {
				b = num(yExt(numv(float64(toInt32(float64(i) / N)))))
			}
			y := b - boxes[i].Y2 - num(prop(groups[i], "y")) - dy[i]
			if y > 0 {
				dy[i] += y / 2
			}
		}
	}

	return placeGrid(groups, opt, boxes, dx, dy, add), nil
}

// toInt32 is JavaScript's ToInt32 (`~~v`).
func toInt32(v float64) int32 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return int32(uint32(int64(math.Trunc(math.Mod(v, 1<<32)))))
}
