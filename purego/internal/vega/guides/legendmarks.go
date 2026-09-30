package guides

import "github.com/mgilbir/aster/purego/internal/jsval"

func legendGradient(spec Value, scale string, l lookup, userEncode Value) Value {
	vertical := l.isVertical(false)
	thickness := l.gradientThickness()
	length := l.gradientLength()

	var start, stop, width, height Value
	if vertical {
		start = jsval.ArrOf(jsval.Int(0), jsval.Int(1))
		stop = jsval.ArrOf(jsval.Int(0), jsval.Int(0))
		width, height = thickness, length
	} else {
		start = jsval.ArrOf(jsval.Int(0), jsval.Int(0))
		stop = jsval.ArrOf(jsval.Int(1), jsval.Int(0))
		width, height = length, thickness
	}

	enter := obj(
		"opacity", zero(),
		"x", zero(),
		"y", zero(),
		"width", jsval.Obj(encoder(width)),
		"height", jsval.Obj(encoder(height)),
	)
	update := extend(jsval.NewObject(0), jsval.Obj(enter))
	set(update, "opacity", one())
	set(update, "fill", objv("gradient", scale, "start", start, "stop", stop))
	encode := obj("enter", enter, "update", update, "exit", objv("opacity", zero()))

	addEncoders(encode, []kv{
		{"stroke", l.get("gradientStrokeColor")},
		{"strokeWidth", l.get("gradientStrokeWidth")},
	}, []kv{
		{"opacity", l.get("gradientOpacity")},
	})

	return guideMark(obj("type", "rect", "role", "legend-gradient", "encode", encode), userEncode)
}

func legendGradientDiscrete(spec Value, scale string, l lookup, userEncode, dataRef Value) Value {
	vertical := l.isVertical(false)
	thickness := l.gradientThickness()
	length := l.gradientLength()

	var u, v, uu, vv, adjust string
	if vertical {
		u, uu, v, vv, adjust = "y", "y2", "x", "width", "1-"
	} else {
		u, uu, v, vv = "x", "x2", "y", "height"
	}

	enter := obj(
		"opacity", zero(),
		"fill", objv("scale", scale, "field", fValue),
	)
	set(enter, u, objv("signal", adjust+"datum."+fPerc, "mult", length))
	set(enter, v, zero())
	set(enter, uu, objv("signal", adjust+"datum."+fPerc2, "mult", length))
	set(enter, vv, jsval.Obj(encoder(thickness)))

	update := extend(jsval.NewObject(0), jsval.Obj(enter))
	set(update, "opacity", one())
	encode := obj("enter", enter, "update", update, "exit", objv("opacity", zero()))

	addEncoders(encode, []kv{
		{"stroke", l.get("gradientStrokeColor")},
		{"strokeWidth", l.get("gradientStrokeWidth")},
	}, []kv{
		{"opacity", l.get("gradientOpacity")},
	})

	return guideMark(obj("type", "rect", "role", "legend-band", "key", fValue, "from", dataRef, "encode", encode), userEncode)
}

const (
	gradAlignExpr    = `datum.perc<=0?"left":datum.perc>=1?"right":"center"`
	gradBaselineExpr = `datum.perc<=0?"bottom":datum.perc>=1?"top":"middle"`
)

func legendGradientLabels(spec Value, l lookup, userEncode, dataRef Value) Value {
	vertical := l.isVertical(false)
	thickness := encoder(l.gradientThickness())
	length := l.gradientLength()
	overlap := l.get("labelOverlap")

	enter := obj("opacity", zero())
	update := obj("opacity", one(), "text", objv("field", fLabel))
	encode := obj("enter", enter, "update", update, "exit", objv("opacity", zero()))

	addEncoders(encode, []kv{
		{"fill", l.get("labelColor")},
		{"fillOpacity", l.get("labelOpacity")},
		{"font", l.get("labelFont")},
		{"fontSize", l.get("labelFontSize")},
		{"fontStyle", l.get("labelFontStyle")},
		{"fontWeight", l.get("labelFontWeight")},
		{"limit", value(spec.Get("labelLimit"), l.config.Get("gradientLabelLimit"))},
	}, nil)

	var u, v, adjust string
	if vertical {
		set(enter, "align", objv("value", "left"))
		b := objv("signal", gradBaselineExpr)
		set(enter, "baseline", b)
		set(update, "baseline", b)
		u, v, adjust = "y", "x", "1-"
	} else {
		a := objv("signal", gradAlignExpr)
		set(enter, "align", a)
		set(update, "align", a)
		set(enter, "baseline", objv("value", "top"))
		u, v = "x", "y"
	}

	pos := objv("signal", adjust+"datum."+fPerc, "mult", length)
	set(enter, u, pos)
	set(update, u, pos)

	th := jsval.Obj(thickness)
	set(enter, v, th)
	set(update, v, th)
	set(thickness, "offset", or(value(spec.Get("labelOffset"), l.config.Get("gradientLabelOffset")), jsval.Int(0)))

	var ov Value
	if overlap.IsTruthy() {
		ov = objv("separation", l.get("labelSeparation"), "method", overlap, "order", "datum."+fIndex)
	}

	return guideMark(obj(
		"type", "text",
		"role", "legend-label",
		"style", guideLabelStyle,
		"key", fValue,
		"from", dataRef,
		"encode", encode,
		"overlap", ov,
	), userEncode)
}

