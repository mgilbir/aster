package hierarchy

import "math"

// phi is the golden ratio, the default squarify aspect ratio.
var phi = (1 + math.Sqrt(5)) / 2

// tileFunc positions parent's children inside the rectangle.
type tileFunc func(parent *Node, x0, y0, x1, y1 float64)

// jsRound is Math.round: halves round toward +Infinity.
func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	f := math.Floor(x)
	if x-f >= 0.5 {
		f++
	}
	return f
}

func roundNode(n *Node) {
	n.X0, n.Y0, n.X1, n.Y1 = jsRound(n.X0), jsRound(n.Y0), jsRound(n.X1), jsRound(n.Y1)
}

// valueScale is d3's `parent.value && (x1 - x0) / parent.value`: a falsy value
// (0 or NaN) is returned as is.
func valueScale(value, extent float64) float64 {
	if !truthy(value) {
		return value
	}
	return extent / value
}

func dice(nodes []*Node, value, x0, y0, x1, y1 float64) {
	k := valueScale(value, x1-x0)
	for _, n := range nodes {
		n.Y0, n.Y1 = y0, y1
		n.X0 = x0
		x0 += float64(n.Value * k)
		n.X1 = x0
	}
}

func slice(nodes []*Node, value, x0, y0, x1, y1 float64) {
	k := valueScale(value, y1-y0)
	for _, n := range nodes {
		n.X0, n.X1 = x0, x1
		n.Y0 = y0
		y0 += float64(n.Value * k)
		n.Y1 = y0
	}
}

func tileDice(p *Node, x0, y0, x1, y1 float64)  { dice(p.Children, p.Value, x0, y0, x1, y1) }
func tileSlice(p *Node, x0, y0, x1, y1 float64) { slice(p.Children, p.Value, x0, y0, x1, y1) }

func tileSliceDice(p *Node, x0, y0, x1, y1 float64) {
	if p.Depth&1 != 0 {
		tileSlice(p, x0, y0, x1, y1)
	} else {
		tileDice(p, x0, y0, x1, y1)
	}
}

// tileBinary balances the children into two halves recursively.
func tileBinary(parent *Node, x0, y0, x1, y1 float64) {
	nodes := parent.Children
	n := len(nodes)
	sums := make([]float64, n+1)
	sum := 0.0
	for i, nd := range nodes {
		sum += nd.Value
		sums[i+1] = sum
	}
	var part func(i, j int, value, x0, y0, x1, y1 float64)
	part = func(i, j int, value, x0, y0, x1, y1 float64) {
		if i >= j-1 {
			nd := nodes[i]
			nd.X0, nd.Y0, nd.X1, nd.Y1 = x0, y0, x1, y1
			return
		}
		valueOffset := sums[i]
		valueTarget := value/2 + valueOffset
		k := i + 1
		hi := j - 1
		for k < hi {
			mid := (k + hi) >> 1
			if sums[mid] < valueTarget {
				k = mid + 1
			} else {
				hi = mid
			}
		}
		if valueTarget-sums[k-1] < sums[k]-valueTarget && i+1 < k {
			k--
		}
		valueLeft := sums[k] - valueOffset
		valueRight := value - valueLeft
		if x1-x0 > y1-y0 {
			xk := x1
			if truthy(value) {
				xk = (float64(x0*valueRight) + float64(x1*valueLeft)) / value
			}
			part(i, k, valueLeft, x0, y0, xk, y1)
			part(k, j, valueRight, xk, y0, x1, y1)
		} else {
			yk := y1
			if truthy(value) {
				yk = (float64(y0*valueRight) + float64(y1*valueLeft)) / value
			}
			part(i, k, valueLeft, x0, y0, x1, yk)
			part(k, j, valueRight, x0, yk, x1, y1)
		}
	}
	part(0, n, parent.Value, x0, y0, x1, y1)
}

// sqRow is a squarified row of siblings.
type sqRow struct {
	value    float64
	dice     bool
	children []*Node
}

func squarifyRatio(ratio float64, parent *Node, x0, y0, x1, y1 float64) []*sqRow {
	var rows []*sqRow
	nodes := parent.Children
	n := len(nodes)
	value := parent.Value
	i0, i1 := 0, 0
	for i0 < n {
		dx, dy := x1-x0, y1-y0
		var sumValue float64
		for {
			sumValue = nodes[i1].Value
			i1++
			if truthy(sumValue) || i1 >= n {
				break
			}
		}
		minValue, maxValue := sumValue, sumValue
		alpha := math.Max(dy/dx, dx/dy) / (value * ratio)
		beta := sumValue * sumValue * alpha
		minRatio := math.Max(maxValue/beta, beta/minValue)
		for ; i1 < n; i1++ {
			nodeValue := nodes[i1].Value
			sumValue += nodeValue
			if nodeValue < minValue {
				minValue = nodeValue
			}
			if nodeValue > maxValue {
				maxValue = nodeValue
			}
			beta = sumValue * sumValue * alpha
			newRatio := math.Max(maxValue/beta, beta/minValue)
			if newRatio > minRatio {
				sumValue -= nodeValue
				break
			}
			minRatio = newRatio
		}
		row := &sqRow{value: sumValue, dice: dx < dy, children: nodes[i0:i1]}
		rows = append(rows, row)
		x0, y0 = placeRow(row, value, x0, y0, x1, y1)
		value -= sumValue
		i0 = i1
	}
	return rows
}

