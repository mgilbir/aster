package guides

import (
	"errors"

	"github.com/mgilbir/aster/purego/internal/jsval"
	"github.com/mgilbir/aster/purego/internal/scale"
)

// LegendEntriesParams are the raw parameters of the LegendEntries operator
// that feeds a legend. Count, Limit, Values, MinStep, FormatType and Format
// may be signal references.
type LegendEntriesParams struct {
	Type       string
	Scale      string // canonical scale name
	Count      Value
	Limit      Value
	Values     Value
	MinStep    Value
	FormatType Value
	Format     Value

	// SizeExpr, set for symbol legends, is the expression computing the
	// symbol extent of an entry (`datum` is the value); the operator's
	// `size` function is this expression.
	SizeExpr string
	// CountExpr, set for gradient legends, is the signal expression that
	// replaces a missing (falsy) Count: the default tick count grows with
	// the gradient length.
	CountExpr string
}

// LegendPlan is a parsed legend specification.
type LegendPlan struct {
	// Datum is the single-element data source of the legend group.
	Datum Value
	// Entries are the parameters of the LegendEntries operator.
	Entries LegendEntriesParams

	spec        Value
	config      Value
	l           lookup
	scale       string
	typ         string
	encode      Value
	entryLayout Value
}

// legendType resolves symbol / gradient / discrete: with a single fill or
// stroke scale and no explicit type the scale decides; a requested gradient
// over a discretizing scale becomes a discrete (stepped) gradient.
func legendType(spec Value, scaleType string) string {
	typ := or(spec.Get("type"), jsval.Str(symbolLegend)).AsString()
	if !spec.Get("type").IsTruthy() && legendScaleCount(spec) == 1 &&
		(spec.Get("fill").IsTruthy() || spec.Get("stroke").IsTruthy()) {
		switch {
		case scale.IsContinuous(scaleType):
			typ = gradientLegend
		case scale.IsDiscretizing(scaleType):
			typ = discreteLegend
		default:
			typ = symbolLegend
		}
	}
	if typ != gradientLegend {
		return typ
	}
	if scale.IsDiscretizing(scaleType) {
		return discreteLegend
	}
	return gradientLegend
}

func legendScaleCount(spec Value) int {
	n := 0
	for _, s := range legendScales {
		if spec.Get(s).IsTruthy() {
			n++
		}
	}
	return n
}

// NewLegend prepares a legend. It fails for a specification without any
// legend scale, as upstream does.
func NewLegend(spec Value, sc Scope) (*LegendPlan, error) {
	if !spec.IsObj() {
		return nil, errors.New("legend: specification must be an object")
	}
	config := sc.Config().Get("legend")
	l := lookup{spec: spec, config: config}
	encode := spec.Get("encode")

	scales := jsval.NewObject(0)
	scale := ""
	for _, s := range legendScales {
		if v := spec.Get(s); v.IsTruthy() {
			scales.Set(s, v)
			if scale == "" {
				scale = v.AsString()
			}
		}
	}
	if scale == "" {
		return nil, errors.New("Missing valid scale for legend.")
	}
	typ := legendType(spec, sc.ScaleType(scale))

	p := &LegendPlan{spec: spec, config: config, l: l, scale: scale, typ: typ, encode: encode}
	p.Datum = objv(
		"title", jsval.Bool(!spec.Get("title").IsNullish()),
		"scales", jsval.Obj(scales),
		"type", typ,
		"vgrad", jsval.Bool(typ != symbolLegend && l.isVertical(false)),
	)
	p.Entries = LegendEntriesParams{
		Type:       typ,
		Scale:      scale,
		Count:      l.get("tickCount"),
		Limit:      l.get("symbolLimit"),
		Values:     spec.Get("values"),
		MinStep:    spec.Get("tickMinStep"),
		FormatType: spec.Get("formatType"),
		Format:     spec.Get("format"),
	}

	switch typ {
	case gradientLegend:
		// adjust default tick count based on the gradient length
		p.Entries.CountExpr = "max(2,2*floor((" + deref(l.gradientLength()).AsString() + ")/100))"
	case discreteLegend:
	default:
		p.entryLayout = legendSymbolLayout(l)
	}
	// The size expression reads the encoders of the generated symbol and
	// label marks, so build them once (without a data ref) to inspect them.
	if typ == symbolLegend {
		marks := legendSymbolGroups(spec, l, config, encode, jsval.Undefined, deref(p.entryLayout.Get("columns")))
		p.Entries.SizeExpr = sizeExpression(spec, sc, marks.Get("marks"))
	}
	return p, nil
}

