// Package jssort sorts exactly as V8's Array.prototype.sort does.
//
// For a consistent comparator every stable sort produces the same order, but
// Vega's comparators are not always consistent: vega-util's compare orders
// mixed strings and numbers with JavaScript's `<`, which is not transitive
// across types. The order upstream produces then depends on which pairs the
// algorithm happens to compare, so reproducing it takes V8's algorithm, not
// just any stable sort. This is a port of V8's TimSort
// (third_party/v8/builtins/array-sort.tq, V8 11.3 as shipped in Node 20),
// itself derived from CPython's listsort.
//
// The port keeps V8's guards for inconsistent comparators, so no comparator
// can make it index out of range or loop forever.
package jssort

// minGallopWins is kMinGallopWins: the number of consecutive wins that
// switches a merge into galloping mode.
const minGallopWins = 7

// Sort sorts s in place with cmp, which reports a negative number when a
// sorts before b (V8 only ever tests `order < 0` and `order >= 0`).
func Sort[T any](s []T, cmp func(a, b T) int) {
	if len(s) < 2 {
		return
	}
	st := &state[T]{a: s, cmp: cmp, minGallop: minGallopWins}
	st.sort()
}

type run struct{ base, length int }

type state[T any] struct {
	a         []T
	cmp       func(a, b T) int
	minGallop int
	runs      []run
	tmp       []T
}

func (st *state[T]) sort() {
	remaining := len(st.a)
	low := 0
	minRun := minRunLength(remaining)
	for remaining != 0 {
		n := st.countAndMakeRun(low, low+remaining)
		if n < minRun {
			forced := min(minRun, remaining)
			st.binaryInsertionSort(low, low+n, low+forced)
			n = forced
		}
		st.runs = append(st.runs, run{low, n})
		st.mergeCollapse()
		low += n
		remaining -= n
	}
	st.mergeForceCollapse()
}

func minRunLength(n int) int {
	r := 0
	for n >= 64 {
		r |= n & 1
		n >>= 1
	}
	return n + r
}

// binaryInsertionSort sorts a[low:high], of which a[low:start] is sorted.
func (st *state[T]) binaryInsertionSort(low, start, high int) {
	a := st.a
	if low == start {
		start++
	}
	for ; start < high; start++ {
		left, right := low, start
		pivot := a[right]
		for left < right {
			mid := left + ((right - left) >> 1)
			if st.cmp(pivot, a[mid]) < 0 {
				right = mid
			} else {
				left = mid + 1
			}
		}
		copy(a[left+1:start+1], a[left:start])
		a[left] = pivot
	}
}

// countAndMakeRun returns the length of the run starting at low, reversing
// it in place when it is strictly descending.
func (st *state[T]) countAndMakeRun(lowArg, high int) int {
	a := st.a
	low := lowArg + 1
	if low == high {
		return 1
	}
	n := 2
	prev := a[low]
	descending := st.cmp(a[low], a[low-1]) < 0
	for i := low + 1; i < high; i++ {
		cur := a[i]
		order := st.cmp(cur, prev)
		if descending {
			if order >= 0 {
				break
			}
		} else if order < 0 {
			break
		}
		prev = cur
		n++
	}
	if descending {
		for l, h := lowArg, lowArg+n-1; l < h; l, h = l+1, h-1 {
			a[l], a[h] = a[h], a[l]
		}
	}
	return n
}

func (st *state[T]) runInvariantEstablished(n int) bool {
	if n < 2 {
		return true
	}
	r := st.runs
	return r[n-2].length > r[n-1].length+r[n].length
}

func (st *state[T]) mergeCollapse() {
	for len(st.runs) > 1 {
		n := len(st.runs) - 2
		if !st.runInvariantEstablished(n+1) || !st.runInvariantEstablished(n) {
			if st.runs[n-1].length < st.runs[n+1].length {
				n--
			}
			st.mergeAt(n)
		} else if st.runs[n].length <= st.runs[n+1].length {
			st.mergeAt(n)
		} else {
			break
		}
	}
}

func (st *state[T]) mergeForceCollapse() {
	for len(st.runs) > 1 {
		n := len(st.runs) - 2
		if n > 0 && st.runs[n-1].length < st.runs[n+1].length {
			n--
		}
		st.mergeAt(n)
	}
}

func (st *state[T]) mergeAt(i int) {
	a := st.a
	baseA, lengthA := st.runs[i].base, st.runs[i].length
	baseB, lengthB := st.runs[i+1].base, st.runs[i+1].length
	st.runs[i].length = lengthA + lengthB
	if i == len(st.runs)-3 {
		st.runs[i+1] = st.runs[i+2]
	}
	st.runs = st.runs[:len(st.runs)-1]

	k := st.gallopRight(a, a[baseB], baseA, lengthA, 0)
	baseA += k
	lengthA -= k
	if lengthA == 0 {
		return
	}
	lengthB = st.gallopLeft(a, a[baseA+lengthA-1], baseB, lengthB, lengthB-1)
	if lengthB == 0 {
		return
	}
	if lengthA <= lengthB {
		st.mergeLow(baseA, lengthA, baseB, lengthB)
	} else {
		st.mergeHigh(baseA, lengthA, baseB, lengthB)
	}
}

