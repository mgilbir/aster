package vegalite

import (
	"github.com/mgilbir/aster/internal/jsval"
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

// getHeaderChannel compares the orient with contains (strictly): an orient
// that is not one of the four names leaves the channel's own.
func getHeaderChannel(channel string, orientV Value) string {
	switch orient, _ := orientV.Strict(); orient {
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
	return firstDefined(coalesceObjV(header).Get(prop), jsGet(specific, prop), jsGet(config.Get("header"), prop))
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

func getHeaderType(cc *compileCtx, orient Value) string {
	if isSignalRef(cc, orient) || (orient.IsStr() && (orient.StrValue() == "top" || orient.StrValue() == "left")) {
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
	cc := m.b().ctx

	if !m.channelHasField(channel) {
		return
	}
	fd := m.facet.Lookup(channel)
	titleConfig := getHeaderProperty("title", jsval.Null, m.config, channel)
	title := fieldTitle(cc, fd, m.config, true, titleConfig.IsUndefined() || titleConfig.IsTruthy())
	childLH := m.child.b().comp.layoutHeaders[channel]
	if childLH.title.IsTruthy() {
		// isArray(title) ? title.join(', ') : title, then `${title} /
		// ${child.title}`: Array.prototype.join writes null and undefined
		// elements as "" (C86 of the 2026-10-18 audit).
		w := cc.walker()
		t := ""
		if title.IsArr() {
			cc.work(int64(title.Len()))
			t = w.Join(title.Items(), ", ")
		} else {
			t = w.String(title)
		}
		title = jsval.Str(t + " / " + w.String(childLH.title))
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
	cc := m.b().ctx

	child := m.child
	childAxes, ok := child.b().comp.axes.get(channel)
	if !ok || childAxes == nil {
		return
	}
	layoutHeaders, resolve := m.comp.layoutHeaders, m.comp.resolve
	v := parseGuideResolve(cc, resolve, channel)
	resolve.write(resolveAxis, channel)
	resolve.axis[channel] = v
	if resolve.axis[channel] == "shared" {
		headerChannel := "row"
		if channel == "x" {
			headerChannel = "column"
		}
		lh := layoutHeaders[headerChannel]
		for _, ac := range childAxes {
			cc.work(1)
			ht := getHeaderType(cc, ac.get("orient"))
			list := lh.get(ht)
			if list == nil {
				list = []*headerComponent{makeHeaderComponent(m, headerChannel, jsval.False)}
				if ht == "header" {
					lh.header = list
				} else {
					lh.footer = list
				}
			}
			if mainAxis := assembleAxis(cc, ac, "main", m.config, true); mainAxis.IsTruthy() {
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
	headerChannel := getHeaderChannel(channel, titleOrient)
	titleAngle := normalizeAngle(props.Lookup("titleAngle"))
	t := mk("text", title)
	if channel == "row" {
		t.Set("orient", jsval.Str("left"))
	}
	t.Set("style", jsval.Str("guide-title"))
	spreadV(t, defaultHeaderGuideBaseline(m.b().ctx, titleAngle, headerChannel))
	spreadV(t, defaultHeaderGuideAlign(m.b().ctx, headerChannel, titleAngle, props.Lookup("titleAnchor")))
	spread(t, jsval.Obj(assembleHeaderProperties(config, facetFieldDef, channel, headerTitlePropertiesMap)))
	return mkv("name", channel+"-title", "type", "group", "role", headerChannel+"-title", "title", jsval.Obj(t))
}

func defaultHeaderGuideAlign(cc *compileCtx, headerChannel string, angle Value, anchor Value) Value {
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
	if align := defaultLabelAlign(cc, angle, jsval.Str(orient), ch); align.IsTruthy() {
		return mkv("align", align)
	}
	return mkv()
}

func defaultHeaderGuideBaseline(cc *compileCtx, angle Value, channel string) Value {
	orient, ch := "top", chX
	if channel == "row" {
		orient, ch = "left", chY
	}
	if b := defaultLabelBaseline(cc, angle, jsval.Str(orient), ch, true); b.IsTruthy() {
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

func headerGetSort(cc *compileCtx, fd Value, channel string) Value {
	sort := fd.Get("sort")
	switch {
	case isSortField(cc, sort):
		return mkv("field", vgField(cc, sort, fieldRefOption{expr: "datum"}), "order", coalesce(sort.Get("order"), jsval.Str("ascending")))
	case sort.IsArr():
		return mkv("field", sortArrayIndexField(cc, fd, channel, fieldRefOption{expr: "datum"}), "order", "ascending")
	}
	return mkv("field", vgField(cc, fd, fieldRefOption{expr: "datum"}), "order", coalesce(sort, jsval.Str("ascending")))
}

func assembleLabelTitle(cc *compileCtx, fd Value, channel string, config Value) Value {
	props := getHeaderProperties([]string{"format", "formatType", "labelAngle", "labelAnchor", "labelOrient", "labelExpr"}, fd.Get("header"), config, channel)
	titleTextExpr := signalOf(formatSignalRef(cc, formatSignalOpts{
		fieldOrDatumDef: fd, format: props.Lookup("format"), formatType: props.Lookup("formatType"), expr: "parent", config: config,
	}))
	labelOrient := props.Lookup("labelOrient")
	headerChannel := getHeaderChannel(channel, labelOrient)
	var text string
	if labelExpr := props.Lookup("labelExpr"); labelExpr.IsTruthy() {
		if !labelExpr.IsStr() {
			throw("string.replace is not a function") // util.replaceAll(labelExpr, ...)
		}
		text = jsReplaceAll(jsReplaceAll(labelExpr.StrValue(), "datum.label", titleTextExpr), "datum.value", vgField(cc, fd, fieldRefOption{expr: "parent"}))
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
	spreadV(o, defaultHeaderGuideBaseline(cc, labelAngle, headerChannel))
	spreadV(o, defaultHeaderGuideAlign(cc, headerChannel, labelAngle, props.Lookup("labelAnchor")))
	spread(o, jsval.Obj(assembleHeaderProperties(config, fd, channel, headerLabelPropertiesMap)))
	return jsval.Obj(o)
}

func assembleHeaderGroup(m Model, channel, headerType string, lh *layoutHeaderComponent, hc *headerComponent) Value {
	cc := m.b().ctx

	if hc == nil {
		return jsval.Null
	}
	b := m.b()
	title := undef
	fd := lh.facetFieldDef
	config := b.config
	if fd.IsTruthy() && hc.labels.IsTruthy() {
		labelOrient := getHeaderProperty("labelOrient", fd.Get("header"), config, channel)
		if (channel == "row" && !strIn(labelOrient, "top", "bottom")) || (channel == "column" && !strIn(labelOrient, "left", "right")) {
			title = assembleLabelTitle(cc, fd, channel, config)
		}
	}
	f, _ := m.(*facetModel)
	isFacetWithoutRowCol := f != nil && !isFacetMapping(cc, jsval.Obj(f.facet))
	hasAxes := len(hc.axes) > 0
	if title.IsTruthy() || hasAxes {
		sizeChannel := "width"
		if channel == "row" {
			sizeChannel = "height"
		}
		o := mk("name", b.getName(channel+"_"+headerType), "type", "group", "role", channel+"-"+headerType)
		if lh.facetFieldDef.IsTruthy() {
			o.Set("from", mkv("data", b.getName(channel+"_domain")))
			o.Set("sort", headerGetSort(cc, fd, channel))
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

// getLayoutTitleBand is LAYOUT_TITLE_BAND[headerChannel][titleAnchor]: the
// anchor is looked up by its text, and a name every object inherits finds
// that member (Object.prototype for "__proto__", which prints as {}).
func getLayoutTitleBand(titleAnchor Value, headerChannel string) Value {
	a := propKey(titleAnchor)
	if v, ok := inheritedValue(a); ok {
		return v
	}
	if band, ok := layoutTitleBand[headerChannel][a]; ok {
		return jsval.Int(band)
	}
	return undef
}

// layoutTitleBand is upstream's LAYOUT_TITLE_BAND.
var layoutTitleBand = map[string]map[string]int{
	"column": {"start": 0, "end": 1},
	"row":    {"start": 1, "end": 0},
}

func assembleLayoutTitleBand(headers map[string]*layoutHeaderComponent, config Value) Value {
	titleBand := jsval.NewObject(2)
	for _, channel := range facetChannels {
		hc := headers[channel]
		if hc != nil && hc.facetFieldDef.IsTruthy() {
			props := getHeaderProperties([]string{"titleAnchor", "titleOrient"}, hc.facetFieldDef.Get("header"), config, channel)
			headerChannel := getHeaderChannel(channel, props.Lookup("titleOrient"))
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
