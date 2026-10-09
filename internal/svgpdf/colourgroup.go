package svgpdf

import (
	"fmt"
	"math"
	"strings"

	"github.com/mgilbir/forme/shape"
	pdf0 "github.com/mgilbir/pdf0"
)

// formDef is a form XObject: an isolated transparency group, the content of
// a colour glyph's group, drawn in the user space it is drawn in.
type formDef struct {
	res     string
	content []byte
	bbox    [4]float64
}

// stream is the form's XObject, drawing with resources.
func (f *formDef) stream(resources pdf0.Object) (*pdf0.Stream, error) {
	data, err := compress(f.content)
	if err != nil {
		return nil, fmt.Errorf("svgpdf: compressing a form: %w", err)
	}
	group := &pdf0.Dictionary{}
	group.Set("Type", pdf0.Name("Group"))
	group.Set("S", pdf0.Name("Transparency"))
	group.Set("CS", pdf0.Name("DeviceRGB"))
	group.Set("I", pdf0.Boolean(true))
	s := &pdf0.Stream{Data: data}
	s.Dict.Set("Type", pdf0.Name("XObject"))
	s.Dict.Set("Subtype", pdf0.Name("Form"))
	s.Dict.Set("BBox", pdf0.Array{pdf0.Real(f.bbox[0]), pdf0.Real(f.bbox[1]), pdf0.Real(f.bbox[2]), pdf0.Real(f.bbox[3])})
	s.Dict.Set("Group", group)
	s.Dict.Set("Resources", resources)
	s.Dict.Set("Filter", pdf0.Name("FlateDecode"))
	s.Dict.Set("Length", pdf0.Integer(len(data)))
	return s, nil
}

// form registers a form XObject of content over bbox, once for the same
// content and box, and returns its resource name.
func (r *renderer) form(content []byte, bbox [4]float64) string {
	key := fmt.Sprint(bbox) + "\x00" + string(content)
	if f, ok := r.formIndex[key]; ok {
		return f.res
	}
	f := &formDef{res: fmt.Sprintf("Fm%d", len(r.forms)), content: content, bbox: bbox}
	if r.formIndex == nil {
		r.formIndex = map[string]*formDef{}
	}
	r.formIndex[key] = f
	r.forms = append(r.forms, f)
	return f.res
}

// pdfBlend names COLR's blend modes as PDF's.
var pdfBlend = map[shape.CompositeMode]string{
	shape.CompositeScreen: "Screen", shape.CompositeOverlay: "Overlay",
	shape.CompositeDarken: "Darken", shape.CompositeLighten: "Lighten",
	shape.CompositeColorDodge: "ColorDodge", shape.CompositeColorBurn: "ColorBurn",
	shape.CompositeHardLight: "HardLight", shape.CompositeSoftLight: "SoftLight",
	shape.CompositeDifference: "Difference", shape.CompositeExclusion: "Exclusion",
	shape.CompositeMultiply: "Multiply",
	shape.CompositeHSLHue:   "Hue", shape.CompositeHSLSaturation: "Saturation",
	shape.CompositeHSLColor: "Color", shape.CompositeHSLLuminosity: "Luminosity",
}

// porterDuff reports whether a mode is a Porter-Duff operator PDF has no
// blend mode for, which composite draws from the two sides as forms.
func porterDuff(m shape.CompositeMode) bool {
	_, blend := pdfBlend[m]
	return m != shape.CompositeSrcOver && !blend
}

// pdfGroup is an open group of a glyph's painting: what the writer held
// before it, and how many q's were open then.
type pdfGroup struct {
	buf   []byte
	cur   streamState
	stack []streamState
	depth int
}

// beginGroup starts writing a group's content into a buffer of its own.
func (p *pdfPainter) beginGroup() {
	w := p.r.w
	p.groups = append(p.groups, pdfGroup{w.buf, w.cur, w.stack, p.depth})
	ctm := w.cur.ctm
	w.buf, w.stack = nil, nil
	w.cur = defaultStreamState()
	w.cur.ctm = ctm
}

// endGroup returns a group's content, the writer back as it was before it.
func (p *pdfPainter) endGroup() []byte {
	w := p.r.w
	g := p.groups[len(p.groups)-1]
	p.groups = p.groups[:len(p.groups)-1]
	content := w.buf
	w.buf, w.cur, w.stack = g.buf, g.cur, g.stack
	return content
}

