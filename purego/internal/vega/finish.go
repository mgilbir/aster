package vega

import (
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/scale"
	"github.com/mgilbir/aster/purego/internal/scene"
)

// setGuideCaptions computes the aria-label of axis and legend marks. Upstream
// builds it lazily while rendering, from the guide's scale and the item's
// format properties (vega-scenegraph util/aria.js); here it is done once the
// dataflow has settled, since it also reads the text of the guide's title.
func (v *runView) setGuideCaptions() {
	for _, m := range v.marks {
		if m == nil || len(m.Items) == 0 || m.Items[0] == nil || m.Description != "" {
			continue
		}
		ctx := v.markCtx[m]
		if ctx == nil {
			continue
		}
		if (m.Role == "axis" || m.Role == "legend") && len(m.Items[0].Items) == 0 && m.Items[0].Description == "" {
			// A guide with nothing to draw (no domain, ticks, labels, grid or
			// title) has no child marks to bind its context; see
			// scene.Mark.NoGuideCaption.
			m.NoGuideCaption = true
			continue
		}
		switch m.Role {
		case "axis":
			m.GuideCaption = v.axisCaption(ctx, m.Items[0])
		case "legend":
			m.GuideCaption = v.legendCaption(ctx, m.Items[0])
		}
	}
}

func extractTitle(item *scene.Item) string {
	if len(item.Items) == 0 {
		return ""
	}
	last := item.Items[len(item.Items)-1]
	if last == nil || len(last.Items) == 0 || last.Items[0] == nil {
		return ""
	}
	return joinText(last.Items[0].Text)
}

func joinText(t jsval.Value) string {
	if t.IsArr() {
		parts := make([]string, 0, t.Len())
		for _, e := range t.Items() {
			if e.IsNullish() {
				parts = append(parts, "")
			} else {
				parts = append(parts, e.AsString())
			}
		}
		return strings.Join(parts, " ")
	}
	if t.IsNullish() {
		return ""
	}
	return t.AsString()
}

func (v *runView) captionOptions(item *scene.Item) scale.CaptionOptions {
	o := scale.CaptionOptions{}
	if item.Extra != nil {
		o.Format = item.Extra.Lookup("format")
		if ft := item.Extra.Lookup("formatType"); ft.IsStr() {
			o.FormatType = ft.StrValue()
		}
	}
	return o
}

func (v *runView) axisCaption(c *rtContext, item *scene.Item) string {
	datum := item.Datum
	name := datum.Get("scale").AsString()
	n := c.scaleNode(name)
	if n == nil {
		return ""
	}
	s, _ := n.value.(scale.Scale)
	if s == nil {
		return ""
	}
	title := ""
	hasTitle := false
	if datum.Get("title").IsTruthy() {
		title = extractTitle(item)
		hasTitle = title != ""
	}
	xy := "X"
	if item.Orient == "left" || item.Orient == "right" {
		xy = "Y"
	}
	typ := s.Type()
	if scale.IsDiscrete(typ) {
		typ = "discrete"
	}
	dc, err := scale.DomainCaption(v.locale, s, v.captionOptions(item))
	if err != nil {
		return ""
	}
	out := xy + "-axis"
	if hasTitle {
		out += " titled '" + title + "'"
	}
	return out + " for a " + typ + " scale with " + dc
}

func (v *runView) legendCaption(c *rtContext, item *scene.Item) string {
	datum := item.Datum
	scales := datum.Get("scales").ObjValue()
	if scales == nil || scales.Len() == 0 {
		return ""
	}
	props := append([]string(nil), scales.Keys()...)
	n := c.scaleNode(scales.ValueAt(0).AsString())
	if n == nil {
		return ""
	}
	s, _ := n.value.(scale.Scale)
	if s == nil {
		return ""
	}
	title := ""
	if datum.Get("title").IsTruthy() {
		title = extractTitle(item)
	}
	typ := strings.TrimSpace(datum.Get("type").AsString() + " legend")
	if typ != "" {
		typ = strings.ToUpper(typ[:1]) + typ[1:]
	}
	dc, err := scale.DomainCaption(v.locale, s, v.captionOptions(item))
	if err != nil {
		return ""
	}
	out := typ
	if title != "" {
		out += " titled '" + title + "'"
	}
	return out + " for " + channelCaption(props) + " with " + dc
}

func channelCaption(props []string) string {
	ps := make([]string, len(props))
	for i, p := range props {
		ps[i] = p
		if p == "fill" || p == "stroke" {
			ps[i] += " color"
		}
	}
	if len(ps) < 2 {
		if len(ps) == 1 {
			return ps[0]
		}
		return ""
	}
	return strings.Join(ps[:len(ps)-1], ", ") + " and " + ps[len(ps)-1]
}
