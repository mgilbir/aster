package raster

import (
	"errors"
	"fmt"
	"image"
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/imageref"
)

// rasterImage is a decoded bitmap in premultiplied RGBA.
type rasterImage struct {
	w, h int
	pix  []uint8
	mips []*rasterImage // the image halved once, twice, ...; built as drawing needs them
}

// level is the image halved k times, or as many times as it can be (to 1x1),
// and how many times that is. Halving averages 2x2 pixels in premultiplied
// space, so the colour of a transparent pixel does not bleed. Halvings are
// charged to r (nil: never stopped; see halve); once r stops, level returns
// the deepest level made.
func (img *rasterImage) level(k int, r *renderer) (*rasterImage, int) {
	for len(img.mips) < k {
		prev := img
		if n := len(img.mips); n > 0 {
			prev = img.mips[n-1]
		}
		if prev.w == 1 && prev.h == 1 {
			break
		}
		h := prev.halve(r)
		if h == nil {
			break
		}
		img.mips = append(img.mips, h)
	}
	k = min(k, len(img.mips))
	if k == 0 {
		return img, 0
	}
	return img.mips[k-1], k
}

// halve is the image at half its size, rounded up; at an odd edge the last
// pixel stands for the one beyond it. Its memory was charged with the image
// (imageBytes); its work, the pixels it reads, is charged to r's
// MaxPixelOps a band of rows at a time, polling r's context (ADR 0010): a
// halving of an image of 64 Mi pixels is seconds under the race detector. It
// is nil once r stops (nil r: never).
func (img *rasterImage) halve(r *renderer) *rasterImage {
	w, h := (img.w+1)/2, (img.h+1)/2
	pix := make([]uint8, w*h*4)
	rows := imageref.BandRows(w)
	for y := 0; y < h; y++ {
		if r != nil && y%rows == 0 && !r.chargeOps(4*min(rows, h-y)*w) {
			return nil
		}
		y0, y1 := 2*y, min(2*y+1, img.h-1)
		for x := 0; x < w; x++ {
			x0, x1 := 2*x, min(2*x+1, img.w-1)
			p00 := img.pix[(y0*img.w+x0)*4:]
			p10 := img.pix[(y0*img.w+x1)*4:]
			p01 := img.pix[(y1*img.w+x0)*4:]
			p11 := img.pix[(y1*img.w+x1)*4:]
			d := pix[(y*w+x)*4:]
			for c := 0; c < 4; c++ {
				d[c] = uint8((uint32(p00[c]) + uint32(p10[c]) + uint32(p01[c]) + uint32(p11[c]) + 2) / 4)
			}
		}
	}
	return &rasterImage{w: w, h: h, pix: pix}
}

// imageLimits are the bounds of one image, as imageref takes them.
func (lim Limits) imageLimits() imageref.Limits {
	return imageref.Limits{MaxBytes: lim.MaxImageBytes, MaxPixels: lim.MaxImagePixels, Err: errRasterLimit}
}

// decodeImage decodes a PNG, JPEG or GIF (its first frame) into premultiplied
// RGBA. Before it decodes, it charges the call's ledger for what decoding
// allocates: the decoder's own image, given back once converted, and the
// premultiplied copy with every halving drawing may make of it, which stay
// charged (kept, the bytes left charged, for the caller to give back). An
// image the ledger cannot hold fails with an error wrapping budget.ErrLimit.
// Decoding and the conversion poll the render's context throughout, and the
// conversion is charged to MaxPixelOps; once either stops, the render has
// failed (r.err) and the error is its.
func (r *renderer) decodeImage(data []byte) (img *rasterImage, kept int64, err error) {
	cfg, format, err := imageref.Config(data, r.lim.imageLimits())
	if err != nil {
		return nil, 0, err
	}
	w, h := cfg.Width, cfg.Height
	// What the decoder allocates, a progressive JPEG's coefficients and an
	// interlaced PNG's passes included (C20).
	decoded := imageref.DecodeBytes(data, cfg, format)
	kept = imageBytes(w, h)
	if err := r.ledger.Alloc(decoded + kept); err != nil {
		return nil, 0, fmt.Errorf("raster: decoding a %dx%d image: %w", w, h, err)
	}
	src, err := imageref.Decode(r.ctx, data, r.lim.imageLimits())
	if err != nil {
		r.ledger.Free(decoded + kept)
		if r.ctx != nil && errors.Is(err, r.ctx.Err()) {
			r.fail(err)
		}
		return nil, 0, err
	}
	b := src.Bounds()
	if !r.chargeOps(b.Dx() * b.Dy()) {
		r.ledger.Free(decoded + kept)
		return nil, 0, r.err
	}
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	// draw.Src into RGBA premultiplies.
	if err := imageref.DrawSrc(r.ctx, dst, dst.Bounds(), src, b.Min); err != nil {
		r.ledger.Free(decoded + kept)
		r.fail(err)
		return nil, 0, r.err
	}
	r.ledger.Free(decoded)
	return &rasterImage{w: b.Dx(), h: b.Dy(), pix: dst.Pix}, kept, nil
}

