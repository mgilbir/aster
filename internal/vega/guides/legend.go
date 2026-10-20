package guides

import (
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/scale"
)

// LegendEntriesParams are the raw parameters of the LegendEntries operator
// that feeds a legend. Count, Limit, Values, MinStep, FormatType and Format
// may be signal references.
type LegendEntriesParams struct {
	// Type is the legend type as upstream resolves it: spec.type as given
	// (a value of any type), else the type the scale decides.
	Type       Value
	Scale      string // canonical scale name (its property key)
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
	scale       Value
	typ         Value
	encode      Value
	entryLayout Value
	symbols     Value // the symbol groups, built once for the size expression
}

// legendType resolves symbol / gradient / discrete: with a single fill or
// stroke scale and no explicit type the scale decides; a requested gradient
// over a discretizing scale becomes a discrete (stepped) gradient. A type
// given is kept as it is (`spec.type || 'symbol'`), so a type that is no
// string matches none of them.
func legendType(l lookup, scaleType string) Value {
	st := l.sp("type")
	typ := or(st, jsval.Str(symbolLegend))
	if !st.IsTruthy() && legendScaleCount(l) == 1 && (l.sp("fill").IsTruthy() || l.sp("stroke").IsTruthy()) {
		switch {
		case scale.IsContinuous(scaleType):
			typ = jsval.Str(gradientLegend)
		case scale.IsDiscretizing(scaleType):
			typ = jsval.Str(discreteLegend)
		default:
			typ = jsval.Str(symbolLegend)
		}
	}
	if !strictIs(typ, gradientLegend) {
		return typ
	}
	if scale.IsDiscretizing(scaleType) {
		return jsval.Str(discreteLegend)
	}
	return jsval.Str(gradientLegend)
}

// strictIs is `v === s`.
func strictIs(v Value, s string) bool { return v.IsStr() && v.StrValue() == s }

func legendScaleCount(l lookup) int {
	n := 0
	for _, s := range legendScales {
		if l.sp(s).IsTruthy() {
			n++
		}
	}
	return n
}

// NewLegend prepares a legend. It fails where upstream's parse throws: for a
// specification without any legend scale, a config that is null, a value
// whose member upstream reads is null.
func NewLegend(spec Value, sc Scope) (p *LegendPlan, err error) {
	defer catch(&err)
	config := member(sc.Config(), "legend")
	l := lookup{spec: spec, config: config, sc: sc}
	encode := or(member(spec, "encode"), objv())

	scales := jsval.NewObject(0)
	scaleV := jsval.Value(jsval.Int(0))
	for _, s := range legendScales {
		if v := member(spec, s); v.IsTruthy() {
			scales.Set(s, v)
			scaleV = or(scaleV, v)
		}
	}
	if !scaleV.IsTruthy() {
		return nil, &Thrown{msg: "Missing valid scale for legend."}
	}
	key := scaleKey(scaleV)
	typ := legendType(l, sc.ScaleType(key))

	p = &LegendPlan{spec: spec, config: config, l: l, scale: scaleV, typ: typ, encode: encode}
	p.Datum = objv(
		"title", jsval.Bool(!l.sp("title").IsNullish()),
		"scales", jsval.Obj(scales),
		"type", typ,
		"vgrad", jsval.Bool(!strictIs(typ, symbolLegend) && l.isVertical(false)),
	)
	p.Entries = LegendEntriesParams{
		Type:       typ,
		Scale:      key,
		Count:      l.get("tickCount"),
		Limit:      l.get("symbolLimit"),
		Values:     l.sp("values"),
		MinStep:    l.sp("tickMinStep"),
		FormatType: l.sp("formatType"),
		Format:     l.sp("format"),
	}

	switch {
	case strictIs(typ, gradientLegend):
		// adjust default tick count based on the gradient length
		p.Entries.CountExpr = "max(2,2*floor((" + deref(l.gradientLength()).AsString() + ")/100))"
	case strictIs(typ, discreteLegend):
	default:
		p.entryLayout = legendSymbolLayout(l)
		// The size expression reads the encoders of the symbol and label
		// marks, which upstream builds once, with the entries' data ref.
		p.symbols = legendSymbolGroups(l, config, encode, deref(member(p.entryLayout, "columns")))
		p.Entries.SizeExpr = sizeExpression(l, sc, member(p.symbols, "marks"))
	}
	return p, nil
}