// Mark returns the legend group definition. dataRef feeds the legend group
// (one datum, see Datum) and entryRef feeds the entries (labels, symbols,
// gradient bands).
func (p *LegendPlan) Mark(dataRef, entryRef Value) Value {
	spec, l, config, encode := p.spec, p.l, p.config, p.encode
	legendEncode := encode.Get("legend")
	interactive := legendEncode.Get("interactive")

	var children Value
	switch p.typ {
	case gradientLegend:
		children = jsval.ArrOf(
			legendGradient(spec, p.scale, l, encode.Get("gradient")),
			legendGradientLabels(spec, l, encode.Get("labels"), entryRef),
		)
	case discreteLegend:
		children = jsval.ArrOf(
			legendGradientDiscrete(spec, p.scale, l, encode.Get("gradient"), entryRef),
			legendGradientLabels(spec, l, encode.Get("labels"), entryRef),
		)
	default:
		children = jsval.ArrOf(
			legendSymbolGroups(spec, l, config, encode, entryRef, deref(p.entryLayout.Get("columns"))),
		)
	}

	entry := guideGroup(obj(
		"role", "legend-entry",
		"from", dataRef,
		"encode", obj("enter", objv("x", objv("value", 0), "y", objv("value", 0))),
		"marks", children,
		"layout", p.entryLayout,
		"interactive", interactive,
	))
	kids := []Value{entry}
	if p.Datum.Get("title").BoolValue() {
		kids = append(kids, legendTitle(spec, l, encode.Get("title"), dataRef))
	}

	m := obj(
		"role", "legend",
		"from", dataRef,
		"encode", jsval.Obj(extendEncode(p.legendEncode(), legendEncode)),
		"marks", jsval.Arr(kids),
		"aria", l.get("aria"),
		"description", l.get("description"),
		"zindex", l.get("zindex"),
		"name", or(legendEncode.Get("name"), jsval.Undefined),
		"interactive", interactive,
		"style", legendEncode.Get("style"),
	)
	return guideGroup(m)
}

func (p *LegendPlan) legendEncode() *Object {
	encode := obj("enter", objv(), "update", objv())
	l, spec, config := p.l, p.spec, p.config
	addEncoders(encode, []kv{
		{"orient", l.get("orient")},
		{"offset", l.get("offset")},
		{"padding", l.get("padding")},
		{"titlePadding", l.get("titlePadding")},
		{"cornerRadius", l.get("cornerRadius")},
		{"fill", l.get("fillColor")},
		{"stroke", l.get("strokeColor")},
		{"strokeWidth", config.Get("strokeWidth")},
		{"strokeDash", config.Get("strokeDash")},
		{"x", l.get("legendX")},
		{"y", l.get("legendY")},
		// accessibility support
		{"format", spec.Get("format")},
		{"formatType", spec.Get("formatType")},
	}, nil)
	return encode
}

// sizeExpression is the expression that sizes a symbol legend entry: the
// symbol's extent plus stroke, but at least the label font size.
func sizeExpression(spec Value, sc Scope, marks Value) string {
	size := deref(getChannel("size", spec, marks))
	strokeWidth := deref(getChannel("strokeWidth", spec, marks))
	fontSize := getEncoding("fontSize", marks.Index(1).Get("encode"))
	if !fontSize.IsTruthy() {
		fontSize = sc.Config().Get("style").Get(guideLabelStyle).Get("fontSize")
	}
	fontSize = deref(fontSize)
	return "max(ceil(sqrt(" + size.AsString() + ")+" + strokeWidth.AsString() + ")," + fontSize.AsString() + ")"
}

func getChannel(name string, spec, marks Value) Value {
	if s := spec.Get(name); s.IsTruthy() {
		return jsval.Str(`scale("` + s.AsString() + `",datum)`)
	}
	return getEncoding(name, marks.Index(0).Get("encode"))
}

// getEncoding reads the constant or signal a mark's encode block assigns to
// name (update takes precedence over enter); null when unset.
func getEncoding(name string, encode Value) Value {
	v := encode.Get("update").Get(name)
	if !v.IsTruthy() {
		v = encode.Get("enter").Get(name)
	}
	switch {
	case v.IsObj() && isSignal(v):
		return v
	case v.IsTruthy():
		return v.Get("value")
	}
	return jsval.Null
}

func legendSymbolLayout(l lookup) Value {
	return objv(
		"align", l.get("gridAlign"),
		"columns", l.entryColumns(),
		"center", objv("row", true, "column", false),
		"padding", objv("row", l.get("rowPadding"), "column", l.get("columnPadding")),
	)
}