// gallopLeft locates the position at which to insert key into the sorted
// arr[base:base+length], left of any equal elements, starting from hint.
func (st *state[T]) gallopLeft(arr []T, key T, base, length, hint int) int {
	lastOfs, offset := 0, 1
	if st.cmp(arr[base+hint], key) < 0 {
		maxOfs := length - hint
		for offset < maxOfs {
			if st.cmp(arr[base+hint+offset], key) >= 0 {
				break
			}
			lastOfs = offset
			offset = (offset << 1) + 1
			if offset <= 0 {
				offset = maxOfs
			}
		}
		if offset > maxOfs {
			offset = maxOfs
		}
		lastOfs += hint
		offset += hint
	} else {
		maxOfs := hint + 1
		for offset < maxOfs {
			if st.cmp(arr[base+hint-offset], key) < 0 {
				break
			}
			lastOfs = offset
			offset = (offset << 1) + 1
			if offset <= 0 {
				offset = maxOfs
			}
		}
		if offset > maxOfs {
			offset = maxOfs
		}
		lastOfs, offset = hint-offset, hint-lastOfs
	}
	lastOfs++
	for lastOfs < offset {
		m := lastOfs + ((offset - lastOfs) >> 1)
		if st.cmp(arr[base+m], key) < 0 {
			lastOfs = m + 1
		} else {
			offset = m
		}
	}
	return offset
}

// gallopRight is gallopLeft for the position right of any equal elements.
func (st *state[T]) gallopRight(arr []T, key T, base, length, hint int) int {
	lastOfs, offset := 0, 1
	if st.cmp(key, arr[base+hint]) < 0 {
		maxOfs := hint + 1
		for offset < maxOfs {
			if st.cmp(key, arr[base+hint-offset]) >= 0 {
				break
			}
			lastOfs = offset
			offset = (offset << 1) + 1
			if offset <= 0 {
				offset = maxOfs
			}
		}
		if offset > maxOfs {
			offset = maxOfs
		}
		lastOfs, offset = hint-offset, hint-lastOfs
	} else {
		maxOfs := length - hint
		for offset < maxOfs {
			if st.cmp(key, arr[base+hint+offset]) < 0 {
				break
			}
			lastOfs = offset
			offset = (offset << 1) + 1
			if offset <= 0 {
				offset = maxOfs
			}
		}
		if offset > maxOfs {
			offset = maxOfs
		}
		lastOfs += hint
		offset += hint
	}
	lastOfs++
	for lastOfs < offset {
		m := lastOfs + ((offset - lastOfs) >> 1)
		if st.cmp(key, arr[base+m]) < 0 {
			offset = m
		} else {
			lastOfs = m + 1
		}
	}
	return offset
}

func (st *state[T]) tempArray(n int) []T {
	if cap(st.tmp) < n {
		st.tmp = make([]T, n)
	}
	return st.tmp[:n]
}

// mergeLow merges the adjacent runs a[baseA:baseA+lengthA] and
// a[baseB:baseB+lengthB] (lengthA <= lengthB) through a copy of the first.
func (st *state[T]) mergeLow(baseA, lengthA, baseB, lengthB int) {
	a := st.a
	tmp := st.tempArray(lengthA)
	copy(tmp, a[baseA:baseA+lengthA])
	dest, cursorTemp, cursorB := baseA, 0, baseB

	a[dest] = a[cursorB]
	dest++
	cursorB++

	copyB := func() {
		copy(a[dest:dest+lengthB], a[cursorB:cursorB+lengthB])
		a[dest+lengthB] = tmp[cursorTemp]
	}
	succeed := func() {
		if lengthA > 0 {
			copy(a[dest:dest+lengthA], tmp[cursorTemp:cursorTemp+lengthA])
		}
	}

	lengthB--
	if lengthB == 0 {
		succeed()
		return
	}
	if lengthA == 1 {
		copyB()
		return
	}
	minGallop := st.minGallop
	for {
		winsA, winsB := 0, 0
		for {
			if st.cmp(a[cursorB], tmp[cursorTemp]) < 0 {
				a[dest] = a[cursorB]
				dest++
				cursorB++
				winsB++
				lengthB--
				winsA = 0
				if lengthB == 0 {
					succeed()
					return
				}
				if winsB >= minGallop {
					break
				}
			} else {
				a[dest] = tmp[cursorTemp]
				dest++
				cursorTemp++
				winsA++
				lengthA--
				winsB = 0
				if lengthA == 1 {
					copyB()
					return
				}
				if winsA >= minGallop {
					break
				}
			}
		}
		minGallop++
		first := true
		for winsA >= minGallopWins || winsB >= minGallopWins || first {
			first = false
			minGallop = max(1, minGallop-1)
			st.minGallop = minGallop

			winsA = st.gallopRight(tmp, a[cursorB], cursorTemp, lengthA, 0)
			if winsA > 0 {
				copy(a[dest:dest+winsA], tmp[cursorTemp:cursorTemp+winsA])
				dest += winsA
				cursorTemp += winsA
				lengthA -= winsA
				if lengthA == 1 {
					copyB()
					return
				}
				// lengthA can only reach zero with an inconsistent comparator.
				if lengthA == 0 {
					succeed()
					return
				}
			}
			a[dest] = a[cursorB]
			dest++
			cursorB++
			lengthB--
			if lengthB == 0 {
				succeed()
				return
			}

			winsB = st.gallopLeft(a, tmp[cursorTemp], cursorB, lengthB, 0)
			if winsB > 0 {
				copy(a[dest:dest+winsB], a[cursorB:cursorB+winsB])
				dest += winsB
				cursorB += winsB
				lengthB -= winsB
				if lengthB == 0 {
					succeed()
					return
				}
			}
			a[dest] = tmp[cursorTemp]
			dest++
			cursorTemp++
			lengthA--
			if lengthA == 1 {
				copyB()
				return
			}
		}
		minGallop++ // penalize leaving galloping mode
		st.minGallop = minGallop
	}
}

