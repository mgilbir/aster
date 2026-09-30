package wordcloud

import (
	"context"
	"math"
	"sort"

	"github.com/mgilbir/aster/internal/jsmath"

	"github.com/mgilbir/aster/internal/jsval"
)

const cloudRadians = math.Pi / 180

// Work limits. Specifications are untrusted, so the spec-driven loops are
// bounded far above anything a real chart needs.
const (
	MaxWords       = 50000
	maxArea        = 1 << 28 // pixels of the placement board
	maxSpiralSteps = 1 << 24
)

// Spiral names the search path a word follows away from its start position.
type Spiral string

const (
	Archimedean Spiral = "archimedean"
	Rectangular Spiral = "rectangular"
)

// word is one entry of the layout: the inputs resolved for a datum plus the
// sprite state upstream tracks on the same object.
type word struct {
	text                string
	font, style, weight jsval.Value // raw accessor results, echoed to the output
	family, styleS, wtS string
	rotate              jsval.Value
	rotDeg              float64
	size                int
	padding             float64
	x, y                int
	x0, y0, x1, y1      int
	xoff, yoff          int
	width, height       int
	hasText             bool
	sprite              []int32
	hasSprite           bool
	datum               jsval.Value
}

type layout struct {
	ctx      context.Context
	size     [2]float64
	spiral   Spiral
	random   func() float64
	renderer TextRenderer
	mask     *Mask
	sprite   []int32 // shared scratch, as upstream's per-call `sprite` array
	board    []int32
	sw       int
}

// toInt32 is JavaScript's ToInt32 (the effect of `~~x` and `x | 0`).
func toInt32(f float64) int {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	f = math.Trunc(f)
	if f >= -2147483648 && f <= 2147483647 {
		return int(f)
	}
	return int(int32(uint32(int64(math.Mod(f, 4294967296)))))
}

func lshift(a int32, n int) int32  { return a << (uint(n) & 31) }
func urshift(a int32, n int) int32 { return int32(uint32(a) >> (uint(n) & 31)) }

func (l *layout) run(words []*word) ([]*word, error) {
	sw := int(toInt32(l.size[0]) >> 5)
	rows := int(math.Ceil(l.size[1]))
	l.sw = sw
	l.board = make([]int32, sw*rows+sw+2)
	l.mask = newMask()

	var tags []*word
	var bounds *[2][2]int // [0]=min(x,y), [1]=max(x,y)
	for i := range words {
		if i&15 == 0 {
			if err := l.ctx.Err(); err != nil {
				return nil, err
			}
		}
		d := words[i]
		d.x = toInt32(l.size[0]*(l.random()+.5)) >> 1
		d.y = toInt32(l.size[1]*(l.random()+.5)) >> 1
		l.makeSprite(words, i)
		if d.hasText {
			ok, err := l.place(d, bounds)
			if err != nil {
				return nil, err
			}
			if ok {
				tags = append(tags, d)
				if bounds != nil {
					b0, b1 := &bounds[0], &bounds[1]
					if d.x+d.x0 < b0[0] {
						b0[0] = d.x + d.x0
					}
					if d.y+d.y0 < b0[1] {
						b0[1] = d.y + d.y0
					}
					if d.x+d.x1 > b1[0] {
						b1[0] = d.x + d.x1
					}
					if d.y+d.y1 > b1[1] {
						b1[1] = d.y + d.y1
					}
				} else {
					bounds = &[2][2]int{{d.x + d.x0, d.y + d.y0}, {d.x + d.x1, d.y + d.y1}}
				}
				d.x -= toInt32(l.size[0]) >> 1
				d.y -= toInt32(l.size[1]) >> 1
			}
		}
	}
	return tags, nil
}

