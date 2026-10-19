package vega

import (
	"errors"
	"strings"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
	"github.com/mgilbir/aster/internal/scene"
)

// setGuideCaptions computes the aria-label of axis and legend marks. Upstream
// builds it lazily while rendering, from the guide's scale and the item's
// format properties (vega-scenegraph util/aria.js); here it is done once the
// dataflow has settled, since it also reads the text of the guide's title.
//
// A caption that fails for a limit or for the end of the call fails the
// render; anything else fails as upstream's does, leaving the mark without
// a caption.
func (v *runView) setGuideCaptions() error {
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
		var caption string
		ok := true
		var err error
		switch m.Role {
		case "axis":
			caption, ok, err = v.axisCaption(ctx, m.Items[0])
		case "legend":
			caption, ok, err = v.legendCaption(ctx, m.Items[0])
		}
		if err != nil {
			return err
		}
		if !ok {
			// ariaGuide catches what the caption throws and answers null:
			// the mark gets no aria attributes at all.
			m.NoGuideCaption = true
			continue
		}
		m.GuideCaption = caption
	}
	return nil
}

// domainCaption is scale.DomainCaption for a guide's caption: ok is false
// where upstream's throws, and err is set only for a limit or the end of the
// call, which fail the render.
func (v *runView) domainCaption(s scale.Scale, item *scene.Item) (dc string, ok bool, err error) {
	dc, err = scale.DomainCaption(v.ctx, v.locale, s, v.captionOptions(item))
	switch {
	case err == nil:
		return dc, true, nil
	case errors.Is(err, budget.ErrLimit) || v.ctx.Err() != nil:
		return "", false, err
	}
	return "", false, nil
}

func extractTitle(item *scene.Item) string {
	if len(item.Items) == 0 {
		return ""
	}
	last := item.Items[len(item.Items)-1]
	if last == nil || len(last.Items) == 0 || last.Items[0] == nil {
		return ""
	}
	return joinText(last.Items[0].Walker(), last.Items[0].Text())
}

// joinText is `array(text).join(' ')`, the walk of an array with w.
func joinText(w jsval.Walker, t jsval.Value) string {
	if t.IsArr() {
		return w.Join(t.Items(), " ")
	}
	if t.IsNullish() {
		return ""
	}
	return w.String(t)
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

// axisCaption is axisCaption of util/aria.js; ok is false where upstream's
// throws (no such scale, a domain it cannot caption).
func (v *runView) axisCaption(c *rtContext, item *scene.Item) (caption string, ok bool, err error) {
	datum := item.Datum
	name := v.walk.String(datum.Get("scale"))
	n := c.scaleNode(name)
	if n == nil {
		return "", false, nil
	}
	s, _ := n.value.(scale.Scale)
	if s == nil {
		return "", false, nil
	}
	title := ""
	hasTitle := false
	if datum.Get("title").IsTruthy() {
		title = extractTitle(item)
		hasTitle = title != ""
	}
	xy := "X"
	if item.Orient() == "left" || item.Orient() == "right" {
		xy = "Y"
	}
	typ := s.Type()
	if scale.IsDiscrete(typ) {
		typ = "discrete"
	}
	dc, ok, err := v.domainCaption(s, item)
	if !ok {
		return "", false, err
	}
	out := xy + "-axis"
	if hasTitle {
		out += " titled '" + title + "'"
	}
	return out + " for a " + typ + " scale with " + dc, true, nil
}

// legendCaption is legendCaption of util/aria.js; ok is false where upstream's
// throws.
func (v *runView) legendCaption(c *rtContext, item *scene.Item) (caption string, ok bool, err error) {
	datum := item.Datum
	scales := datum.Get("scales").ObjValue()
	if scales == nil || scales.Len() == 0 {
		return "", false, nil
	}
	props := append([]string(nil), scales.Keys()...)
	n := c.scaleNode(v.walk.String(scales.ValueAt(0)))
	if n == nil {
		return "", false, nil
	}
	s, _ := n.value.(scale.Scale)
	if s == nil {
		return "", false, nil
	}
	title := ""
	if datum.Get("title").IsTruthy() {
		title = extractTitle(item)
	}
	// capitalize(`${datum.type || ''} legend`.trim())
	t := ""
	if dt := datum.Get("type"); dt.IsTruthy() {
		t = v.walk.String(dt)
	}
	typ := jsval.UpperFirst(jsval.JSTrim(t + " legend"))
	dc, ok, err := v.domainCaption(s, item)
	if !ok {
		return "", false, err
	}
	out := typ
	if title != "" {
		out += " titled '" + title + "'"
	}
	return out + " for " + channelCaption(props) + " with " + dc, true, nil
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