// mergeHigh merges the adjacent runs (lengthA > lengthB) from the right,
// through a copy of the second.
func (st *state[T]) mergeHigh(baseA, lengthA, baseB, lengthB int) {
	a := st.a
	tmp := st.tempArray(lengthB)
	copy(tmp, a[baseB:baseB+lengthB])
	dest, cursorTemp, cursorA := baseB+lengthB-1, lengthB-1, baseA+lengthA-1

	a[dest] = a[cursorA]
	dest--
	cursorA--

	copyA := func() {
		dest -= lengthA
		cursorA -= lengthA
		copy(a[dest+1:dest+1+lengthA], a[cursorA+1:cursorA+1+lengthA])
		a[dest] = tmp[cursorTemp]
	}
	succeed := func() {
		if lengthB > 0 {
			copy(a[dest-(lengthB-1):dest+1], tmp[:lengthB])
		}
	}

	lengthA--
	if lengthA == 0 {
		succeed()
		return
	}
	if lengthB == 1 {
		copyA()
		return
	}
	minGallop := st.minGallop
	for {
		winsA, winsB := 0, 0
		for {
			if st.cmp(tmp[cursorTemp], a[cursorA]) < 0 {
				a[dest] = a[cursorA]
				dest--
				cursorA--
				winsA++
				lengthA--
				winsB = 0
				if lengthA == 0 {
					succeed()
					return
				}
				if winsA >= minGallop {
					break
				}
			} else {
				a[dest] = tmp[cursorTemp]
				dest--
				cursorTemp--
				winsB++
				lengthB--
				winsA = 0
				if lengthB == 1 {
					copyA()
					return
				}
				if winsB >= minGallop {
					break
				}
			}
		}
		minGallop++
		first := true
		for winsA >= minGallopWins || winsB >= minGallopWins || first {
			first = false
			minGallop = max(1, minGallop-1)
			st.minGallop = minGallop

			k := st.gallopRight(a, tmp[cursorTemp], baseA, lengthA, lengthA-1)
			winsA = lengthA - k
			if winsA > 0 {
				dest -= winsA
				cursorA -= winsA
				copy(a[dest+1:dest+1+winsA], a[cursorA+1:cursorA+1+winsA])
				lengthA -= winsA
				if lengthA == 0 {
					succeed()
					return
				}
			}
			a[dest] = tmp[cursorTemp]
			dest--
			cursorTemp--
			lengthB--
			if lengthB == 1 {
				copyA()
				return
			}

			k = st.gallopLeft(tmp, a[cursorA], 0, lengthB, lengthB-1)
			winsB = lengthB - k
			if winsB > 0 {
				dest -= winsB
				cursorTemp -= winsB
				copy(a[dest+1:dest+1+winsB], tmp[cursorTemp+1:cursorTemp+1+winsB])
				lengthB -= winsB
				if lengthB == 1 {
					copyA()
					return
				}
				// lengthB can only reach zero with an inconsistent comparator.
				if lengthB == 0 {
					succeed()
					return
				}
			}
			a[dest] = a[cursorA]
			dest--
			cursorA--
			lengthA--
			if lengthA == 0 {
				succeed()
				return
			}
		}
		minGallop++
		st.minGallop = minGallop
	}
}

// Sign converts a JavaScript comparator's numeric result for Sort: V8 reads
// ToNumber(result) and treats NaN as zero.
func Sign(v float64) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}
	return 0 // zero or NaN
}
