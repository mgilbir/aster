package raster

import (
	"errors"
	"image"
	"image/draw"
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/imageref"
)

// rasterImage is a decoded bitmap in premultiplied RGBA.
type rasterImage struct {
	w, h int
	pix  []uint8
}

// imageLimits are the bounds of one image, as imageref takes them.
func (lim Limits) imageLimits() imageref.Limits {
	return imageref.Limits{MaxBytes: lim.MaxImageBytes, MaxPixels: lim.MaxImagePixels, Err: errLimit}
}

// decodeImage decodes a PNG, JPEG or GIF (its first frame) into premultiplied
// RGBA.
func decodeImage(data []byte, lim Limits) (*rasterImage, error) {
	img, err := imageref.Decode(data, lim.imageLimits())
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src) // draw.Src into RGBA premultiplies
	return &rasterImage{w: b.Dx(), h: b.Dy(), pix: dst.Pix}, nil
}

// imageShader samples an image through an inverse transform.
type imageShader struct {
	img     *rasterImage
	inv     matrix // device -> image pixel space
	nearest bool
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

func (s *imageShader) shadeRow(y, x0 int, dst []uint8) {
	n := len(dst) / 4
	py := float64(y) + 0.5
	for i := 0; i < n; i++ {
		px := float64(x0+i) + 0.5
		u := s.inv.a*px + s.inv.c*py + s.inv.e
		v := s.inv.b*px + s.inv.d*py + s.inv.f
		var r, g, b, a float64
		if s.nearest {
			r, g, b, a = s.fetch(int(math.Floor(u)), int(math.Floor(v)))
		} else {
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

// imageFor decodes (once) the image an <image> element refers to: a data:
// URI, or the bytes Options.Images fetched.
// Unsupported or unreachable images yield nil, as they are skipped by resvg;
// an image over the limits fails the render.
func (r *renderer) imageFor(n *node) (*rasterImage, error) {
	href := n.str(aHref)
	if href == "" {
		return nil, nil
	}
	if img, cached := r.imgs[n]; cached {
		return img, nil
	}
	var img *rasterImage
	data, err := r.fetched[strings.TrimSpace(href)], error(nil)
	if data == nil {
		// Only data: URIs are decoded here: the rasterizer never touches the
		// network or the filesystem; other images are fetched by Options.Images.
		data, err = imageref.DataURI(href, r.lim.imageLimits())
	}
	if err == nil {
		img, err = decodeImage(data, r.lim)
	}
	if err != nil {
		img = nil
		if errors.Is(err, errLimit) {
			r.fail(err)
		}
	}
	if r.imgs == nil {
		r.imgs = map[*node]*rasterImage{}
	}
	r.imgs[n] = img
	return img, err
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
	vb := rect{0, 0, float64(img.w), float64(img.h)}
	par := n.str(aPreserveAspectRatio)
	im := viewBoxTransform(vb, w, h, par)
	full := st.ctm.mul(translate(x, y)).mul(im) // image pixels -> device
	inv, ok := full.invert()
	if !ok || !full.isFinite() {
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
		opacity: uint32(math.Round(math.Max(0, math.Min(1, opacity)) * 255)),
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
