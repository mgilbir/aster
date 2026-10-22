package jsval

import (
	"strings"
	"unicode/utf8"
)

// The case mapping of String.prototype.toLowerCase and toUpperCase. The
// mappings are tables recorded from the pinned node (casetables.go, written
// by testdata/gen_casetables.mjs), not Go's: the unicode package and
// golang.org/x/text change with the Go toolchain (Go 1.26 has Unicode 15,
// Go 1.27 Unicode 17, which is node's; a later Go may move on while node
// does not), and the engine answers as the pinned node does whichever
// toolchain builds it. The regular expressions do take the toolchain's
// Unicode (goecma262), which is why go.mod requires Go 1.27
// (TestToolchainUnicodeIsNodes). TestNoGoCaseOrSpaceInTheEngine keeps
// golang.org/x/text/cases out of the rest of the engine.
//
// V8 maps a string by ICU's root-locale full case mapping: every code point
// by its own simple or one-to-many mapping, except that toLowerCase gives
// the sigma that ends a word, U+03A3 after a cased letter and before no
// cased letter (skipping case-ignorable code points on both sides), as ς.
// The generator checks that no other code point looks at its neighbours.

// caseRun maps the code points lo, lo+step, ... up to hi (step is 1 or 2) to
// themselves plus delta.
type caseRun struct {
	lo, hi      rune
	step, delta int32
}

// caseSpecial is a code point whose mapping is not one code point.
type caseSpecial struct {
	r rune
	s string
}

// lookupRun is the one-to-one mapping of r in runs, which are sorted. index
// narrows the search to the runs that start in r's block of 256 code points
// and the one before them (runIndex in the generator): case mappings are
// dense in a few blocks and absent from most.
func lookupRun(runs []caseRun, index []uint16, r rune) (rune, bool) {
	lo, hi := 0, len(runs)
	if b := int(r >> 8); b < len(index) {
		if b > 0 {
			lo = int(index[b-1])
		}
		hi = int(index[b])
	} else {
		lo = hi // past the last run's block: only the last run can hold r
	}
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if runs[m].lo <= r {
			lo = m + 1
		} else {
			hi = m
		}
	}
	if lo == 0 {
		return 0, false
	}
	run := &runs[lo-1]
	if r > run.hi || run.step == 2 && (r-run.lo)&1 != 0 {
		return 0, false
	}
	return r + rune(run.delta), true
}

// lookupSpecial is the one-to-many mapping of r in specials, which are
// sorted; index is as for lookupRun.
func lookupSpecial(specials []caseSpecial, index []uint16, r rune) (string, bool) {
	lo, hi := 0, 0
	if b := int(r >> 8); b < len(index) {
		if b > 0 {
			lo = int(index[b-1])
		}
		hi = int(index[b])
	}
	for ; lo < hi; lo++ {
		if specials[lo].r == r {
			return specials[lo].s, true
		}
	}
	return "", false
}

// inRanges reports whether r is in the sorted, inclusive [lo, hi] pairs.
func inRanges(ranges [][2]rune, r rune) bool {
	lo, hi := 0, len(ranges)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if ranges[m][0] <= r {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo > 0 && ranges[lo-1][1] >= r
}

// finalSigma reports whether the sigma at s[i:i+w] ends a word: a cased letter
// before it and none after it, case-ignorable code points skipped on both
// sides (ECMA-262 and Unicode's Final_Sigma). A lone surrogate's WTF-8 bytes,
// like any byte that is not UTF-8, decode as U+FFFD, which is neither cased
// nor case-ignorable, so it ends the search as it does in JavaScript.
func finalSigma(s string, i, w int) bool {
	before := false
	for j := i; j > 0; {
		r, n := utf8.DecodeLastRuneInString(s[:j])
		if inRanges(caseIgnorableRanges, r) {
			j -= n
			continue
		}
		before = inRanges(casedRanges, r)
		break
	}
	if !before {
		return false
	}
	for j := i + w; j < len(s); {
		r, n := utf8.DecodeRuneInString(s[j:])
		if inRanges(caseIgnorableRanges, r) {
			j += n
			continue
		}
		return !inRanges(casedRanges, r)
	}
	return true
}

// mapCase maps s, a string with a non-ASCII byte, by the tables: lower-case
// or upper-case. It returns s itself when nothing in it changes, and keeps
// any byte that is not UTF-8 (a lone surrogate's WTF-8 bytes) as it is.
func mapCase(s string, lower bool) string {
	runs, index, specials, sindex := upperRuns, upperRunIndex, upperSpecial, upperSpecialIndex
	if lower {
		runs, index, specials, sindex = lowerRuns, lowerRunIndex, lowerSpecial, lowerSpecialIndex
	}
	var b strings.Builder
	start := 0 // s[start:] is not yet in b
	for i := 0; i < len(s); {
		r, w := rune(s[i]), 1
		if r >= utf8.RuneSelf {
			r, w = utf8.DecodeRuneInString(s[i:])
		}
		var one rune
		var many string
		ok := false
		switch {
		case lower && r == 0x3A3 && finalSigma(s, i, w):
			many, ok = "ς", true
		case r < utf8.RuneSelf:
			if lower && 'A' <= r && r <= 'Z' {
				one, ok = r+'a'-'A', true
			} else if !lower && 'a' <= r && r <= 'z' {
				one, ok = r-('a'-'A'), true
			}
		default:
			if m, found := lookupRun(runs, index, r); found {
				one, ok = m, true
			} else if m, found := lookupSpecial(specials, sindex, r); found {
				many, ok = m, true
			}
		}
		if ok {
			if b.Cap() == 0 {
				b.Grow(len(s) + len(s)/8)
			}
			b.WriteString(s[start:i])
			if many != "" {
				b.WriteString(many)
			} else {
				b.WriteRune(one)
			}
			start = i + w
		}
		i += w
	}
	if b.Cap() == 0 {
		return s
	}
	b.WriteString(s[start:])
	return b.String()
}
