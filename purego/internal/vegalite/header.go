package vegalite

import (
	"strings"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Facet headers — vega-lite/src/compile/header/*.ts and header.ts.

var (
	headerChannels = []string{"row", "column"}
	headerTypes    = []string{"header", "footer"}
)

var headerTitlePropertiesMap = [][2]string{
	{"titleAlign", "align"}, {"titleAnchor", "anchor"}, {"titleAngle", "angle"}, {"titleBaseline", "baseline"},
	{"titleColor", "color"}, {"titleFont", "font"}, {"titleFontSize", "fontSize"}, {"titleFontStyle", "fontStyle"},
	{"titleFontWeight", "fontWeight"}, {"titleLimit", "limit"}, {"titleLineHeight", "lineHeight"},
	{"titleOrient", "orient"}, {"titlePadding", "offset"},
}

var headerLabelPropertiesMap = [][2]string{
	{"labelAlign", "align"}, {"labelAnchor", "anchor"}, {"labelAngle", "angle"}, {"labelBaseline", "baseline"},
	{"labelColor", "color"}, {"labelFont", "font"}, {"labelFontSize", "fontSize"}, {"labelFontStyle", "fontStyle"},
	{"labelFontWeight", "fontWeight"}, {"labelLimit", "limit"}, {"labelLineHeight", "lineHeight"},
	{"labelOrient", "orient"}, {"labelPadding", "offset"},
}

type headerComponent struct {
	labels     Value
	sizeSignal Value
	axes       []Value
}

type layoutHeaderComponent struct {
	title         Value
	facetFieldDef Value
	header        []*headerComponent
	footer        []*headerComponent
}

func (l *layoutHeaderComponent) get(t string) []*headerComponent {
	if t == "header" {
		return l.header
	}
	return l.footer
}

func getHeaderChannel(channel, orient string) string {
	switch orient {
	case "top", "bottom":
		return "column"
	case "left", "right":
		return "row"
	}
	if channel == "row" {
		return "row"
	}
	return "column"
}

func getHeaderProperty(prop string, header Value, config Value, channel string) Value {
	var specific Value
	switch channel {
	case "row":
		specific = config.Get("headerRow")
	case "column":
		specific = config.Get("headerColumn")
	default:
		specific = config.Get("headerFacet")
	}
	return firstDefined(coalesceObjV(header).Get(prop), specific.Get(prop), config.Get("header").Get(prop))
}

func coalesceObjV(v Value) Value {
	if v.IsTruthy() {
		return v
	}
	return mkv()
}

func getHeaderProperties(props []string, header Value, config Value, channel string) *Object {
	out := jsval.NewObject(4)
	for _, p := range props {
		if v := getHeaderProperty(p, coalesceObjV(header), config, channel); !v.IsUndefined() {
			out.Set(p, v)
		}
	}
	return out
}

func getHeaderType(orient Value) string {
	if isSignalRef(orient) || (orient.IsStr() && (orient.StrValue() == "top" || orient.StrValue() == "left")) {
		return "header"
	}
	return "footer"
}

func parseFacetHeaders(m *facetModel) {
	for _, channel := range facetChannels {
		parseFacetHeader(m, channel)
	}
	mergeChildAxis(m, "x")
	mergeChildAxis(m, "y")
}

func parseFacetHeader(m *facetModel, channel string) {
	if !m.channelHasField(channel) {
		return
	}
	fd := m.facet.Lookup(channel)
	titleConfig := getHeaderProperty("title", jsval.Null, m.config, channel)
	title := fieldTitle(fd, m.config, true, titleConfig.IsUndefined() || titleConfig.IsTruthy())
	childLH := m.child.b().comp.layoutHeaders[channel]
	if childLH.title.IsTruthy() {
		var t string
		if title.IsArr() {
			var parts []string
			for _, x := range title.Items() {
				parts = append(parts, x.AsString())
			}
			t = strings.Join(parts, ", ")
		} else {
			t = title.AsString()
		}
		title = jsval.Str(t + " / " + childLH.title.AsString())
		childLH.title = jsval.Null
	}
	labelOrient := getHeaderProperty("labelOrient", fd.Get("header"), m.config, channel)
	var labels Value
	if !fd.Get("header").IsNull() {
		labels = firstDefined(fd.Get("header").Get("labels"), m.config.Get("header").Get("labels"), jsval.True)
	} else {
		labels = jsval.False
	}
	headerType := "header"
	if labelOrient.IsStr() && (labelOrient.StrValue() == "bottom" || labelOrient.StrValue() == "right") {
		headerType = "footer"
	}
	lh := &layoutHeaderComponent{facetFieldDef: fd}
	if !fd.Get("header").IsNull() {
		lh.title = title
	} else {
		lh.title = jsval.Null
	}
	var list []*headerComponent
	if channel != "facet" {
		list = []*headerComponent{makeHeaderComponent(m, channel, labels)}
	} else {
		list = []*headerComponent{}
	}
	if headerType == "header" {
		lh.header = list
	} else {
		lh.footer = list
	}
	m.comp.layoutHeaders[channel] = lh
}

func makeHeaderComponent(m *facetModel, channel string, labels Value) *headerComponent {
	sizeType := "width"
	if channel == "row" {
		sizeType = "height"
	}
	h := &headerComponent{labels: labels}
	if m.child.b().comp.layoutSize.get(sizeType).IsTruthy() {
		h.sizeSignal = m.child.b().getSizeSignalRef(sizeType)
	}
	return h
}

func mergeChildAxis(m *facetModel, channel string) {
	child := m.child
	childAxes, ok := child.b().comp.axes.get(channel)
	if !ok || childAxes == nil {
		return
	}
	layoutHeaders, resolve := m.comp.layoutHeaders, m.comp.resolve
	resolve.axis[channel] = parseGuideResolve(resolve, channel)
	if resolve.axis[channel] == "shared" {
		headerChannel := "row"
		if channel == "x" {
			headerChannel = "column"
		}
		lh := layoutHeaders[headerChannel]
		for _, ac := range childAxes {
			ht := getHeaderType(ac.get("orient"))
			list := lh.get(ht)
			if list == nil {
				list = []*headerComponent{makeHeaderComponent(m, headerChannel, jsval.False)}
				if ht == "header" {
					lh.header = list
				} else {
					lh.footer = list
				}
			}
			if mainAxis := assembleAxis(ac, "main", m.config, true); mainAxis.IsTruthy() {
				list[0].axes = append(list[0].axes, mainAxis)
			}
			ac.mainExtracted = true
		}
	}
}

// ---- assemble ----

func assembleTitleGroup(m Model, channel string) Value {
	b := m.b()
	title := b.comp.layoutHeaders[channel].title
	config := b.config
	facetFieldDef := b.comp.layoutHeaders[channel].facetFieldDef
	props := getHeaderProperties([]string{"titleAnchor", "titleAngle", "titleOrient"}, facetFieldDef.Get("header"), config, channel)
	titleOrient := props.Lookup("titleOrient")
	headerChannel := getHeaderChannel(channel, titleOrient.AsString())
	titleAngle := normalizeAngle(props.Lookup("titleAngle"))
	t := mk("text", title)
	if channel == "row" {
		t.Set("orient", jsval.Str("left"))
	}
	t.Set("style", jsval.Str("guide-title"))
	spreadV(t, defaultHeaderGuideBaseline(titleAngle, headerChannel))
	spreadV(t, defaultHeaderGuideAlign(headerChannel, titleAngle, props.Lookup("titleAnchor")))
	spread(t, jsval.Obj(assembleHeaderProperties(config, facetFieldDef, channel, headerTitlePropertiesMap)))
	return mkv("name", channel+"-title", "type", "group", "role", headerChannel+"-title", "title", jsval.Obj(t))
}

func defaultHeaderGuideAlign(headerChannel string, angle Value, anchor Value) Value {
	a := "middle"
	if anchor.IsStr() {
		a = anchor.StrValue()
	} else if anchor.IsUndefined() {
		a = "middle"
	}
	switch a {
	case "start":
		return mkv("align", "left")
	case "end":
		return mkv("align", "right")
	}
	orient, ch := "top", chX
	if headerChannel == "row" {
		orient, ch = "left", chY
	}
	if align := defaultLabelAlign(angle, jsval.Str(orient), ch); align.IsTruthy() {
		return mkv("align", align)
	}
	return mkv()
}

func defaultHeaderGuideBaseline(angle Value, channel string) Value {
	orient, ch := "top", chX
	if channel == "row" {
		orient, ch = "left", chY
	}
	if b := defaultLabelBaseline(angle, jsval.Str(orient), ch, true); b.IsTruthy() {
		return mkv("baseline", b)
	}
	return mkv()
}

func assembleHeaderGroups(m Model, channel string) []Value {
	lh := m.b().comp.layoutHeaders[channel]
	var groups []Value
	for _, ht := range headerTypes {
		for _, hc := range lh.get(ht) {
			if g := assembleHeaderGroup(m, channel, ht, lh, hc); g.IsTruthy() {
				groups = append(groups, g)
			}
		}
	}
	return groups
}

func headerGetSort(fd Value, channel string) Value {
	sort := fd.Get("sort")
	switch {
	case isSortField(sort):
		return mkv("field", vgField(sort, fieldRefOption{expr: "datum"}), "order", coalesce(sort.Get("order"), jsval.Str("ascending")))
	case sort.IsArr():
		return mkv("field", sortArrayIndexField(fd, channel, fieldRefOption{expr: "datum"}), "order", "ascending")
	}
	return mkv("field", vgField(fd, fieldRefOption{expr: "datum"}), "order", coalesce(sort, jsval.Str("ascending")))
}

func assembleLabelTitle(fd Value, channel string, config Value) Value {
	props := getHeaderProperties([]string{"format", "formatType", "labelAngle", "labelAnchor", "labelOrient", "labelExpr"}, fd.Get("header"), config, channel)
	titleTextExpr := signalOf(formatSignalRef(formatSignalOpts{
		fieldOrDatumDef: fd, format: props.Lookup("format"), formatType: props.Lookup("formatType"), expr: "parent", config: config,
	}))
	labelOrient := props.Lookup("labelOrient")
	headerChannel := getHeaderChannel(channel, labelOrient.AsString())
	var text string
	if labelExpr := props.Lookup("labelExpr"); labelExpr.IsTruthy() {
		text = strings.ReplaceAll(strings.ReplaceAll(labelExpr.AsString(), "datum.label", titleTextExpr), "datum.value", vgField(fd, fieldRefOption{expr: "parent"}))
	} else {
		text = titleTextExpr
	}
	o := mk("text", mkv("signal", text))
	if channel == "row" {
		o.Set("orient", jsval.Str("left"))
	}
	o.Set("style", jsval.Str("guide-label"))
	o.Set("frame", jsval.Str("group"))
	labelAngle := props.Lookup("labelAngle")
	spreadV(o, defaultHeaderGuideBaseline(labelAngle, headerChannel))
	spreadV(o, defaultHeaderGuideAlign(headerChannel, labelAngle, props.Lookup("labelAnchor")))
	spread(o, jsval.Obj(assembleHeaderProperties(config, fd, channel, headerLabelPropertiesMap)))
	return jsval.Obj(o)
}

func assembleHeaderGroup(m Model, channel, headerType string, lh *layoutHeaderComponent, hc *headerComponent) Value {
	if hc == nil {
		return jsval.Null
	}
	b := m.b()
	title := undef
	fd := lh.facetFieldDef
	config := b.config
	if fd.IsTruthy() && hc.labels.IsTruthy() {
		labelOrient := getHeaderProperty("labelOrient", fd.Get("header"), config, channel)
		lo := labelOrient.AsString()
		if (channel == "row" && lo != "top" && lo != "bottom") || (channel == "column" && lo != "left" && lo != "right") {
			title = assembleLabelTitle(fd, channel, config)
		}
	}
	f, _ := m.(*facetModel)
	isFacetWithoutRowCol := f != nil && !isFacetMapping(jsval.Obj(f.facet))
	hasAxes := len(hc.axes) > 0
	if title.IsTruthy() || hasAxes {
		sizeChannel := "width"
		if channel == "row" {
			sizeChannel = "height"
		}
		o := mk("name", b.getName(channel+"_"+headerType), "type", "group", "role", channel+"-"+headerType)
		if lh.facetFieldDef.IsTruthy() {
			o.Set("from", mkv("data", b.getName(channel+"_domain")))
			o.Set("sort", headerGetSort(fd, channel))
		}
		if hasAxes && isFacetWithoutRowCol {
			o.Set("from", mkv("data", b.getName("facet_domain_"+channel)))
		}
		if title.IsTruthy() {
			o.Set("title", title)
		}
		if hc.sizeSignal.IsTruthy() {
			o.Set("encode", mkv("update", mkv(sizeChannel, hc.sizeSignal)))
		}
		if hasAxes {
			o.Set("axes", jsval.Arr(hc.axes))
		}
		return jsval.Obj(o)
	}
	return jsval.Null
}

func getLayoutTitleBand(titleAnchor Value, headerChannel string) Value {
	a := titleAnchor.AsString()
	if !titleAnchor.IsStr() {
		return undef
	}
	switch headerChannel {
	case "column":
		if a == "start" {
			return jsval.Int(0)
		} else if a == "end" {
			return jsval.Int(1)
		}
	case "row":
		if a == "start" {
			return jsval.Int(1)
		} else if a == "end" {
			return jsval.Int(0)
		}
	}
	return undef
}

func assembleLayoutTitleBand(headers map[string]*layoutHeaderComponent, config Value) Value {
	titleBand := jsval.NewObject(2)
	for _, channel := range facetChannels {
		hc := headers[channel]
		if hc != nil && hc.facetFieldDef.IsTruthy() {
			props := getHeaderProperties([]string{"titleAnchor", "titleOrient"}, hc.facetFieldDef.Get("header"), config, channel)
			headerChannel := getHeaderChannel(channel, props.Lookup("titleOrient").AsString())
			if band := getLayoutTitleBand(props.Lookup("titleAnchor"), headerChannel); !band.IsUndefined() {
				titleBand.Set(headerChannel, band)
			}
		}
	}
	if titleBand.Len() == 0 {
		return undef
	}
	return jsval.Obj(titleBand)
}

func assembleHeaderProperties(config Value, fd Value, channel string, propsMap [][2]string) *Object {
	props := jsval.NewObject(4)
	for _, kv := range propsMap {
		if v := getHeaderProperty(kv[0], fd.Get("header"), config, channel); !v.IsUndefined() {
			props.Set(kv[1], v)
		}
	}
	return props
}