// spiralFn returns the step function for the configured spiral.
func (l *layout) spiralFn() func(t int) (float64, float64) {
	switch l.spiral {
	case Rectangular:
		dy := 4.0
		dx := dy * l.size[0] / l.size[1]
		x, y := 0.0, 0.0
		return func(t int) (float64, float64) {
			sign := 1.0
			if t < 0 {
				sign = -1
			}
			// Triangular numbers: T_n = n * (n + 1) / 2.
			switch toInt32(math.Sqrt(1+4*sign*float64(t))-sign) & 3 {
			case 0:
				x += dx
			case 1:
				y += dy
			case 2:
				x -= dx
			default:
				y -= dy
			}
			return x, y
		}
	default:
		e := l.size[0] / l.size[1]
		return func(t int) (float64, float64) {
			tt := float64(t) * .1
			return float64(e*tt) * jsmath.Cos(tt), tt * jsmath.Sin(tt)
		}
	}
}

func (l *layout) place(tag *word, bounds *[2][2]int) (bool, error) {
	startX, startY := tag.x, tag.y
	maxDelta := jsmath.Hypot(l.size[0], l.size[1])
	s := l.spiralFn()
	dt := -1
	if l.random() < .5 {
		dt = 1
	}
	t := -dt
	for steps := 0; ; steps++ {
		if steps&4095 == 0 {
			if err := l.ctx.Err(); err != nil {
				return false, err
			}
			if steps > maxSpiralSteps {
				return false, nil
			}
		}
		t += dt
		fx, fy := s(t)
		dx, dy := toInt32(fx), toInt32(fy)
		if math.Min(math.Abs(float64(dx)), math.Abs(float64(dy))) >= maxDelta {
			break
		}
		tag.x = startX + dx
		tag.y = startY + dy
		if float64(tag.x+tag.x0) < 0 || float64(tag.y+tag.y0) < 0 ||
			float64(tag.x+tag.x1) > l.size[0] || float64(tag.y+tag.y1) > l.size[1] {
			continue
		}
		if bounds == nil || !l.collide(tag) {
			if bounds == nil || collideRects(tag, bounds) {
				l.stamp(tag)
				tag.sprite, tag.hasSprite = nil, false
				return true, nil
			}
		}
	}
	return false, nil
}

func (l *layout) get(i int) int32 {
	if i < 0 || i >= len(l.board) {
		return 0
	}
	return l.board[i]
}

// collide is upstream's mask-based collision test. The shift counts are taken
// modulo 32 (JavaScript semantics), which makes a zero sub-word offset OR the
// previous sprite word in unshifted; that is upstream's behaviour and is kept.
func (l *layout) collide(tag *word) bool {
	w := tag.width >> 5
	lx := tag.x - (w << 4)
	sx := lx & 0x7f
	msx := 32 - sx
	h := tag.y1 - tag.y0
	x := (tag.y+tag.y0)*l.sw + (lx >> 5)
	var last int32
	for j := 0; j < h; j++ {
		last = 0
		for i := 0; i <= w; i++ {
			var cur int32
			prev := lshift(last, msx)
			if i < w {
				last = tag.sprite[j*w+i]
				cur = urshift(last, sx)
			}
			if (prev|cur)&l.get(x+i) != 0 {
				return true
			}
		}
		x += l.sw
	}
	return false
}

func (l *layout) stamp(tag *word) {
	w := tag.width >> 5
	lx := tag.x - (w << 4)
	sx := lx & 0x7f
	msx := 32 - sx
	h := tag.y1 - tag.y0
	x := (tag.y+tag.y0)*l.sw + (lx >> 5)
	var last int32
	for j := 0; j < h; j++ {
		last = 0
		for i := 0; i <= w; i++ {
			var cur int32
			prev := lshift(last, msx)
			if i < w {
				last = tag.sprite[j*w+i]
				cur = urshift(last, sx)
			}
			if k := x + i; k >= 0 && k < len(l.board) {
				l.board[k] |= prev | cur
			}
		}
		x += l.sw
	}
}