// imageBytes is the premultiplied RGBA of a w x h image and of every
// halving of it (see level), as it is kept for drawing.
func imageBytes(w, h int) int64 {
	n := budget.Mul(budget.Mul(int64(w), int64(h)), 4)
	for w > 1 || h > 1 {
		w, h = (w+1)/2, (h+1)/2
		n += budget.Mul(budget.Mul(int64(w), int64(h)), 4) // w, h <= 65535: no overflow
	}
	return n
}

// sampledLevel is what an image drawn through inv (device -> image pixels) is
// sampled from, and whether by area: an image drawn at half its size or more
// is the image itself, sampled bilinearly as it always was. One drawn smaller
// would skip pixels so, as bilinear sampling reads four for each drawn one:
//
//   - unrotated, each drawn pixel is the average of the pixels its footprint
//     covers, weighted by how much of each it covers, on the image or the copy
//     halved as many times as leaves the footprint between two and four
//     pixels a side (at most 25 read);
//   - rotated or skewed, it is sampled bilinearly from the copy halved as
//     many times as leaves the footprint between one and two pixels.
//
// Halved copies average 2x2 pixels in premultiplied space, made as drawing
// first needs them and charged to r (see halve; nil r for none). The matrix
// returned maps device pixels to the pixels of what is sampled.
func sampledLevel(r *renderer, img *rasterImage, inv matrix) (*rasterImage, matrix, bool) {
	f := max(math.Hypot(inv.a, inv.b), math.Hypot(inv.c, inv.d))
	if !(f > 2) {
		return img, inv, false
	}
	area := inv.b == 0 && inv.c == 0
	// The halvings are counted in float64 and capped before they become an
	// int: an infinite footprint (an image drawn at a subnormal size) must
	// not convert to an implementation-defined int (X1).
	kf := math.Floor(math.Log2(f))
	if area {
		kf--
	}
	k := 62
	if kf < 62 {
		k = int(kf)
	}
	lvl, k := img.level(k, r)
	s := float64(int(1) << k)
	return lvl, matrix{inv.a / s, inv.b / s, inv.c / s, inv.d / s, inv.e / s, inv.f / s}, area
}

// imageShader samples an image through an inverse transform.
type imageShader struct {
	img     *rasterImage
	inv     matrix // device -> image pixel space
	nearest bool
	area    bool // averaged over each pixel's footprint (see sampledLevel)
	opacity uint32
}

func (s *imageShader) fetch(x, y int) (r, g, b, a float64) {
	if x < 0 {
		x = 0
	} else if x >= s.img.w {
		x = s.img.w - 1
	}
	if y < 0 {
		y = 0
	} else if y >= s.img.h {
		y = s.img.h - 1
	}
	i := (y*s.img.w + x) * 4
	p := s.img.pix[i : i+4 : i+4]
	return float64(p[0]), float64(p[1]), float64(p[2]), float64(p[3])
}

