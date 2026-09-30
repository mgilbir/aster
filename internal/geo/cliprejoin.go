package geo

// intersection is a node of the two circular lists (subject polygon and clip
// edge) the Greiner–Hormann style rejoin walks.
type intersection struct {
	x    *cpoint
	z    []cpoint // the segment this end belongs to (subject list only)
	o    *intersection
	e    bool // entry?
	v    bool // visited
	n, p *intersection
}

func link(a []*intersection) {
	n := len(a)
	if n == 0 {
		return
	}
	prev := a[0]
	for i := 1; i < n; i++ {
		b := a[i]
		prev.n = b
		b.p = prev
		prev = b
	}
	prev.n = a[0]
	a[0].p = prev
}

// interpolateFunc emits points along the clip edge from one intersection to
// another (from == nil means the whole clip boundary).
type interpolateFunc func(from, to *cpoint, direction float64, s Stream)

// clipRejoin is a generalised polygon clipping step: given a polygon already
// cut into its visible line segments, it rejoins the segments by interpolating
// along the clip edge.
func clipRejoin(segments [][]cpoint, compare func(a, b *intersection) float64, startInside bool, interpolate interpolateFunc, s Stream) {
	var subject, clip []*intersection

	for _, segment := range segments {
		n := len(segment) - 1
		if n <= 0 {
			continue
		}
		p0, p1 := &segment[0], &segment[n]
		if pointEqual(p0.x, p0.y, p1.x, p1.y) {
			if p0.m == 0 && p1.m == 0 {
				s.LineStart()
				for i := 0; i < n; i++ {
					s.Point(segment[i].x, segment[i].y)
				}
				s.LineEnd()
				continue
			}
			// handle degenerate cases by moving the point
			p1.x += 2 * epsilon
		}

		x := &intersection{x: p0, z: segment, e: true}
		x.o = &intersection{x: p0, o: x}
		subject = append(subject, x)
		clip = append(clip, x.o)
		x = &intersection{x: p1, z: segment, e: false}
		x.o = &intersection{x: p1, o: x, e: true}
		subject = append(subject, x)
		clip = append(clip, x.o)
	}

	if len(subject) == 0 {
		return
	}

	sortIntersections(clip, compare)
	link(subject)
	link(clip)

	for _, c := range clip {
		startInside = !startInside
		c.e = startInside
	}

	start := subject[0]
	for {
		// Find first unvisited intersection.
		current := start
		isSubject := true
		for current.v {
			current = current.n
			if current == start {
				return
			}
		}
		points := current.z
		s.LineStart()
		for {
			current.v, current.o.v = true, true
			if current.e {
				if isSubject {
					for i := range points {
						s.Point(points[i].x, points[i].y)
					}
				} else {
					interpolate(current.x, current.n.x, 1, s)
				}
				current = current.n
			} else {
				if isSubject {
					points = current.p.z
					for i := len(points) - 1; i >= 0; i-- {
						s.Point(points[i].x, points[i].y)
					}
				} else {
					interpolate(current.x, current.p.x, -1, s)
				}
				current = current.p
			}
			current = current.o
			points = current.z
			isSubject = !isSubject
			if current.v {
				break
			}
		}
		s.LineEnd()
	}
}

// sortIntersections is a stable sort by the clip comparison: JavaScript's
// Array.prototype.sort is stable, and ties (coincident intersections) must keep
// their order for the rejoin to match upstream.
func sortIntersections(a []*intersection, compare func(a, b *intersection) float64) {
	// insertion sort for short lists, merge sort otherwise
	if len(a) < 12 {
		for i := 1; i < len(a); i++ {
			for j := i; j > 0 && compare(a[j-1], a[j]) > 0; j-- {
				a[j-1], a[j] = a[j], a[j-1]
			}
		}
		return
	}
	tmp := make([]*intersection, len(a))
	mergeSort(a, tmp, compare)
}

func mergeSort(a, tmp []*intersection, compare func(a, b *intersection) float64) {
	n := len(a)
	if n < 12 {
		sortIntersections(a, compare)
		return
	}
	mid := n / 2
	mergeSort(a[:mid], tmp[:mid], compare)
	mergeSort(a[mid:], tmp[mid:], compare)
	copy(tmp, a)
	i, j, k := 0, mid, 0
	for i < mid && j < n {
		if compare(tmp[j], tmp[i]) < 0 {
			a[k] = tmp[j]
			j++
		} else {
			a[k] = tmp[i]
			i++
		}
		k++
	}
	for i < mid {
		a[k] = tmp[i]
		i++
		k++
	}
	for j < n {
		a[k] = tmp[j]
		j++
		k++
	}
}

func pointEqual(ax, ay, bx, by float64) bool {
	return abs(ax-bx) < epsilon && abs(ay-by) < epsilon
}
