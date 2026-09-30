// Package svg renders a scenegraph to SVG text, byte-for-byte compatible with
// vega-scenegraph's SVGStringRenderer: same element and attribute order, same
// number formatting, same defs and id schemes.
package svg

import (
	"context"
	"errors"

	"github.com/mgilbir/aster/purego/internal/scene"
)

// ImageInfo describes a resolved image: the value of its href attribute and its
// natural size (0, 0 if unknown).
type ImageInfo struct {
	Src           string
	Width, Height float64
}

// Options configure a rendering.
type Options struct {
	// Width and Height are the size of the display in coordinate units,
	// including padding; Origin is the translation of the drawing (the
	// padding plus the view origin). Scale multiplies the width/height
	// attributes (the viewBox stays in coordinate units); 0 means 1.
	Width, Height float64
	Origin        [2]float64
	Scale         float64
	// Background paints a full-size rect unless empty, "transparent" or "none".
	Background string

	// Measurer measures text for truncation to `limit`; nil uses vega's
	// width estimate.
	Measurer scene.TextMeasurer

	// Href returns the attributes of the <a> element for a hyperlinked item;
	// ok=false renders no link. Nil uses DefaultHref(URLOptions{}).
	Href func(uri string) (attrs []HrefAttr, ok bool)
	// Image resolves an image URL. Nil reproduces vega running without a
	// canvas: the sanitized URL as source and an unknown (0x0) size.
	Image func(url string) ImageInfo
}

var errNilScene = errors.New("svg: nil scenegraph")

// Render produces the SVG document for sg. It checks ctx periodically and
// returns its error when cancelled.
func Render(ctx context.Context, sg *scene.Scenegraph, opt Options) (string, error) {
	b, err := AppendSVG(ctx, nil, sg, opt)
	return string(b), err
}