// average is the mean of the image over the box [u0,u1) x [v0,v1), in image
// pixels, each pixel weighted by how much of it the box covers. Past the
// image's edges is its edge pixels, as fetch reads it, so only the image's own
// pixels are visited, the edge ones weighted for what lies beyond them: a box
// many times the image's size, from a transform that shrinks it to nothing,
// costs no more than the image.
func (s *imageShader) average(u0, v0, u1, v1 float64) (r, g, b, a float64) {
	if !(u1 > u0) || !(v1 > v0) {
		return s.bilinear((u0+u1)/2, (v0+v1)/2)
	}
	// span is how much of [lo,hi) pixel i of n covers, the first and last
	// reaching out to everything before and after them.
	span := func(lo, hi float64, i, n int) float64 {
		a, b := float64(i), float64(i+1)
		if i == 0 {
			a = math.Inf(-1)
		}
		if i == n-1 {
			b = math.Inf(1)
		}
		return math.Max(0, math.Min(hi, b)-math.Max(lo, a))
	}
	// The pixel index of v, within the n pixels, clamped as a float first:
	// converting an infinity to an int is not defined.
	index := func(v float64, n int) int { return int(math.Max(0, math.Min(float64(n-1), v))) }
	y0, y1 := index(math.Floor(v0), s.img.h), index(math.Ceil(v1)-1, s.img.h)
	x0, x1 := index(math.Floor(u0), s.img.w), index(math.Ceil(u1)-1, s.img.w)
	var wsum float64
	for y := y0; y <= y1; y++ {
		wy := span(v0, v1, y, s.img.h)
		for x := x0; x <= x1; x++ {
			w := wy * span(u0, u1, x, s.img.w)
			pr, pg, pb, pa := s.fetch(x, y)
			r, g, b, a = r+pr*w, g+pg*w, b+pb*w, a+pa*w
			wsum += w
		}
	}
	if wsum <= 0 {
		return s.bilinear((u0+u1)/2, (v0+v1)/2)
	}
	return r / wsum, g / wsum, b / wsum, a / wsum
}

// bilinear samples the image at (u, v), in image pixels.
func (s *imageShader) bilinear(u, v float64) (r, g, b, a float64) {
	u -= 0.5
	v -= 0.5
	fx, fy := math.Floor(u), math.Floor(v)
	tx, ty := u-fx, v-fy
	ix, iy := int(fx), int(fy)
	r00, g00, b00, a00 := s.fetch(ix, iy)
	r10, g10, b10, a10 := s.fetch(ix+1, iy)
	r01, g01, b01, a01 := s.fetch(ix, iy+1)
	r11, g11, b11, a11 := s.fetch(ix+1, iy+1)
	w00, w10, w01, w11 := (1-tx)*(1-ty), tx*(1-ty), (1-tx)*ty, tx*ty
	r = r00*w00 + r10*w10 + r01*w01 + r11*w11
	g = g00*w00 + g10*w10 + g01*w01 + g11*w11
	b = b00*w00 + b10*w10 + b01*w01 + b11*w11
	a = a00*w00 + a10*w10 + a01*w01 + a11*w11
	return r, g, b, a
}

func (s *imageShader) shadeRow(y, x0 int, dst []uint8) {
	n := len(dst) / 4
	py := float64(y) + 0.5
	for i := 0; i < n; i++ {
		px := float64(x0+i) + 0.5
		// Kept finite (an overflowing transform gives Inf - Inf) before they
		// become pixel indices; past 1e9 every sample is an edge pixel.
		u := clampCoord(s.inv.a*px + s.inv.c*py + s.inv.e)
		v := clampCoord(s.inv.b*px + s.inv.d*py + s.inv.f)
		var r, g, b, a float64
		switch {
		case s.nearest:
			r, g, b, a = s.fetch(int(math.Floor(u)), int(math.Floor(v)))
		case s.area:
			// The pixel's footprint on the level: the device pixel's square,
			// which an unrotated transform maps to a box.
			hu, hv := math.Abs(s.inv.a)/2, math.Abs(s.inv.d)/2
			r, g, b, a = s.average(u-hu, v-hv, u+hu, v+hv)
		default:
			r, g, b, a = s.bilinear(u, v)
		}
		if s.opacity != 255 {
			k := float64(s.opacity) / 255
			r, g, b, a = r*k, g*k, b*k, a*k
		}
		dst[i*4] = uint8(r + 0.5)
		dst[i*4+1] = uint8(g + 0.5)
		dst[i*4+2] = uint8(b + 0.5)
		dst[i*4+3] = uint8(a + 0.5)
	}
}

// imageFor decodes the image an <image> element refers to: a data: URI, or
// the bytes Options.Images fetched. It is decoded once per distinct href,
// however many elements draw it, and kept, charged to the ledger, for the
// rest of the render (dropImages gives it back); one that cannot be read is
// reported once (Options.ImageError) and remembered as such.
// Unsupported or unreachable images yield nil, as they are skipped by resvg;
// an image over the limits fails the render.
func (r *renderer) imageFor(n *node) (*rasterImage, error) {
	return r.imageHref(strings.TrimSpace(n.str(aHref)))
}

