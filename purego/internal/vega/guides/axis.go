package guides

import (
	"errors"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// AxisTicksParams are the raw (unresolved) parameters of the AxisTicks
// operator that feeds an axis. Count, Values, MinStep, FormatType and Format
// may be signal references; Scale names the scale to draw ticks for.
type AxisTicksParams struct {
	Scale      string
	Extra      Value
	Count      Value
	Values     Value
	MinStep    Value
	FormatType Value
	Format     Value
}

// AxisPlan is a parsed axis specification.
type AxisPlan struct {
	// Datum is the single-element data source of the axis group.
	Datum Value
	// Ticks are the parameters of the AxisTicks operator producing the
	// axis' tick datums.
	Ticks AxisTicksParams

	spec, config Value
	l            lookup
	band         tickBandInfo
}

// NewAxis resolves the axis config precedence and prepares an axis. It fails
// only for a specification that upstream would reject with an exception (a
// missing scale or orient).
func NewAxis(spec Value, sc Scope) (*AxisPlan, error) {
	if !spec.IsObj() {
		return nil, errors.New("axis: specification must be an object")
	}
	if !spec.Get("scale").IsStr() {
		return nil, errors.New("axis: missing scale")
	}
	config, err := axisConfig(spec, sc)
	if err != nil {
		return nil, err
	}
	l := lookup{spec: spec, config: config}
	band := tickBand(l)
	p := &AxisPlan{spec: spec, config: config, l: l, band: band}
	p.Datum = objv(
		"scale", spec.Get("scale"),
		"ticks", jsval.Bool(l.get("ticks").IsTruthy()),
		"labels", jsval.Bool(l.get("labels").IsTruthy()),
		"grid", jsval.Bool(l.get("grid").IsTruthy()),
		"domain", jsval.Bool(l.get("domain").IsTruthy()),
		"title", jsval.Bool(!spec.Get("title").IsNullish()),
	)
	p.Ticks = AxisTicksParams{
		Scale:      spec.Get("scale").StrValue(),
		Extra:      band.extra,
		Count:      spec.Get("tickCount"),
		Values:     spec.Get("values"),
		MinStep:    spec.Get("tickMinStep"),
		FormatType: spec.Get("formatType"),
		Format:     spec.Get("format"),
	}
	return p, nil
}

// Mark returns the axis group definition. dataRef feeds the group (one
// datum, see Datum) and ticksRef feeds grid, tick and label marks.
func (p *AxisPlan) Mark(dataRef, ticksRef Value) Value {
	spec, _ := p.spec, p.config
	l := p.l
	encode := spec.Get("encode")
	axisEncode := encode.Get("axis")
	datum := p.Datum

	var children []Value
	var size Value
	if datum.Get("grid").BoolValue() {
		children = append(children, axisGrid(spec, l, encode.Get("grid"), ticksRef, p.band))
	}
	if datum.Get("ticks").BoolValue() {
		size = l.get("tickSize")
		children = append(children, axisTicks(spec, l, encode.Get("ticks"), ticksRef, size, p.band))
	}
	if datum.Get("labels").BoolValue() {
		if !datum.Get("ticks").BoolValue() {
			size = jsval.Num(0)
		}
		children = append(children, axisLabels(spec, l, encode.Get("labels"), ticksRef, size, p.band))
	}
	if datum.Get("domain").BoolValue() {
		children = append(children, axisDomain(spec, l, encode.Get("domain"), dataRef))
	}
	if datum.Get("title").BoolValue() {
		children = append(children, axisTitle(spec, l, encode.Get("title"), dataRef))
	}

	m := obj(
		"role", "axis",
		"from", dataRef,
		"encode", jsval.Obj(extendEncode(p.axisEncode(), axisEncode)),
		"marks", jsval.Arr(children),
		"aria", l.get("aria"),
		"description", l.get("description"),
		"zindex", l.get("zindex"),
		"name", or(axisEncode.Get("name"), jsval.Undefined),
		"interactive", axisEncode.Get("interactive"),
		"style", axisEncode.Get("style"),
	)
	return guideGroup(m)
}

func (p *AxisPlan) axisEncode() *Object {
	encode := obj("enter", objv(), "update", objv())
	spec, l := p.spec, p.l
	addEncoders(encode, []kv{
		{"orient", l.get("orient")},
		{"offset", or(l.get("offset"), jsval.Int(0))},
		{"position", value(spec.Get("position"), jsval.Int(0))},
		{"titlePadding", l.get("titlePadding")},
		{"minExtent", l.get("minExtent")},
		{"maxExtent", l.get("maxExtent")},
		{"range", objv("signal", `abs(span(range("`+spec.Get("scale").StrValue()+`")))`)},
		{"translate", l.get("translate")},
		// accessibility support
		{"format", spec.Get("format")},
		{"formatType", spec.Get("formatType")},
	}, nil)
	return encode
}

func axisDomain(spec Value, l lookup, userEncode, dataRef Value) Value {
	orient := spec.Get("orient")
	enter := obj("opacity", zero())
	update := obj("opacity", one())
	encode := obj("enter", enter, "update", update, "exit", objv("opacity", zero()))
	addEncoders(encode, []kv{
		{"stroke", l.get("domainColor")},
		{"strokeCap", l.get("domainCap")},
		{"strokeDash", l.get("domainDash")},
		{"strokeDashOffset", l.get("domainDashOffset")},
		{"strokeWidth", l.get("domainWidth")},
		{"strokeOpacity", l.get("domainOpacity")},
	}, nil)

	pos := func(r int) Value { return objv("scale", spec.Get("scale"), "range", r) }
	pos0, pos1 := pos(0), pos(1)

	x := ifX(orient, pos0, zero())
	set(enter, "x", x)
	set(update, "x", x)
	x2 := ifX(orient, pos1, jsval.Undefined)
	set(enter, "x2", x2)
	set(update, "x2", x2)
	y := ifY(orient, pos0, zero())
	set(enter, "y", y)
	set(update, "y", y)
	y2 := ifY(orient, pos1, jsval.Undefined)
	set(enter, "y2", y2)
	set(update, "y2", y2)

	return guideMark(obj("type", "rule", "role", "axis-domain", "from", dataRef, "encode", encode), userEncode)
}

// offsetValue negates a grid offset for right and bottom axes (sign -1),
// pushing the negation into the innermost `mult` of an offset encoder.
func offsetValue(offset Value, sign Value) Value {
	if !isSignal(sign) && sign.NumValue() == 1 {
		return offset // no further adjustment needed
	}
	if !isObject(offset) {
		if isSignal(sign) {
			return objv("signal", "("+signalOf(sign)+") * ("+jsOr(offset, "0")+")")
		}
		o := 0.0
		if offset.IsTruthy() {
			o = jsval.ToNumber(offset)
		}
		return jsval.Num(sign.NumValue() * o)
	}
	// clone the offset chain along its mult links
	root := extend(jsval.NewObject(0), offset)
	entry := root
	for !entry.Lookup("mult").IsNullish() {
		m := entry.Lookup("mult")
		if !isObject(m) {
			if isSignal(sign) {
				entry.Set("mult", objv("signal", "("+m.AsString()+") * ("+signalOf(sign)+")"))
			} else {
				entry.Set("mult", jsval.Num(jsval.ToNumber(m)*sign.NumValue()))
			}
			return jsval.Obj(root)
		}
		next := extend(jsval.NewObject(0), m)
		entry.Set("mult", jsval.Obj(next))
		entry = next
	}
	entry.Set("mult", sign)
	return jsval.Obj(root)
}

// jsOr renders `offset || dflt` inside a template literal.
func jsOr(v Value, dflt string) string {
	if v.IsTruthy() {
		return v.AsString()
	}
	return dflt
}

func axisGrid(spec Value, l lookup, userEncode, dataRef Value, band tickBandInfo) Value {
	orient := spec.Get("orient")
	vscale := spec.Get("gridScale")
	sign := getSign(orient, 1, -1)
	offset := offsetValue(spec.Get("offset"), sign)

	enter := obj("opacity", zero())
	update := obj("opacity", one())
	exit := obj("opacity", zero())
	encode := obj("enter", enter, "update", update, "exit", exit)

	addEncoders(encode, []kv{
		{"stroke", l.get("gridColor")},
		{"strokeCap", l.get("gridCap")},
		{"strokeDash", l.get("gridDash")},
		{"strokeDashOffset", l.get("gridDashOffset")},
		{"strokeOpacity", l.get("gridOpacity")},
		{"strokeWidth", l.get("gridWidth")},
	}, nil)

	tickPos := func() Value {
		return objv(
			"scale", spec.Get("scale"),
			"field", fValue,
			"band", band.band,
			"extra", band.extra,
			"offset", band.offset,
			"round", l.get("tickRound"),
		)
	}

	sz := ifX(orient, objv("signal", "height"), objv("signal", "width"))

	var gridStart, gridEnd Value
	if vscale.IsTruthy() {
		gridStart = objv("scale", vscale, "range", 0, "mult", sign, "offset", offset)
		gridEnd = objv("scale", vscale, "range", 1, "mult", sign, "offset", offset)
	} else {
		gridStart = objv("value", 0, "offset", offset)
		e := extend(jsval.NewObject(0), sz)
		set(e, "mult", sign)
		set(e, "offset", offset)
		gridEnd = jsval.Obj(e)
	}

	x := ifX(orient, tickPos(), gridStart)
	set(enter, "x", x)
	set(update, "x", x)
	y := ifY(orient, tickPos(), gridStart)
	set(enter, "y", y)
	set(update, "y", y)
	x2 := ifY(orient, gridEnd, jsval.Undefined)
	set(enter, "x2", x2)
	set(update, "x2", x2)
	y2 := ifX(orient, gridEnd, jsval.Undefined)
	set(enter, "y2", y2)
	set(update, "y2", y2)
	set(exit, "x", ifX(orient, tickPos(), jsval.Undefined))
	set(exit, "y", ifY(orient, tickPos(), jsval.Undefined))

	return guideMark(obj("type", "rule", "role", "axis-grid", "key", fValue, "from", dataRef, "encode", encode), userEncode)
}

func axisTicks(spec Value, l lookup, userEncode, dataRef, size Value, band tickBandInfo) Value {
	orient := spec.Get("orient")
	sign := getSign(orient, -1, 1)

	enter := obj("opacity", zero())
	update := obj("opacity", one())
	exit := obj("opacity", zero())
	encode := obj("enter", enter, "update", update, "exit", exit)

	addEncoders(encode, []kv{
		{"stroke", l.get("tickColor")},
		{"strokeCap", l.get("tickCap")},
		{"strokeDash", l.get("tickDash")},
		{"strokeDashOffset", l.get("tickDashOffset")},
		{"strokeOpacity", l.get("tickOpacity")},
		{"strokeWidth", l.get("tickWidth")},
	}, nil)

	tickSize := encoder(size)
	set(tickSize, "mult", sign)

	tickPos := func() Value {
		return objv(
			"scale", spec.Get("scale"),
			"field", fValue,
			"band", band.band,
			"extra", band.extra,
			"offset", band.offset,
			"round", l.get("tickRound"),
		)
	}

	ts := jsval.Obj(tickSize)
	y := ifX(orient, zero(), tickPos())
	set(update, "y", y)
	set(enter, "y", y)
	y2 := ifX(orient, ts, jsval.Undefined)
	set(update, "y2", y2)
	set(enter, "y2", y2)
	set(exit, "x", ifX(orient, tickPos(), jsval.Undefined))

	x := ifY(orient, zero(), tickPos())
	set(update, "x", x)
	set(enter, "x", x)
	x2 := ifY(orient, ts, jsval.Undefined)
	set(update, "x2", x2)
	set(enter, "x2", x2)
	set(exit, "y", ifY(orient, tickPos(), jsval.Undefined))

	return guideMark(obj("type", "rule", "role", "axis-tick", "key", fValue, "from", dataRef, "encode", encode), userEncode)
}

func flushExpr(scale string, threshold Value, a, b, c string) Value {
	return objv("signal", `flush(range("`+scale+`"), `+
		`scale("`+scale+`", datum.value), `+
		threshold.AsString()+","+a+","+b+","+c+")")
}

func axisLabels(spec Value, l lookup, userEncode, dataRef, size Value, band tickBandInfo) Value {
	orient := spec.Get("orient")
	scale := spec.Get("scale").StrValue()
	sign := getSign(orient, -1, 1)
	flush := deref(l.get("labelFlush"))
	flushOffset := deref(l.get("labelFlushOffset"))
	labelAlign := l.get("labelAlign")
	labelBaseline := l.get("labelBaseline")

	flushOn := (flush.IsNum() && flush.NumValue() == 0) || flush.IsTruthy()

	tickSize := encoder(size)
	set(tickSize, "mult", sign)
	pad := encoder(or(l.get("labelPadding"), jsval.Int(0)))
	set(pad, "mult", sign)
	set(tickSize, "offset", jsval.Obj(pad))

	tickPos := objv(
		"scale", scale,
		"field", fValue,
		"band", 0.5,
		"offset", extendOffset(band.offset, l.get("labelOffset")),
	)

	var alignC Value
	if flushOn {
		alignC = flushExpr(scale, flush, `"left"`, `"right"`, `"center"`)
	} else {
		alignC = objv("value", "center")
	}
	align := ifX(orient, alignC, ifRight(orient, "left", "right"))

	var baseC Value
	if flushOn {
		baseC = flushExpr(scale, flush, `"top"`, `"bottom"`, `"middle"`)
	} else {
		baseC = objv("value", "middle")
	}
	baseline := ifX(orient, ifTop(orient, "bottom", "top"), baseC)

	offsetExpr := flushExpr(scale, flush, "-("+flushOffset.AsString()+")", flushOffset.AsString(), "0")
	flushOn = flushOn && flushOffset.IsTruthy()

	ts := jsval.Obj(tickSize)
	enter := obj(
		"opacity", zero(),
		"x", ifX(orient, tickPos, ts),
		"y", ifY(orient, tickPos, ts),
	)
	update := obj(
		"opacity", one(),
		"text", objv("field", fLabel),
		"x", enter.Lookup("x"),
		"y", enter.Lookup("y"),
		"align", align,
		"baseline", baseline,
	)
	encode := obj(
		"enter", enter,
		"update", update,
		"exit", objv("opacity", zero(), "x", enter.Lookup("x"), "y", enter.Lookup("y")),
	)

	var dx, dy Value = jsval.Null, jsval.Null
	if !labelAlign.IsTruthy() && flushOn {
		dx = ifX(orient, offsetExpr, jsval.Undefined)
	}
	if !labelBaseline.IsTruthy() && flushOn {
		dy = ifY(orient, offsetExpr, jsval.Undefined)
	}
	addEncoders(encode, []kv{{"dx", dx}, {"dy", dy}}, nil)

	addEncoders(encode, []kv{
		{"angle", l.get("labelAngle")},
		{"fill", l.get("labelColor")},
		{"fillOpacity", l.get("labelOpacity")},
		{"font", l.get("labelFont")},
		{"fontSize", l.get("labelFontSize")},
		{"fontWeight", l.get("labelFontWeight")},
		{"fontStyle", l.get("labelFontStyle")},
		{"limit", l.get("labelLimit")},
		{"lineHeight", l.get("labelLineHeight")},
	}, []kv{
		{"align", labelAlign},
		{"baseline", labelBaseline},
	})

	bound := l.get("labelBound")
	overlapM := l.get("labelOverlap")

	// if overlap method or bound defined, request label overlap removal
	var overlap Value
	if overlapM.IsTruthy() || bound.IsTruthy() {
		var b Value = jsval.Null
		if bound.IsTruthy() {
			b = objv("scale", scale, "orient", orient, "tolerance", bound)
		}
		overlap = objv(
			"separation", l.get("labelSeparation"),
			"method", overlapM,
			"order", "datum.index",
			"bound", b,
		)
	}

	if !jsval.SameRef(update.Lookup("align"), align) {
		set(update, "align", patch(update.Lookup("align"), align))
	}
	if !jsval.SameRef(update.Lookup("baseline"), baseline) {
		set(update, "baseline", patch(update.Lookup("baseline"), baseline))
	}

	return guideMark(obj(
		"type", "text",
		"role", "axis-label",
		"style", guideLabelStyle,
		"key", fValue,
		"from", dataRef,
		"encode", encode,
		"overlap", overlap,
	), userEncode)
}

func axisTitle(spec Value, l lookup, userEncode, dataRef Value) Value {
	orient := spec.Get("orient")
	sign := getSign(orient, -1, 1)

	enter := obj(
		"opacity", zero(),
		"anchor", jsval.Obj(encoder(l.getOr("titleAnchor", jsval.Null))),
		"align", objv("signal", alignExpr),
	)
	update := extend(jsval.NewObject(0), jsval.Obj(enter))
	set(update, "opacity", one())
	set(update, "text", jsval.Obj(encoder(spec.Get("title"))))
	encode := obj("enter", enter, "update", update, "exit", objv("opacity", zero()))

	titlePos := objv("signal", `lerp(range("`+spec.Get("scale").StrValue()+`"), `+anchorExpr("0", "1", "0.5")+")")
	set(update, "x", ifX(orient, titlePos, jsval.Undefined))
	set(update, "y", ifY(orient, titlePos, jsval.Undefined))
	set(enter, "angle", ifX(orient, zero(), mult(sign, 90)))
	set(enter, "baseline", ifX(orient, ifTop(orient, bottom, top), objv("value", bottom)))
	set(update, "angle", enter.Lookup("angle"))
	set(update, "baseline", enter.Lookup("baseline"))

	addEncoders(encode, []kv{
		{"fill", l.get("titleColor")},
		{"fillOpacity", l.get("titleOpacity")},
		{"font", l.get("titleFont")},
		{"fontSize", l.get("titleFontSize")},
		{"fontStyle", l.get("titleFontStyle")},
		{"fontWeight", l.get("titleFontWeight")},
		{"limit", l.get("titleLimit")},
		{"lineHeight", l.get("titleLineHeight")},
	}, []kv{ // require update
		{"align", l.get("titleAlign")},
		{"angle", l.get("titleAngle")},
		{"baseline", l.get("titleBaseline")},
	})

	axisTitleAutoLayout(l, orient, encode, userEncode)
	set(update, "align", patch(update.Lookup("align"), enter.Lookup("align")))
	set(update, "angle", patch(update.Lookup("angle"), enter.Lookup("angle")))
	set(update, "baseline", patch(update.Lookup("baseline"), enter.Lookup("baseline")))

	return guideMark(obj("type", "text", "role", "axis-title", "style", guideTitleStyle, "from", dataRef, "encode", encode), userEncode)
}

// axisTitleAutoLayout sets the title's `auto` flag: the title is positioned by
// ViewLayout unless the specification places it explicitly (titleX/titleY or a
// user x/y encoder).
func axisTitleAutoLayout(l lookup, orient Value, encode *Object, userEncode Value) {
	update := encode.Lookup("update").ObjValue()
	auto := func(v Value, dim string) bool {
		if !v.IsNullish() {
			set(update, dim, patch(jsval.Obj(encoder(v)), update.Lookup(dim)))
			return false
		}
		return !hasEncoder(dim, userEncode)
	}
	autoY := auto(l.get("titleX"), "x")
	autoX := auto(l.get("titleY"), "y")
	enter := encode.Lookup("enter").ObjValue()
	if autoX == autoY {
		enter.Set("auto", jsval.Obj(encoder(jsval.Bool(autoX))))
	} else {
		enter.Set("auto", ifX(orient,
			jsval.Obj(encoder(jsval.Bool(autoX))), jsval.Obj(encoder(jsval.Bool(autoY)))))
	}
}