func (p *pdfPainter) PushGroup() { p.beginGroup() }

// PopGroup combines the group, the source, with what its enclosing group
// holds so far, the backdrop, in mode: source-over by drawing it; a blend
// mode by drawing it as a form under the mode; a Porter-Duff operator by
// drawing either side, both, or one masked by the alpha of the other.
// colourCheck sends a glyph whose enclosing group has a clip or transform
// open around the group to an image, as the backdrop is then not whole.
func (p *pdfPainter) PopGroup(mode shape.CompositeMode) {
	// The glyph's own group is closed by drawColourGlyph, not by a pop.
	if len(p.groups) <= 1 {
		return
	}
	p.closeTo(p.groups[len(p.groups)-1].depth)
	src := p.endGroup()
	w := p.r.w
	wrap := func(b []byte) []byte {
		if len(b) == 0 {
			return nil
		}
		out := append([]byte("q\n"), b...)
		return append(out, "Q\n"...)
	}
	// A Porter-Duff operator rewrites the backdrop: one with a clip or
	// transform still open in it is composited by compositeOpen.
	if k := p.depth - p.groups[len(p.groups)-1].depth; porterDuff(mode) && k > 0 && len(p.r.w.stack) >= k {
		p.compositeOpen(mode, src, k)
		return
	}
	if mode == shape.CompositeSrcOver {
		w.buf = append(w.buf, wrap(src)...)
		return
	}
	box := p.bbox()
	if bm, ok := pdfBlend[mode]; ok {
		if len(src) == 0 {
			return
		}
		fs := p.pageForm(src, box)
		w.save()
		p.toForm()
		w.stateGS("bm:"+bm, gsEntry{bm: bm})
		w.drawXObject(fs)
		w.restore()
		return
	}
	dst := w.buf
	// masked draws the form of b under the alpha of the form of mask,
	// inverted for an Out operator.
	masked := func(b, mask []byte, invert bool) []byte {
		if len(b) == 0 {
			return nil
		}
		if len(mask) == 0 {
			if invert {
				return wrap(b)
			}
			return nil
		}
		fb := p.pageForm(b, box)
		saved := w.buf
		w.buf = nil
		w.save()
		if invert {
			p.setInvertedMask(p.invertedMaskForm(mask, p.formMatrix()))
		} else {
			fm := p.pageForm(mask, box)
			p.toForm()
			w.stateGS(fmt.Sprintf("mask:%s:false", fm), gsEntry{mask: fm})
		}
		w.drawXObject(fb)
		w.restore()
		out := w.buf
		w.buf = saved
		return out
	}
	var out []byte
	switch mode {
	case shape.CompositeClear:
	case shape.CompositeSrc:
		out = wrap(src)
	case shape.CompositeDest:
		out = dst
	case shape.CompositeDestOver:
		out = append(wrap(src), wrap(dst)...)
	case shape.CompositeSrcIn:
		out = masked(src, dst, false)
	case shape.CompositeDestIn:
		out = masked(dst, src, false)
	case shape.CompositeSrcOut:
		out = masked(src, dst, true)
	case shape.CompositeDestOut:
		out = masked(dst, src, true)
	case shape.CompositeSrcAtop:
		out = append(wrap(dst), masked(src, dst, false)...)
	case shape.CompositeDestAtop:
		out = append(wrap(src), masked(dst, src, false)...)
	default:
		// Xor and Plus: colourCheck draws such a glyph as an image.
		out = dst
	}
	w.buf = append(dst[:0:0], out...)
}

// pageForm registers a form of content, drawn in the current user space
// within box, as a form in the glyph's page space: page units, from the
// corner of the glyph's box on the page. Its content first sets the current
// transform there, and its BBox is box there. It is drawn after toForm.
//
// Quartz draws nothing of a soft mask whose group's coordinates run past the
// page's own, as a glyph's font units, in the thousands, do; a glyph's page
// space runs from zero to its size. And it is the same wherever the glyph
// is, so that every copy of a glyph at one size shares its forms.
func (p *pdfPainter) pageForm(content []byte, box [4]float64) string {
	return p.formAt(content, p.formMatrix(), box)
}