// imageHref is imageFor of an href: an <image>'s or an feImage's.
func (r *renderer) imageHref(href string) (*rasterImage, error) {
	if href == "" {
		return nil, nil
	}
	if img, cached := r.imgs[href]; cached {
		return img, nil
	}
	var img *rasterImage
	data, err := r.fetched[href], error(nil)
	if data == nil {
		// Only data: URIs are decoded here: the rasterizer never touches the
		// network or the filesystem; other images are fetched by Options.Images.
		if imageref.IsData(href) {
			data, err = imageref.DataURI(href, r.lim.imageLimits())
		} else {
			err = errNotFetched
		}
	}
	var kept int64
	if err == nil {
		img, kept, err = r.decodeImage(data)
	}
	if err != nil && r.ctx != nil && errors.Is(err, r.ctx.Err()) {
		// The render stopped (its context ended) while decoding: no image
		// failed to read, and nothing is reported.
		return nil, err
	}
	if err != nil {
		img = nil
		if errors.Is(err, budget.ErrLimit) {
			r.fail(err)
		}
		if r.imageErr != nil {
			r.imageErr(href, err)
		}
	}
	if r.imgs == nil {
		r.imgs = map[string]*rasterImage{}
	}
	r.imgs[href] = img
	r.imageBytes += kept
	return img, err
}

// errNotFetched is the error of an image Options.Images did not fetch.
var errNotFetched = errors.New("raster: image not fetched")

// dropImages gives back what the decoded images are charged.
func (r *renderer) dropImages() {
	r.ledger.Free(r.imageBytes)
	r.imgs, r.imageBytes = nil, 0
}

func (r *renderer) renderImage(n *node, st *state, opacity float64, blend blendMode) {
	if !st.visible {
		return
	}
	img, _ := r.imageFor(n)
	if img == nil {
		return
	}
	x := st.length(n.str(aX), 0, 0)
	y := st.length(n.str(aY), 1, 0)
	w := st.length(n.str(aWidth), 0, float64(img.w))
	h := st.length(n.str(aHeight), 1, float64(img.h))
	if n.str(aWidth) == "" && n.str(aHeight) != "" {
		w = h * float64(img.w) / float64(img.h)
	} else if n.str(aHeight) == "" && n.str(aWidth) != "" {
		h = w * float64(img.h) / float64(img.w)
	}
	if !(w > 0 && h > 0) {
		return
	}
	r.drawImage(img, x, y, w, h, n.str(aPreserveAspectRatio), st, opacity, blend)
}

// drawImage draws img into the viewport (x, y, w, h), w and h positive,
// placed by preserveAspectRatio par: an <image>'s, or an feImage's
// (usvg's convert_inner, which both share).
func (r *renderer) drawImage(img *rasterImage, x, y, w, h float64, par string, st *state, opacity float64, blend blendMode) {
	vb := rect{0, 0, float64(img.w), float64(img.h)}
	im := viewBoxTransform(vb, w, h, par)
	full := st.ctm.mul(translate(x, y)).mul(im) // image pixels -> device
	inv, ok := full.invert()
	if !ok || !full.isFinite() || !inv.isFinite() {
		// An image drawn so small its inverse overflows covers no pixel.
		return
	}
	// Visible region: the image rectangle, clipped to the viewport (x,y,w,h)
	// for "slice".
	region := rect{0, 0, vb.x1, vb.y1}
	p := &path{}
	p.addRect(region.x0, region.y0, region.w(), region.h())
	area := full
	slice := strings.Contains(par, "slice")
	clip := st.clip
	if slice {
		clip = r.rectClip(st, st.ctm, rect{x, y, x + w, y + h})
		if clip.r.empty() {
			return
		}
	}
	shader := &imageShader{
		img:     img,
		inv:     inv,
		nearest: st.pixelated,
		opacity: unitByte(opacity),
	}
	if !shader.nearest {
		shader.img, shader.inv, shader.area = sampledLevel(r, img, inv)
	}
	var prev *canvas
	if blend != blendNormal {
		if prev = r.pushLayer(); prev == nil {
			return
		}
		shader.opacity = 255
	}
	sub := *st
	sub.clip = clip
	r.fl.flatten(p, area, shapeTol)
	r.fillPolys(&r.fl, false, paintSrc{sh: shader}, &sub)
	if blend != blendNormal {
		r.popLayer(prev, opacity, blend)
	}
}
