package jsval

import "strings"

// MaxLevelText is the longest text NextAtLevel indexes: its answers are
// 32-bit positions.
const MaxLevelText = 1<<31 - 1

// NextAtLevel is vega-event-selector's find(s, i, end, push, pop) for every i
// at once: next[i] is the position of the first byte end at or after i with
// as many bytes of push as of pop between i and it (the bytes of push count
// up, those of pop down, as find counts them), and len(s) if there is none;
// next has len(s)+1 entries, the last being len(s). find scans the text for
// each question, which a parser that asks it of the rest of the text at every
// level of nesting turns into a cost of the text's length times its depth;
// this answers all of them in one pass, whatever the nesting. The characters
// are ASCII, so bytes stand for UTF-16 units. s must be shorter than
// MaxLevelText.
//
// Two positions are at the same level exactly when the running total of the
// level changes from each to the end of the text is the same, so the pass
// goes backward and keeps, for each such total, the nearest end seen so far.
func NextAtLevel(s string, end byte, push, pop string) []int32 {
	n := len(s)
	delta := func(c byte) int {
		switch {
		case strings.IndexByte(pop, c) >= 0:
			return -1
		case strings.IndexByte(push, c) >= 0:
			return 1
		}
		return 0
	}
	// The totals can run from -n to n, but the table covers only the range
	// they reach.
	lo, hi, sum := 0, 0, 0
	for i := n - 1; i >= 0; i-- {
		sum += delta(s[i])
		lo, hi = min(lo, sum), max(hi, sum)
	}
	last := make([]int32, hi-lo+1)
	for i := range last {
		last[i] = int32(n)
	}
	next := make([]int32, n+1)
	next[n] = int32(n)
	sum = 0
	for i := n - 1; i >= 0; i-- {
		sum += delta(s[i])
		if s[i] == end {
			last[sum-lo] = int32(i)
		}
		next[i] = last[sum-lo]
	}
	return next
}