func collideRects(a *word, b *[2][2]int) bool {
	return a.x+a.x1 > b[0][0] && a.x+a.x0 < b[1][0] && a.y+a.y1 > b[0][1] && a.y+a.y0 < b[1][1]
}

// makeSprite rasterises words[di:] onto the shared sheet until it is full,
// then cuts each word's bitmap out of it. Upstream re-walks the words before
// di as well, but those are already placed (or rejected) and never read again,
// so they are skipped here.
func (l *layout) makeSprite(words []*word, di int) {
	if words[di].hasSprite {
		return
	}
	l.mask.clear()
	x, y, maxh := 0, 0, 0
	n := len(words)
	start := di
	for ; di < n; di++ {
		d := words[di]
		f := Font{Style: d.styleS, Weight: d.wtS, Family: d.family, Px: toInt32(float64(d.size+1) / 1)}
		w := l.renderer.Measure(f, d.text+"m")
		h := d.size << 1
		if d.rotDeg != 0 && !math.IsNaN(d.rotDeg) {
			sr, cr := jsmath.Sin(d.rotDeg*cloudRadians), jsmath.Cos(d.rotDeg*cloudRadians)
			wcr, wsr := float64(w*cr), float64(w*sr)
			hcr, hsr := float64(float64(h)*cr), float64(float64(h)*sr)
			w = float64(toInt32(math.Max(math.Abs(wcr+hsr), math.Abs(wcr-hsr))+0x1f) >> 5 << 5)
			h = toInt32(math.Max(math.Abs(wsr+hcr), math.Abs(wsr-hcr)))
		} else {
			w = float64(toInt32(w+0x1f) >> 5 << 5)
		}
		wi := int(w)
		if h > maxh {
			maxh = h
		}
		if x+wi >= sheetW {
			x = 0
			y += maxh
			maxh = 0
		}
		if y+h >= sheetH {
			break
		}
		angle := 0.0
		if d.rotDeg != 0 && !math.IsNaN(d.rotDeg) {
			angle = d.rotDeg * cloudRadians
		}
		stroke := 0.0
		if d.padding != 0 && !math.IsNaN(d.padding) {
			stroke = 2 * d.padding
		}
		l.renderer.Draw(l.mask, f, d.text, float64(x+(wi>>1)), float64(y+(h>>1)), angle, stroke)
		d.width, d.height = wi, h
		d.xoff, d.yoff = x, y
		d.x1 = wi >> 1
		d.y1 = h >> 1
		d.x0 = -d.x1
		d.y0 = -d.y1
		d.hasText = true
		x += wi
	}
	for k := di - 1; k >= start; k-- {
		d := words[k]
		if !d.hasText {
			continue
		}
		w := d.width
		w32 := w >> 5
		h := d.y1 - d.y0
		need := h * w32
		if need > len(l.sprite) {
			l.sprite = append(l.sprite, make([]int32, need-len(l.sprite))...)
		}
		for i := 0; i < need; i++ {
			l.sprite[i] = 0
		}
		x, y := d.xoff, d.yoff
		seen := false
		seenRow := -1
		for j := 0; j < h; j++ {
			for i := 0; i < w; i++ {
				if l.mask.get((y+j)*sheetW + x + i) {
					l.sprite[w32*j+(i>>5)] |= int32(uint32(1) << (31 - uint(i%32)))
					seen = true
				}
			}
			if seen {
				seenRow = j
			} else {
				d.y0++
				h--
				j--
				y++
			}
		}
		d.y1 = d.y0 + seenRow
		cnt := (d.y1 - d.y0) * w32
		if cnt < 0 {
			cnt = 0
		}
		d.sprite = append([]int32(nil), l.sprite[:cnt]...)
		d.hasSprite = true
	}
}

// sortWords orders by descending size; the sort is stable as in modern engines.
func sortWords(ws []*word) {
	sort.SliceStable(ws, func(i, j int) bool { return ws[i].size > ws[j].size })
}