// legendSymbolGroups builds the entry groups of a symbol legend. userEncode is
// the whole `encode` block of the legend (entries, symbols, labels).
func legendSymbolGroups(spec Value, l lookup, config, userEncode, dataRef, columns Value) Value {
	entries := userEncode.Get("entries")
	interactive := entries.Get("interactive").IsTruthy()
	name := entries.Get("name")
	height := l.get("clipHeight")
	symbolOffset := l.get("symbolOffset")
	valueRef := objv("data", "value")
	cols := columns.AsString()
	xSignal := "(" + cols + ") ? datum." + fOffset + " : datum." + fSize
	var yEncode *Object
	if height.IsTruthy() {
		yEncode = encoder(height)
	} else {
		yEncode = obj("field", fSize)
	}
	index := "datum." + fIndex
	ncols := "max(1, " + cols + ")"
	set(yEncode, "mult", jsval.Num(0.5))
	y := jsval.Obj(yEncode)

	// -- LEGEND SYMBOLS --
	enter := obj(
		"opacity", zero(),
		"x", objv("signal", xSignal, "mult", 0.5, "offset", symbolOffset),
		"y", y,
	)
	update := obj("opacity", one(), "x", enter.Lookup("x"), "y", enter.Lookup("y"))
	encode := obj("enter", enter, "update", update, "exit", objv("opacity", zero()))

	baseFill, baseStroke := jsval.Null, jsval.Null
	if !spec.Get("fill").IsTruthy() {
		baseFill = config.Get("symbolBaseFillColor")
		baseStroke = config.Get("symbolBaseStrokeColor")
	}
	addEncoders(encode, []kv{
		{"fill", l.getOr("symbolFillColor", baseFill)},
		{"shape", l.get("symbolType")},
		{"size", l.get("symbolSize")},
		{"stroke", l.getOr("symbolStrokeColor", baseStroke)},
		{"strokeDash", l.get("symbolDash")},
		{"strokeDashOffset", l.get("symbolDashOffset")},
		{"strokeWidth", l.get("symbolStrokeWidth")},
	}, []kv{
		{"opacity", l.get("symbolOpacity")},
	})
	for _, sc := range legendScales {
		if s := spec.Get(sc); s.IsTruthy() {
			ref := objv("scale", s, "field", fValue)
			set(update, sc, ref)
			set(enter, sc, ref)
		}
	}
	var clip Value
	if height.IsTruthy() {
		clip = jsval.True
	}
	symbols := guideMark(obj(
		"type", "symbol",
		"role", "legend-symbol",
		"key", fValue,
		"from", valueRef,
		"clip", clip,
		"encode", encode,
	), userEncode.Get("symbols"))

	// -- LEGEND LABELS --
	labelOffset := encoder(symbolOffset)
	set(labelOffset, "offset", l.get("labelOffset"))

	enter = obj(
		"opacity", zero(),
		"x", objv("signal", xSignal, "offset", labelOffset),
		"y", y,
	)
	update = obj(
		"opacity", one(),
		"text", objv("field", fLabel),
		"x", enter.Lookup("x"),
		"y", enter.Lookup("y"),
	)
	encode = obj("enter", enter, "update", update, "exit", objv("opacity", zero()))
	addEncoders(encode, []kv{
		{"align", l.get("labelAlign")},
		{"baseline", l.get("labelBaseline")},
		{"fill", l.get("labelColor")},
		{"fillOpacity", l.get("labelOpacity")},
		{"font", l.get("labelFont")},
		{"fontSize", l.get("labelFontSize")},
		{"fontStyle", l.get("labelFontStyle")},
		{"fontWeight", l.get("labelFontWeight")},
		{"limit", l.get("labelLimit")},
	}, nil)
	labels := guideMark(obj(
		"type", "text",
		"role", "legend-label",
		"style", guideLabelStyle,
		"key", fValue,
		"from", valueRef,
		"encode", encode,
	), userEncode.Get("labels"))

	// -- LEGEND ENTRY GROUPS --
	var h Value
	if height.IsTruthy() {
		h = jsval.Obj(encoder(height))
	} else {
		h = zero()
	}
	rowSig, colSig := jsval.Null, jsval.Null
	upd := obj("opacity", one(), "row", objv("signal", rowSig), "column", objv("signal", colSig))
	encode = obj(
		"enter", objv("noBound", objv("value", jsval.Bool(!height.IsTruthy())), "width", zero(), "height", h, "opacity", zero()),
		"exit", objv("opacity", zero()),
		"update", upd,
	)

	// annotate and sort groups to ensure correct ordering
	var rowExpr, colExpr string
	var sort Value
	if l.isVertical(true) {
		nrows := "ceil(item.mark.items.length / " + ncols + ")"
		rowExpr = index + "%" + nrows
		colExpr = "floor(" + index + " / " + nrows + ")"
		sort = objv("field", jsval.ArrOf(jsval.Str("row"), jsval.Str(index)))
	} else {
		rowExpr = "floor(" + index + " / " + ncols + ")"
		colExpr = index + " % " + ncols
		sort = objv("field", index)
	}
	// handle zero column case (implies infinite columns)
	colExpr = "(" + cols + ")?" + colExpr + ":" + index
	upd.Set("row", objv("signal", rowExpr))
	upd.Set("column", objv("signal", colExpr))

	// facet legend entries into sub-groups
	facet := objv("facet", objv("data", dataRef, "name", "value", "groupby", fIndex))

	return guideGroup(obj(
		"role", "scope",
		"from", facet,
		"encode", jsval.Obj(extendEncode(encode, entries)),
		"marks", jsval.ArrOf(symbols, labels),
		"name", name,
		"interactive", jsval.Bool(interactive),
		"sort", sort,
	))
}

