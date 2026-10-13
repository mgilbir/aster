package format

import "math"

// week.every(k) in a zone whose offset changes is the one filter d3 can only
// walk: its floor steps back one week at a time, and its offset forward one
// at a time, until interval.count(0, week) is a multiple of k, so a boundary
// can be k weeks away (d3 makes no assumption about the counts; across a
// change of offset with seconds, such as the end of local mean time, two
// weeks can share one). Past a few dozen weeks, the walk is replaced by a
// search with the same result (ADR 0011): the count of the week j weeks
// from a week start is monotone in j, so the latest (or next) week whose
// count is a given multiple of k is found by bisection, and a multiple no
// week has is passed over as the walk passes over it.

// weekWalk is the largest k for which week.every(k) is walked: a walk of at
// most k weeks costs less than a search.
const weekWalk = 64

// maxWeekSearches bounds the multiples one search passes over (each a
// multiple of k no week has); real zones skip none.
const maxWeekSearches = 64

// weekSearch reports whether the filter is a week.every(k) of a zone with
// changing offsets and a k past weekWalk.
func (iv Interval) weekSearch() bool {
	return iv.kind == kWeek && iv.filter > weekWalk && !iv.z.fixed()
}

// weekAt is the start of the week j calendar weeks from the week start w, as
// d3's walk reaches it (floor(previous - 1) going back, floor of a date 7
// days on going forward): the start of the week of that local day.
func (iv Interval) weekAt(w, j float64) float64 {
	if j == 0 {
		return w
	}
	parent := iv
	parent.filter = 0
	f := iv.z.Fields(w)
	return parent.baseFloor(iv.z.make(float64(f.Year), float64(f.Month), float64(f.Day)+float64(7*j), 0, 0, 0, 0))
}

// weekCount is the count the filter tests, of the week j weeks from w; NaN
// once the week is not a valid date. Each call adds one to *evals when
// evals is not nil (tests count the search's work).
func (iv Interval) weekCount(w, j float64, evals *int) float64 {
	if evals != nil {
		*evals++
	}
	d := iv.weekAt(w, j)
	if !Valid(d) {
		return math.NaN()
	}
	return iv.epochCount(d)
}

// weekFloor is Floor for weekSearch filters: from the week start w, the
// latest week at or before it whose count is a multiple of k.
func (iv Interval) weekFloor(w float64) float64 { return iv.weekFloorN(w, nil) }

func (iv Interval) weekFloorN(w float64, evals *int) float64 {
	k := iv.filter
	j, v := 0.0, iv.weekCount(w, 0, evals)
	for range maxWeekSearches {
		if math.IsNaN(v) {
			return math.NaN() // the walk runs off the dates: upstream never ends
		}
		r := v - float64(floorDiv(v, k)*k)
		if r == 0 {
			return iv.weekAt(w, j)
		}
		// The latest week before j whose count is at most v - r. A week
		// past the dates reads as below every count.
		m := v - r
		below := func(i float64) bool { c := iv.weekCount(w, i, evals); return math.IsNaN(c) || c <= m }
		step := r
		lo := j - step
		for !below(lo) {
			step *= 2
			lo = j - step
		}
		hi := j // !below(hi)
		for hi-lo > 1 {
			mid := math.Floor((lo + hi) / 2)
			if below(mid) {
				lo = mid
			} else {
				hi = mid
			}
		}
		j, v = lo, iv.weekCount(w, lo, evals)
	}
	return math.NaN()
}

// weekNext is Floor(Offset(w, 1)) for weekSearch filters and a week start
// w: the first week after it whose count is a multiple of k.
func (iv Interval) weekNext(w float64) float64 { return iv.weekNextN(w, nil) }

func (iv Interval) weekNextN(w float64, evals *int) float64 {
	k := iv.filter
	j, v := 1.0, iv.weekCount(w, 1, evals)
	for range maxWeekSearches {
		if math.IsNaN(v) {
			return math.NaN()
		}
		r := v - float64(floorDiv(v, k)*k)
		if r == 0 {
			return iv.weekAt(w, j)
		}
		// The first week after j whose count is at least the next multiple.
		m := v + (k - r)
		above := func(i float64) bool { c := iv.weekCount(w, i, evals); return math.IsNaN(c) || c >= m }
		step := k - r
		hi := j + step
		for !above(hi) {
			step *= 2
			hi = j + step
		}
		lo := j // !above(lo)
		for hi-lo > 1 {
			mid := math.Floor((lo + hi) / 2)
			if above(mid) {
				hi = mid
			} else {
				lo = mid
			}
		}
		j, v = hi, iv.weekCount(w, hi, evals)
	}
	return math.NaN()
}