// AppendSVG is Render appending to dst.
func AppendSVG(ctx context.Context, dst []byte, sg *scene.Scenegraph, opt Options) ([]byte, error) {
	if sg == nil || sg.Root == nil {
		return dst, errNilScene
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r := &renderer{
		ctx:     ctx,
		opt:     opt,
		metrics: scene.Metrics{Measurer: opt.Measurer},
	}
	r.w.buf = dst
	if r.opt.Href == nil {
		r.opt.Href = DefaultHref(URLOptions{})
	}
	if err := r.root(sg.Root); err != nil {
		return dst, err
	}
	return r.w.buf, nil
}

type renderer struct {
	ctx     context.Context
	opt     Options
	metrics scene.Metrics
	w       writer
	sp      scene.StringPath
	count   int

	// Definitions collected while rendering; gradient and clip ids are
	// assigned on first use, in document order, like vega's global counters
	// (which the caller resets between documents).
	gradients   []*gradDef
	gradientIDs map[*scene.Gradient]string
	gradByID    map[string]int
	nextGrad    int
	clips       []*clipDef
	clipIDs     map[any]string
	clipByID    map[string]int
	nextClip    int
}

func (r *renderer) root(scn *scene.Mark) error {
	w := &r.w
	scale := r.opt.Scale
	if scale == 0 {
		scale = 1
	}
	w.start("svg")
	w.attr("xmlns", "http://www.w3.org/2000/svg")
	w.attr("xmlns:xlink", "http://www.w3.org/1999/xlink")
	w.attr("version", "1.1")
	w.attr("class", "marks")
	w.attrNum("width", r.opt.Width*scale)
	w.attrNum("height", r.opt.Height*scale)
	w.attrName("viewBox")
	w.buf = append(w.buf, "0 0 "...)
	w.buf = scene.AppendNumber(w.buf, r.opt.Width)
	w.buf = append(w.buf, ' ')
	w.buf = scene.AppendNumber(w.buf, r.opt.Height)
	w.buf = append(w.buf, '"')

	if bg := r.opt.Background; bg != "" && bg != "transparent" && bg != "none" {
		w.start("rect")
		w.attrNum("width", r.opt.Width)
		w.attrNum("height", r.opt.Height)
		w.attr("fill", bg)
		w.end()
	}

	w.start("g")
	w.attr("fill", "none")
	w.attr("stroke-miterlimit", "4")
	w.attrName("transform")
	w.buf = append(w.buf, "translate("...)
	w.buf = scene.AppendNumber(w.buf, r.opt.Origin[0])
	w.buf = append(w.buf, ',')
	w.buf = scene.AppendNumber(w.buf, r.opt.Origin[1])
	w.buf = append(w.buf, ")\""...)
	if err := r.mark(scn, 0); err != nil {
		return err
	}
	w.end() // </g>

	r.defs()
	w.end() // </svg>
	return nil
}

// tick reports cancellation every so many rendered items.
func (r *renderer) tick() error {
	r.count++
	if r.count&1023 == 0 {
		return r.ctx.Err()
	}
	return nil
}

// tagOf is the SVG element a mark type renders to.
func tagOf(t scene.MarkType) string {
	switch t {
	case scene.MarkGroup:
		return "g"
	case scene.MarkImage:
		return "image"
	case scene.MarkRule:
		return "line"
	case scene.MarkText:
		return "text"
	}
	return "path"
}

// orderedMarks is scene.Mark.Ordered for the child marks of a group: marks
// without a z-index first, then the others sorted by z-index.
func orderedMarks(ms []*scene.Mark) []*scene.Mark {
	anyZ := false
	for _, m := range ms {
		if m.Zindex != 0 {
			anyZ = true
			break
		}
	}
	if !anyZ {
		return ms
	}
	out := make([]*scene.Mark, 0, len(ms))
	var z []*scene.Mark
	for _, m := range ms {
		if m.Zindex != 0 {
			z = append(z, m)
		} else {
			out = append(out, m)
		}
	}
	// Stable insertion sort by z-index keeps document order for ties.
	for i := 1; i < len(z); i++ {
		for j := i; j > 0 && z[j-1].Zindex > z[j].Zindex; j-- {
			z[j-1], z[j] = z[j], z[j-1]
		}
	}
	return append(out, z...)
}

func cssClass(w *writer, m *scene.Mark) {
	w.attrName("class")
	w.buf = append(w.buf, "mark-"...)
	w.buf = appendEscaped(w.buf, m.Type.String(), true)
	if m.Role != "" {
		w.buf = append(w.buf, " role-"...)
		w.buf = appendEscaped(w.buf, m.Role, true)
	}
	if m.Name != "" {
		w.buf = append(w.buf, ' ')
		w.buf = appendEscaped(w.buf, m.Name, true)
	}
	w.buf = append(w.buf, '"')
}

// mark renders one mark: a <g> container with its items.
func (r *renderer) mark(m *scene.Mark, depth int) error {
	if depth > scene.MaxDepth {
		return scene.ErrTooDeep
	}
	w := &r.w
	tag := tagOf(m.Type)

	w.start("g")
	cssClass(w, m)
	if m.Clip || m.ClipPath != nil {
		ref, err := r.clipRef(m, m.Clip, m.ClipPath, m.Group)
		if err != nil {
			return err
		}
		w.attrRaw("clip-path", ref)
	}
	r.markAria(m)
	if tag != "g" && m.NonInteractive {
		w.attrRaw("pointer-events", "none")
	}

	if m.Type.Nested() {
		if len(m.Items) > 0 {
			if err := r.item(m, m.Items[0], tag, depth); err != nil {
				return err
			}
		}
	} else {
		for _, it := range m.Ordered() {
			if err := r.tick(); err != nil {
				return err
			}
			if err := r.item(m, it, tag, depth); err != nil {
				return err
			}
		}
	}
	w.end() // </g>
	return nil
}

// item renders one item, wrapped in <a> when it has an href.
func (r *renderer) item(m *scene.Mark, it *scene.Item, tag string, depth int) error {
	w := &r.w
	var link bool
	if it.Href != "" {
		if attrs, ok := r.opt.Href(it.Href); ok {
			link = true
			w.start("a")
			for _, a := range attrs {
				w.attr(a.Name, a.Value)
			}
		}
	}

	w.start(tag)
	r.itemAria(m, it)
	var err error
	switch m.Type {
	case scene.MarkGroup:
		err = r.group(m, it, depth)
	case scene.MarkText:
		r.textItem(m, it)
	case scene.MarkImage:
		r.imageItem(m, it)
	case scene.MarkRule:
		w.attrName("transform")
		w.buf = appendTranslate(w.buf, it.X.Zero(), it.Y.Zero())
		w.buf = append(w.buf, '"')
		x2, y2 := 0.0, 0.0
		if it.X2.Set() {
			x2 = it.X2.Val() - it.X.Zero()
		}
		if it.Y2.Set() {
			y2 = it.Y2.Val() - it.Y.Zero()
		}
		w.attrNum("x2", x2)
		w.attrNum("y2", y2)
		r.style(m, it, "line", it.Fill, it.Stroke)
	case scene.MarkRect:
		w.attrBytes("d", scene.RectPathData(&r.sp, it, false, 0, 0))
		r.style(m, it, "path", it.Fill, it.Stroke)
	case scene.MarkPath:
		sx, sy := scaleOne(it.ScaleX), scaleOne(it.ScaleY)
		if sx != 1 || sy != 1 {
			w.attrRaw("vector-effect", "non-scaling-stroke")
		}
		w.attrName("transform")
		w.buf = appendTranslate(w.buf, it.X.Zero(), it.Y.Zero())
		if it.AngleTruthy() {
			w.buf = append(w.buf, " rotate("...)
			w.buf = appendAngle(w.buf, it)
			w.buf = append(w.buf, ')')
		}
		if it.ScaleX.Truthy() || it.ScaleY.Truthy() {
			w.buf = append(w.buf, " scale("...)
			w.buf = scene.AppendNumber(w.buf, sx)
			w.buf = append(w.buf, ',')
			w.buf = scene.AppendNumber(w.buf, sy)
			w.buf = append(w.buf, ')')
		}
		w.buf = append(w.buf, '"')
		if it.Path.Set {
			w.attr("d", it.Path.D)
		}
		r.style(m, it, "path", it.Fill, it.Stroke)
	case scene.MarkArc, scene.MarkSymbol, scene.MarkShape:
		w.attrName("transform")
		w.buf = appendTranslate(w.buf, it.X.Zero(), it.Y.Zero())
		if it.AngleTruthy() {
			w.buf = append(w.buf, " rotate("...)
			w.buf = appendAngle(w.buf, it)
			w.buf = append(w.buf, ')')
		}
		w.buf = append(w.buf, '"')
		d, ok, perr := scene.ItemPathData(&r.sp, m.Type, it)
		if perr != nil {
			return perr
		}
		if ok {
			w.attrBytes("d", d)
		}
		r.style(m, it, "path", it.Fill, it.Stroke)
	case scene.MarkArea, scene.MarkLine, scene.MarkTrail:
		d, ok, perr := scene.MultiPathData(&r.sp, m.Type, m.Items)
		if perr != nil {
			return perr
		}
		if ok {
			w.attrBytes("d", d)
		}
		r.style(m, it, "path", it.Fill, it.Stroke)
	}
	if err != nil {
		return err
	}
	w.end() // </tag>
	if link {
		w.end() // </a>
	}
	return nil
}

func scaleOne(n scene.Num) float64 {
	if v := n.Zero(); v != 0 {
		return v
	}
	return 1
}

func appendTranslate(dst []byte, x, y float64) []byte {
	dst = append(dst, "translate("...)
	dst = scene.AppendNumber(dst, x)
	dst = append(dst, ',')
	dst = scene.AppendNumber(dst, y)
	return append(dst, ')')
}

// groupStrokeOffset is the half-pixel shift that keeps 1px strokes of group
// boxes crisp: an explicit strokeOffset, else 0.5-|w-1| for stroke widths
// between 0.5 and 1.5. stroke is the stroke in effect: with a foreground stroke
// the background box is computed while upstream has the stroke removed.
func groupStrokeOffset(it *scene.Item, stroke scene.Paint) float64 {
	sw := it.StrokeWidth.Or(1)
	switch {
	case it.StrokeOffset.Set():
		return it.StrokeOffset.Val()
	case stroke.Truthy() && sw > 0.5 && sw < 1.5:
		return 0.5 - abs(sw-1)
	}
	return 0
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// group renders a group item: background, content and foreground.
func (r *renderer) group(m *scene.Mark, it *scene.Item, depth int) error {
	w := &r.w
	w.attrName("transform")
	w.buf = appendTranslate(w.buf, it.X.Zero(), it.Y.Zero())
	w.buf = append(w.buf, '"')

	fore := it.StrokeForeground.IsTrue()
	fill, stroke := it.Fill, it.Stroke
	// With a foreground stroke the background is drawn without it.
	bgStroke := stroke
	if fore && stroke.Truthy() {
		bgStroke = scene.NullPaint()
	}

	// The path data aliases the shared scratch buffer, so it is regenerated
	// wherever it is needed rather than kept across the children.
	off := groupStrokeOffset(it, bgStroke)

	w.start("path")
	w.attrRaw("class", "background")
	w.attrRaw("aria-hidden", "true")
	w.attrBytes("d", scene.RectPathData(&r.sp, it, true, off, off))
	r.style(m, it, "bgrect", fill, bgStroke)
	w.end()

	// content
	w.start("g")
	if it.Clip.IsTrue() || it.ClipPath != nil {
		ref, err := r.clipRef(it, true, it.ClipPath, it)
		if err != nil {
			return err
		}
		w.attrRaw("clip-path", ref)
	}
	for _, child := range orderedMarks(it.Items) {
		if err := r.mark(child, depth+1); err != nil {
			return err
		}
	}
	w.end()

	if fore && stroke.Truthy() {
		fgFill := fill
		if fill.Truthy() {
			fgFill = scene.NullPaint()
		}
		off = groupStrokeOffset(it, stroke)
		w.start("path")
		w.attrRaw("class", "foreground")
		w.attrRaw("aria-hidden", "true")
		w.attrBytes("d", scene.RectPathData(&r.sp, it, true, off, off))
		r.style(m, it, "bgrect", fgFill, stroke)
		w.end()
	} else {
		w.start("path")
		w.attrRaw("class", "foreground")
		w.attrRaw("aria-hidden", "true")
		if fore {
			w.attrBytes("d", scene.RectPathData(&r.sp, it, true, off, off))
		} else {
			w.attrRaw("d", "")
		}
		r.style(m, it, "bgfore", fill, stroke)
		w.end()
	}
	return nil
}
