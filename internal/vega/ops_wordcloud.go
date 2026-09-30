package vega

import (
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/transforms/wordcloud"
)

func init() {
	transformFactories["wordcloud"] = tupleTransform(func(n *opNode, p *opParams, in []jsval.Value) ([]jsval.Value, error) {
		v := n.g.view
		if v.wcText == nil {
			fail("the wordcloud transform needs a canvas and is not supported")
		}
		wp := wordcloud.Params{
			Text:     wordcloud.Accessor(p.field("text").Apply),
			Spiral:   wordcloud.Spiral(p.str("spiral")),
			Random:   v.randSource(),
			Renderer: v.wcText,
		}
		if sz := p.pair2("size"); sz != nil {
			wp.Size = sz
		}
		if as := p.strs("as"); len(as) == 7 {
			wp.As = as
		}
		// Upstream ORs every parameter with its default, so a constant that
		// is falsy (0, "", false) reads as the default; accessors are kept.
		// The second result reports an accessor (upstream: isFunction).
		acc := func(name string, def jsval.Value) (wordcloud.Accessor, bool) {
			switch x := p.Get(name).(type) {
			case jsval.Value:
				if !x.IsTruthy() {
					return wordcloud.Const(def), false
				}
				return wordcloud.Const(x), false
			case nil:
				return wordcloud.Const(def), false
			default:
				f := asField(x)
				if f.IsNil() {
					return wordcloud.Const(def), false
				}
				return wordcloud.Accessor(f.Get), true
			}
		}
		wp.Font, _ = acc("font", jsval.Str("sans-serif"))
		wp.FontStyle, _ = acc("fontStyle", jsval.Str("normal"))
		wp.FontWeight, _ = acc("fontWeight", jsval.Str("normal"))
		wp.Rotate, _ = acc("rotate", jsval.Num(0))
		wp.Padding, _ = acc("padding", jsval.Num(1))
		wp.FontSize, wp.FontSizeIsField = acc("fontSize", jsval.Num(14))
		switch x := p.Get("fontSizeRange").(type) {
		case nil:
			wp.FontSizeRange = wordcloud.DefaultFontSizeRange
		case jsval.Value:
			if !x.IsNullish() {
				wp.FontSizeRange = p.nums("fontSizeRange")
			}
		default:
			wp.FontSizeRange = p.nums("fontSizeRange")
		}
		return in, wordcloud.Transform(n.g.ctx, in, wp)
	})
}