// Mark returns the legend group definition. dataRef feeds the legend group
// (one datum, see Datum) and entryRef feeds the entries (labels, symbols,
// gradient bands).
func (p *LegendPlan) Mark(dataRef, entryRef Value) (mark Value, err error) {
	defer catch(&err)
	spec, l, config, encode := p.spec, p.l, p.config, p.encode
	legendEncode := or(member(encode, "legend"), objv())
	name := or(member(legendEncode, "name"), jsval.Undefined)
	interactive := member(legendEncode, "interactive")
	style := member(legendEncode, "style")

	var children Value
	switch {
	case strictIs(p.typ, gradientLegend):
		children = jsval.ArrOf(
			legendGradient(p.scale, l, member(encode, "gradient")),
			legendGradientLabels(l, member(encode, "labels"), entryRef),
		)
	case strictIs(p.typ, discreteLegend):
		children = jsval.ArrOf(
			legendGradientDiscrete(p.scale, l, member(encode, "gradient"), entryRef),
			legendGradientLabels(l, member(encode, "labels"), entryRef),
		)
	default:
		// the symbol groups face the entries' data
		facet := member(p.symbols, "from").Get("facet")
		facet.ObjValue().Set("data", entryRef)
		children = jsval.ArrOf(p.symbols)
	}
	_ = config

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
		kids = append(kids, legendTitle(spec, l, member(encode, "title"), dataRef))
	}

	m := obj(
		"role", "legend",
		"from", dataRef,
		"encode", jsval.Obj(extendEncode(l.sc, p.legendEncode(), legendEncode)),
		"marks", jsval.Arr(kids),
		"aria", l.get("aria"),
		"description", l.get("description"),
		"zindex", l.get("zindex"),
		"name", name,
		"interactive", interactive,
		"style", style,
	)
	return guideGroup(m), nil
}

func (p *LegendPlan) legendEncode() *Object {
	encode := obj("enter", objv(), "update", objv())
	l := p.l
	addEncoders(encode, []kv{
		{"orient", l.get("orient")},
		{"offset", l.get("offset")},
		{"padding", l.get("padding")},
		{"titlePadding", l.get("titlePadding")},
		{"cornerRadius", l.get("cornerRadius")},
		{"fill", l.get("fillColor")},
		{"stroke", l.get("strokeColor")},
		{"strokeWidth", l.cfg("strokeWidth")},
		{"strokeDash", l.cfg("strokeDash")},
		{"x", l.get("legendX")},
		{"y", l.get("legendY")},
		// accessibility support
		{"format", l.sp("format")},
		{"formatType", l.sp("formatType")},
	}, nil)
	return encode
}

// sizeExpression is the expression that sizes a symbol legend entry: the
// symbol's extent plus stroke, but at least the label font size.
func sizeExpression(l lookup, sc Scope, marks Value) string {
	size := deref(getChannel("size", l, marks))
	strokeWidth := deref(getChannel("strokeWidth", l, marks))
	fontSize := getEncoding("fontSize", member(member(marks, "1"), "encode"))
	if !fontSize.IsTruthy() {
		// getStyle: scope.config.style[style], then s && s[name]
		s := member(member(sc.Config(), "style"), guideLabelStyle)
		fontSize = s
		if s.IsTruthy() {
			fontSize = member(s, "fontSize")
		}
	}
	fontSize = deref(fontSize)
	return "max(ceil(sqrt(" + size.AsString() + ")+" + strokeWidth.AsString() + ")," + fontSize.AsString() + ")"
}

func getChannel(name string, l lookup, marks Value) Value {
	if s := l.sp(name); s.IsTruthy() {
		return jsval.Str(`scale("` + s.AsString() + `",datum)`)
	}
	return getEncoding(name, member(member(marks, "0"), "encode"))
}

// getEncoding reads the constant or signal a mark's encode block assigns to
// name (update takes precedence over enter); null when unset:
// `v && v.signal ? v : v ? v.value : null`.
func getEncoding(name string, encode Value) Value {
	var v Value
	if encode.IsTruthy() {
		if u := member(encode, "update"); u.IsTruthy() {
			v = member(u, name)
		}
		if !v.IsTruthy() {
			v = jsval.Undefined
			if e := member(encode, "enter"); e.IsTruthy() {
				v = member(e, name)
			}
		}
	} else {
		v = encode
	}
	switch {
	case v.IsTruthy() && member(v, "signal").IsTruthy():
		return v
	case v.IsTruthy():
		return member(v, "value")
	}
	return jsval.Null
}

// legendSymbolLayout is the layout of a symbol legend's entries. Its members
// are kept when undefined, as the object literal upstream writes keeps them:
// the mark's layout is an object property, whose expression prints each
// member, an undefined one as the name undefined (no signal is so named).
func legendSymbolLayout(l lookup) Value {
	keep := func(pairs ...any) Value {
		o := jsval.NewObject(len(pairs) / 2)
		for i := 0; i+1 < len(pairs); i += 2 {
			o.Set(pairs[i].(string), toValue(pairs[i+1]))
		}
		return jsval.Obj(o)
	}
	align := l.get("gridAlign")
	columns := l.entryColumns()
	padding := keep("row", l.get("rowPadding"), "column", l.get("columnPadding"))
	return keep(
		"align", align,
		"columns", columns,
		"center", objv("row", true, "column", false),
		"padding", padding,
	)
}
