package raster

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"net/url"
	"strings"
)

// rasterImage is a decoded bitmap in premultiplied RGBA.
type rasterImage struct {
	w, h int
	pix  []uint8
}

// decodeDataURI decodes a data: URI holding PNG or JPEG. Only data URIs are
// accepted: the rasterizer never touches the network or filesystem.
func decodeDataURI(href string, lim Limits) (*rasterImage, error) {
	href = strings.TrimSpace(href)
	if !strings.HasPrefix(href, "data:") {
		return nil, errors.New("only data: image URIs are supported")
	}
	comma := strings.IndexByte(href, ',')
	if comma < 0 {
		return nil, errors.New("malformed data URI")
	}
	meta := href[5:comma]
	payload := href[comma+1:]
	var data []byte
	if strings.HasSuffix(strings.ToLower(meta), ";base64") {
		if base64.StdEncoding.DecodedLen(len(payload)) > lim.MaxImageBytes+4 {
			return nil, fmt.Errorf("image data exceeds %d bytes", lim.MaxImageBytes)
		}
		// Strip whitespace that XML pretty-printing may have inserted.
		clean := strings.Map(func(r rune) rune {
			switch r {
			case ' ', '\n', '\r', '\t':
				return -1
			}
			return r
		}, payload)
		var err error
		data, err = base64.StdEncoding.DecodeString(clean)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(clean, "="))
			if err != nil {
				return nil, fmt.Errorf("invalid base64 image data: %w", err)
			}
		}
	} else {
		if len(payload) > lim.MaxImageBytes {
			return nil, fmt.Errorf("image data exceeds %d bytes", lim.MaxImageBytes)
		}
		s, err := url.PathUnescape(payload)
		if err != nil {
			return nil, err
		}
		data = []byte(s)
	}
	if len(data) > lim.MaxImageBytes {
		return nil, fmt.Errorf("image data exceeds %d bytes", lim.MaxImageBytes)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("unsupported image: %w", err)
	}
	if format != "png" && format != "jpeg" {
		return nil, fmt.Errorf("unsupported image format %q", format)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 65535 || cfg.Height > 65535 ||
		cfg.Width*cfg.Height > lim.MaxImagePixels {
		return nil, fmt.Errorf("image dimensions %dx%d exceed the limit", cfg.Width, cfg.Height)
	}
	var img image.Image
	if format == "png" {
		img, err = png.Decode(bytes.NewReader(data))
	} else {
		img, err = jpeg.Decode(bytes.NewReader(data))
	}
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

// imageFor decodes (once) the data: image an <image> element refers to.
// Unsupported or unreachable images yield nil, as they are skipped by resvg.
func (r *renderer) imageFor(n *node) (*rasterImage, error) {
	href := n.str(aHref)
	if href == "" {
		return nil, nil
	}
	if img, cached := r.imgs[n]; cached {
		return img, nil
	}
	img, err := decodeDataURI(href, r.lim)
	if err != nil {
		img = nil
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