// placeRow tiles one row and returns the origin of the remaining rectangle.
// Note the argument order in d3: the row is laid out with the *old* x0/y0,
// and the far edge is the advanced one.
func placeRow(row *sqRow, value, x0, y0, x1, y1 float64) (float64, float64) {
	if row.dice {
		ny := y1
		if truthy(value) {
			ny = y0 + (y1-y0)*row.value/value
		}
		dice(row.children, row.value, x0, y0, x1, ny)
		if truthy(value) {
			y0 = ny
		}
	} else {
		nx := x1
		if truthy(value) {
			nx = x0 + (x1-x0)*row.value/value
		}
		slice(row.children, row.value, x0, y0, nx, y1)
		if truthy(value) {
			x0 = nx
		}
	}
	return x0, y0
}

func tileSquarify(ratio float64) tileFunc {
	return func(parent *Node, x0, y0, x1, y1 float64) {
		squarifyRatio(ratio, parent, x0, y0, x1, y1)
	}
}

// tileResquarify reuses the rows of the previous layout (same ratio) and only
// re-sizes them, keeping the arrangement stable when values change. The cache
// lives on the node, as upstream's parent._squarify does.
func tileResquarify(ratio float64) tileFunc {
	return func(parent *Node, x0, y0, x1, y1 float64) {
		if rows := parent.sq; rows != nil && parent.sqR == ratio {
			value := parent.Value
			for _, row := range rows {
				row.value = 0
				for _, c := range row.children {
					row.value += c.Value
				}
				x0, y0 = placeRow(row, value, x0, y0, x1, y1)
				value -= row.value
			}
		} else {
			parent.sq = squarifyRatio(ratio, parent, x0, y0, x1, y1)
			parent.sqR = ratio
		}
	}
}

// treemapConfig is the resolved d3.treemap() state.
type treemapConfig struct {
	tile                                                 tileFunc
	round                                                bool
	dx, dy                                               float64
	paddingInner                                         float64
	paddingTop, paddingRight, paddingBottom, paddingLeft float64
}

func (c *treemapConfig) layout(root *Node) {
	root.X0, root.Y0 = 0, 0
	root.X1, root.Y1 = c.dx, c.dy
	// paddingStack[d] is the inner padding applied to nodes of depth d.
	stack := []float64{0}
	root.eachBefore(func(n *Node) {
		p := stack[n.Depth]
		x0, y0, x1, y1 := n.X0+p, n.Y0+p, n.X1-p, n.Y1-p
		if x1 < x0 {
			x0 = (x0 + x1) / 2
			x1 = x0
		}
		if y1 < y0 {
			y0 = (y0 + y1) / 2
			y1 = y0
		}
		n.X0, n.Y0, n.X1, n.Y1 = x0, y0, x1, y1
		if len(n.Children) > 0 {
			p = c.paddingInner / 2
			for len(stack) <= n.Depth+1 {
				stack = append(stack, 0)
			}
			stack[n.Depth+1] = p
			x0 += c.paddingLeft - p
			y0 += c.paddingTop - p
			x1 -= c.paddingRight - p
			y1 -= c.paddingBottom - p
			if x1 < x0 {
				x0 = (x0 + x1) / 2
				x1 = x0
			}
			if y1 < y0 {
				y0 = (y0 + y1) / 2
				y1 = y0
			}
			c.tile(n, x0, y0, x1, y1)
		}
	})
	if c.round {
		root.eachBefore(roundNode)
	}
}

// partition is d3.partition(): an adjacency diagram (icicle) layout.
func partition(root *Node, dx, dy, padding float64, round bool) {
	n := float64(root.Height + 1)
	root.X0, root.Y0 = padding, padding
	root.X1 = dx
	root.Y1 = dy / n
	root.eachBefore(func(node *Node) {
		if len(node.Children) > 0 {
			d := float64(node.Depth)
			tileDice(node, node.X0, dy*(d+1)/n, node.X1, dy*(d+2)/n)
		}
		x0, y0 := node.X0, node.Y0
		x1, y1 := node.X1-padding, node.Y1-padding
		if x1 < x0 {
			x0 = (x0 + x1) / 2
			x1 = x0
		}
		if y1 < y0 {
			y0 = (y0 + y1) / 2
			y1 = y0
		}
		node.X0, node.Y0, node.X1, node.Y1 = x0, y0, x1, y1
	})
	if round {
		root.eachBefore(roundNode)
	}
}
