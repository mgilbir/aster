package vega

import (
	"math"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
	"github.com/mgilbir/aster/internal/scene"
	"github.com/mgilbir/aster/internal/vega/guides"
	"github.com/mgilbir/aster/internal/vega/layout"
)

func init() {
	transformFactories["viewlayout"] = facViewLayout
	transformFactories["overlap"] = facOverlap
	transformFactories["axisticks"] = facAxisTicks
	transformFactories["legendentries"] = facLegendEntries
}

func (v *runView) guidesEnv() *guides.Env {
	return &guides.Env{Locale: v.locale, Warn: v.g.warn}
}

func facViewLayout(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		v := n.g.view
		mark, _ := p.Get("mark").(*scene.Mark)
		if mark == nil {
			return nil
		}
		lp := layout.Params{Legends: p.Value("legends")}
		if lay := p.Value("layout"); lay.IsObj() {
			lp.Grid = layout.ParseGridSpec(lay)
		}
		if p.Has("autosize") {
			a := layout.ParseAutosize(p.Value("autosize"))
			lp.Autosize = &a
		}
		l, r, t, b := v.padding()
		lv := &layout.View{
			Width: v.width, Height: v.height,
			Padding:        layout.Padding{Left: l, Right: r, Top: t, Bottom: b},
			AutosizeActive: v.autosize >= 1,
			Warn:           n.g.warn,
		}
		for _, s := range layout.ViewLayout(mark, lv, lp) {
			v.resizeView(s.ViewWidth, s.ViewHeight, s.Width, s.Height, s.Origin, s.Resize)
		}
		// Layout may resize child items, so group bounds are recomputed
		// (legend entries excepted, which upstream exempts to avoid
		// instability).
		if g := mark.Group; g != nil && g.Mark != nil && g.Mark.Role != "legend-entry" {
			return reflowPulse(pulse)
		}
		return nil
	}), nil
}

// reflowPulse is Pulse.reflow(): every source item counts as modified.
func reflowPulse(p *flowPulse) *flowPulse {
	out := *p
	if len(p.items) != len(p.add) {
		skip := make(map[*scene.Item]struct{}, len(p.add))
		for _, it := range p.add {
			skip[it] = struct{}{}
		}
		mod := make([]*scene.Item, 0, len(p.items))
		for _, it := range p.items {
			if _, ok := skip[it]; !ok {
				mod = append(mod, it)
			}
		}
		out.mod = mod
	}
	out.changed = out.changed || len(out.mod) > 0
	return &out
}

func facOverlap(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return nil, trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		mark := markOfPulse(pulse)
		if mark == nil {
			return nil
		}
		op := layout.OverlapParams{
			Method:     p.Value("method"),
			Separation: jsval.ToNumber(p.Value("separation")),
		}
		if o := p.Value("order"); o.IsStr() {
			op.Order = o.StrValue()
		}
		if cmp := p.comparator("sort"); cmp != nil {
			// The parser passes the overlap `order` as a comparator over the
			// items (vega's `compare({field: "datum.index"})`).
			v := n.g.view
			op.Sort = func(a, b *scene.Item) int { return cmp(v.itemTuple(a), v.itemTuple(b)) }
		}
		if sc, ok := p.Get("boundScale").(scale.Scale); ok && sc != nil {
			rng := sc.Range()
			if len(rng) > 0 {
				op.Bound = &layout.OverlapBound{
					Range:     [2]float64{jsval.ToNumber(rng[0]), jsval.ToNumber(rng[len(rng)-1])},
					Orient:    p.Value("boundOrient").AsString(),
					Tolerance: jsval.ToNumber(p.Value("boundTolerance")),
				}
			}
		}
		layout.Overlap(mark, op)
		return nil
	}), nil
}

// markOfPulse recovers the mark whose items a pulse carries.
func markOfPulse(p *flowPulse) *scene.Mark {
	for _, it := range p.items {
		if it != nil && it.Mark != nil {
			return it.Mark
		}
	}
	return nil
}

func facAxisTicks(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return []jsval.Value(nil), trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		if n.value != nil && !p.Modified() {
			return stopPulse
		}
		sc, _ := p.Get("scale").(scale.Scale)
		in := guides.AxisTicksInput{
			Scale:           sc,
			Extra:           p.Value("extra").IsTruthy(),
			Count:           p.Value("count"),
			Values:          p.Value("values"),
			MinStep:         p.Value("minstep"),
			FormatType:      strParam(p.Value("formatType")),
			FormatSpecifier: p.Value("formatSpecifier"),
		}
		ticks, err := n.g.view.guidesEnv().AxisTicks(in)
		if err != nil {
			failErr(err)
		}
		n.value = ticks
		return changedPulse(pulse, ticks)
	}), nil
}

func strParam(v jsval.Value) string {
	if v.IsNullish() {
		return ""
	}
	return v.AsString()
}

func facLegendEntries(c *rtContext, n *opNode, e *entry) (any, transform, func(*opNode, *opParams) any) {
	return []jsval.Value(nil), trFunc(func(n *opNode, p *opParams, pulse *flowPulse) *flowPulse {
		if n.value != nil && !p.Modified() {
			return stopPulse
		}
		sc, _ := p.Get("scale").(scale.Scale)
		in := guides.LegendEntriesInput{
			Type:            strParam(p.Value("type")),
			Scale:           sc,
			Count:           p.Value("count"),
			Limit:           p.Value("limit"),
			Values:          p.Value("values"),
			MinStep:         p.Value("minstep"),
			FormatType:      strParam(p.Value("formatType")),
			FormatSpecifier: p.Value("formatSpecifier"),
		}
		if b, ok := p.Get("size").(*boundExpr); ok {
			in.Size = b.call
		} else {
			in.SizeConst = p.Value("size")
		}
		entries, err := n.g.view.guidesEnv().LegendEntries(in)
		if err != nil {
			failErr(err)
		}
		n.value = entries
		return changedPulse(pulse, entries)
	}), nil
}

var _ = math.NaN