// formAt is pageForm for content drawn in a user space m maps into the
// glyph's page space, within box there.
func (p *pdfPainter) formAt(content []byte, m Matrix, box [4]float64) string {
	return p.formIn(content, m, mapBox(m, box))
}

// formIn is a form of content drawn in a user space m maps into the glyph's
// page space, its BBox bbox in the glyph's page space.
func (p *pdfPainter) formIn(content []byte, m Matrix, bbox [4]float64) string {
	c := appendMatrix(nil, m)
	c = append(c, " cm\n"...)
	c = append(c, content...)
	return p.r.form(c, bbox)
}

// invertedMaskForm is a form for a soft mask inverted by its transfer
// function, of content drawn in a user space m maps into the glyph's page
// space: in the page's own space, its BBox the page. It is set by
// setInvertedMask. Quartz applies an inverted mask only when it is set with
// the default transform in effect and its BBox covers the page, and hides
// what it masks otherwise; PDFium draws it either way.
func (p *pdfPainter) invertedMaskForm(content []byte, m Matrix) string {
	m.E += p.origin[0]
	m.F += p.origin[1]
	page := [4]float64{0, 0, p.r.pageW, p.r.pageH}
	if !(page[2] > 0 && page[3] > 0) {
		page = [4]float64{-big, -big, big, big}
	}
	return p.formIn(content, m, page)
}

// setInvertedMask sets the inverted soft mask of form fm, and leaves the
// transform at the glyph's page space, where its forms are drawn. The caller
// saves and restores the graphics state around it.
func (p *pdfPainter) setInvertedMask(fm string) {
	w := p.r.w
	if inv, ok := invert(w.cur.ctm); ok {
		w.concat(inv)
	}
	w.stateGS(fmt.Sprintf("mask:%s:true", fm), gsEntry{mask: fm, maskInvert: true})
	w.concat(Matrix{A: 1, D: 1, E: p.origin[0], F: p.origin[1]})
}

// formMatrix maps the current user space into the glyph's page space.
func (p *pdfPainter) formMatrix() Matrix {
	m := p.r.w.cur.ctm
	m.E -= p.origin[0]
	m.F -= p.origin[1]
	return m
}

// toForm moves from the current user space to the glyph's page space, for
// drawing a pageForm. The caller saves and restores the graphics state
// around it.
func (p *pdfPainter) toForm() {
	if inv, ok := invert(p.formMatrix()); ok {
		p.r.w.concat(inv)
	}
}

func appendMatrix(b []byte, m Matrix) []byte {
	for i, v := range []float64{m.A, m.B, m.C, m.D, m.E, m.F} {
		if i > 0 {
			b = append(b, ' ')
		}
		b = appendNum(b, v)
	}
	return b
}

// bbox is the glyph's bounds in the current user space, for a form's BBox.
func (p *pdfPainter) bbox() [4]float64 {
	inv, ok := invert(p.top())
	b := p.box
	if !ok || b == (shape.Rect{}) {
		return [4]float64{-big, -big, big, big}
	}
	x0, y0 := math.Inf(1), math.Inf(1)
	x1, y1 := math.Inf(-1), math.Inf(-1)
	for _, c := range [][2]float64{{b.XMin, b.YMin}, {b.XMax, b.YMin}, {b.XMin, b.YMax}, {b.XMax, b.YMax}} {
		x, y := inv.Apply(c[0], c[1])
		x0, y0 = math.Min(x0, x), math.Min(y0, y)
		x1, y1 = math.Max(x1, x), math.Max(y1, y)
	}
	return [4]float64{x0, y0, x1, y1}
}

// invert is m's inverse, and whether it has one.
func invert(m Matrix) (Matrix, bool) {
	d := m.A*m.D - m.B*m.C
	if d == 0 || math.IsNaN(d) || math.IsInf(d, 0) {
		return Identity(), false
	}
	return Matrix{
		A: m.D / d, B: -m.B / d, C: -m.C / d, D: m.A / d,
		E: (m.C*m.F - m.D*m.E) / d, F: (m.B*m.E - m.A*m.F) / d,
	}, true
}