// Legend title expressions: alignment, anchor, angle and baseline depend on
// the legend orient and on whether the gradient is vertical.
var (
	isL          = `item.orient === "left"`
	isR          = `item.orient === "right"`
	isLR         = "(" + isL + " || " + isR + ")"
	isVG         = "datum.vgrad && " + isLR
	titleBase    = anchorExpr(`"top"`, `"bottom"`, `"middle"`)
	titleFlip    = anchorExpr(`"right"`, `"left"`, `"center"`)
	exprAlign    = "datum.vgrad && " + isR + " ? (" + titleFlip + ") : (" + isLR + " && !(datum.vgrad && " + isL + ")) ? \"left\" : " + alignExpr
	exprAnchor   = `item._anchor || (` + isLR + ` ? "middle" : "start")`
	exprAngle    = isVG + " ? (" + isL + " ? -90 : 90) : 0"
	exprBaseline = isLR + ` ? (datum.vgrad ? (` + isR + ` ? "bottom" : "top") : ` + titleBase + `) : "top"`
)

func legendTitle(spec Value, l lookup, userEncode, dataRef Value) Value {
	encode := obj(
		"enter", objv("opacity", zero()),
		"update", objv("opacity", one(), "x", objv("field", objv("group", "padding")), "y", objv("field", objv("group", "padding"))),
		"exit", objv("opacity", zero()),
	)
	addEncoders(encode, []kv{
		{"orient", l.get("titleOrient")},
		{"_anchor", l.get("titleAnchor")},
		{"anchor", objv("signal", exprAnchor)},
		{"angle", objv("signal", exprAngle)},
		{"align", objv("signal", exprAlign)},
		{"baseline", objv("signal", exprBaseline)},
		{"text", spec.Get("title")},
		{"fill", l.get("titleColor")},
		{"fillOpacity", l.get("titleOpacity")},
		{"font", l.get("titleFont")},
		{"fontSize", l.get("titleFontSize")},
		{"fontStyle", l.get("titleFontStyle")},
		{"fontWeight", l.get("titleFontWeight")},
		{"limit", l.get("titleLimit")},
		{"lineHeight", l.get("titleLineHeight")},
	}, []kv{
		{"align", l.get("titleAlign")},
		{"baseline", l.get("titleBaseline")},
	})
	return guideMark(obj("type", "text", "role", "legend-title", "style", guideTitleStyle, "from", dataRef, "encode", encode), userEncode)
}