// compositeOpen composites the source onto a backdrop with k clips or
// transforms opened in it since its group began, which a Porter-Duff
// operator rewrites: inside the clips, the operator's result; outside them,
// the backdrop as it was, as a clip bounds an operator. The backdrop, closed,
// the source, the result and the clips' region (the opened clips replayed
// over the glyph's box) are forms; the backdrop is drawn masked by the
// inverse of the region and the result masked by it, from the state the
// backdrop's group began in, and the clips are opened again for the pops
// that close them.
func (p *pdfPainter) compositeOpen(mode shape.CompositeMode, src []byte, k int) {
	w := p.r.w
	base := w.stack[len(w.stack)-k]
	kb := base.ctm
	kb.E -= p.origin[0]
	kb.F -= p.origin[1]
	kc := p.formMatrix()
	var prefix []byte
	for _, b := range p.openBytes[len(p.openBytes)-k:] {
		prefix = append(prefix, b...)
	}
	closing := []byte(strings.Repeat("Q\n", k))
	// The glyph's box in the current space, the glyph's page space, and the
	// backdrop's group's space.
	bc := p.bbox()
	bp := mapBox(kc, bc)
	bb := bp
	if inv, ok := invert(kb); ok {
		bb = mapBox(inv, bp)
	}
	fd := p.formAt(append(append([]byte(nil), w.buf...), closing...), kb, bb)
	fs := p.formAt(src, kc, bc)
	region := append([]byte(nil), prefix...)
	region = fmt.Appendf(region, "0 g\n")
	region = appendRect(region, bc)
	region = append(region, "f\n"...)
	region = append(region, closing...)
	fc := p.formAt(region, kb, bb)
	fci := p.invertedMaskForm(region, kb)

	// The operator's result, in the glyph's page space.
	cur, stack := w.cur, w.stack
	w.buf, w.stack = nil, nil
	w.cur = defaultStreamState()
	draw := func(f string) {
		w.save()
		w.drawXObject(f)
		w.restore()
	}
	masked := func(f, mask string, invert bool) {
		w.save()
		w.stateGS(fmt.Sprintf("mask:%s:%v", mask, invert), gsEntry{mask: mask, maskInvert: invert})
		w.drawXObject(f)
		w.restore()
	}
	switch mode {
	case shape.CompositeSrc:
		draw(fs)
	case shape.CompositeDest:
		draw(fd)
	case shape.CompositeDestOver:
		draw(fs)
		draw(fd)
	case shape.CompositeSrcIn:
		masked(fs, fd, false)
	case shape.CompositeDestIn:
		masked(fd, fs, false)
	case shape.CompositeSrcOut:
		masked(fs, fd, true)
	case shape.CompositeDestOut:
		masked(fd, fs, true)
	case shape.CompositeSrcAtop:
		draw(fd)
		masked(fs, fd, false)
	case shape.CompositeDestAtop:
		draw(fs)
		masked(fd, fs, false)
	}
	result := w.buf

	// The backdrop outside the clips, the result inside them, from the
	// backdrop's group's state, and the clips opened again.
	w.buf, w.stack, w.cur = nil, nil, base
	toPage := func() {
		if inv, ok := invert(kb); ok {
			w.concat(inv)
		}
	}
	w.save()
	p.setInvertedMask(fci)
	w.drawXObject(fd)
	w.restore()
	if len(result) > 0 {
		fr := p.formAt(result, Identity(), bp)
		w.save()
		toPage()
		w.stateGS(fmt.Sprintf("mask:%s:false", fc), gsEntry{mask: fc})
		w.drawXObject(fr)
		w.restore()
	}
	w.buf = append(w.buf, prefix...)
	w.cur, w.stack = cur, stack
}

// mapBox is the box around box under m.
func mapBox(m Matrix, box [4]float64) [4]float64 {
	x0, y0 := math.Inf(1), math.Inf(1)
	x1, y1 := math.Inf(-1), math.Inf(-1)
	for _, pt := range [][2]float64{{box[0], box[1]}, {box[2], box[1]}, {box[0], box[3]}, {box[2], box[3]}} {
		x, y := m.Apply(pt[0], pt[1])
		x0, y0 = math.Min(x0, x), math.Min(y0, y)
		x1, y1 = math.Max(x1, x), math.Max(y1, y)
	}
	return [4]float64{x0, y0, x1, y1}
}

// appendRect appends a re of box.
func appendRect(b []byte, box [4]float64) []byte {
	for _, v := range []float64{box[0], box[1], box[2] - box[0], box[3] - box[1]} {
		b = appendNum(b, v)
		b = append(b, ' ')
	}
	return append(b, "re\n"...)
}
