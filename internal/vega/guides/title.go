package guides

import (
	"github.com/mgilbir/aster/internal/jsval"
)

const angleExpr = `item.orient==="left"?-90:item.orient==="right"?90:0`

// TitlePlan is a parsed title specification.
type TitlePlan struct {
	// Datum is the (empty) single-element data source of the title group.
	Datum Value

	spec Value
	l    lookup
}

// NewTitle prepares a title; a bare string is shorthand for {text: string}.
// Any other value is read as upstream reads it: a number or an array has no
// text and makes an empty title.
func NewTitle(spec Value, sc Scope) (p *TitlePlan, err error) {
	defer catch(&err)
	if spec.IsStr() {
		spec = objv("text", spec)
	}
	return &TitlePlan{
		Datum: objv(),
		spec:  spec,
		l:     lookup{spec: spec, config: member(sc.Config(), "title"), sc: sc},
	}, nil
}

// Mark returns the title group definition; dataRef feeds the group and its
// text marks.
func (p *TitlePlan) Mark(dataRef Value) (mark Value, err error) {
	defer catch(&err)
	l := p.l
	encode := or(l.sp("encode"), objv())
	userEncode := or(member(encode, "group"), objv())
	name := or(member(userEncode, "name"), jsval.Undefined)
	interactive := member(userEncode, "interactive")
	style := member(userEncode, "style")

	children := []Value{buildTitle(l, titleEncode(l), dataRef)}
	if l.sp("subtitle").IsTruthy() {
		children = append(children, buildSubtitle(l, member(encode, "subtitle"), dataRef))
	}

	m := obj(
		"role", "title",
		"from", dataRef,
		"encode", jsval.Obj(titleGroupEncode(l, userEncode)),
		"marks", jsval.Arr(children),
		"aria", l.get("aria"),
		"description", l.get("description"),
		"zindex", l.get("zindex"),
		"name", name,
		"interactive", interactive,
		"style", style,
	)
	return guideGroup(m), nil
}

// titleEncode provides backwards compatibility for the deprecated top-level
// title encode block: unless encode.title exists, the whole encode block
// (plus name, interactive and style of the title) customizes the text mark.
func titleEncode(l lookup) Value {
	encode := l.sp("encode")
	if encode.IsTruthy() {
		if t := member(encode, "title"); t.IsTruthy() {
			return t
		}
	}
	o := obj("name", l.sp("name"), "interactive", l.sp("interactive"), "style", l.sp("style"))
	return jsval.Obj(extendWork(l.sc, o, encode))
}

func titleGroupEncode(l lookup, userEncode Value) *Object {
	encode := obj("enter", objv(), "update", objv())
	addEncoders(encode, []kv{
		{"orient", l.get("orient")},
		{"anchor", l.get("anchor")},
		{"align", objv("signal", alignExpr)},
		{"angle", objv("signal", angleExpr)},
		{"limit", l.get("limit")},
		{"frame", l.get("frame")},
		{"offset", or(l.get("offset"), jsval.Int(0))},
		{"padding", l.get("subtitlePadding")},
	}, nil)
	return extendEncode(l.sc, encode, userEncode)
}

// titleText builds the shared shape of the title and subtitle text marks.
func titleText(text Value, l lookup, userEncode, dataRef Value, role, style string, props []kv) Value {
	encode := obj(
		"enter", objv("opacity", zero()),
		"update", objv("opacity", objv("value", 1)),
		"exit", objv("opacity", zero()),
	)
	enter := append([]kv{
		{"text", text},
		{"align", objv("signal", "item.mark.group.align")},
		{"angle", objv("signal", "item.mark.group.angle")},
		{"limit", objv("signal", "item.mark.group.limit")},
		{"baseline", jsval.Str("top")},
		{"dx", l.get("dx")},
		{"dy", l.get("dy")},
	}, props...)
	addEncoders(encode, enter, []kv{
		{"align", l.get("align")},
		{"angle", l.get("angle")},
		{"baseline", l.get("baseline")},
	})
	return guideMark(l, obj("type", "text", "role", role, "style", style, "from", dataRef, "encode", encode), userEncode)
}

func buildTitle(l lookup, userEncode, dataRef Value) Value {
	return titleText(l.sp("text"), l, userEncode, dataRef, "title-text", groupTitleStyle, []kv{
		{"fill", l.get("color")},
		{"font", l.get("font")},
		{"fontSize", l.get("fontSize")},
		{"fontStyle", l.get("fontStyle")},
		{"fontWeight", l.get("fontWeight")},
		{"lineHeight", l.get("lineHeight")},
	})
}

func buildSubtitle(l lookup, userEncode, dataRef Value) Value {
	return titleText(l.sp("subtitle"), l, userEncode, dataRef, "title-subtitle", groupSubtitleStyle, []kv{
		{"fill", l.get("subtitleColor")},
		{"font", l.get("subtitleFont")},
		{"fontSize", l.get("subtitleFontSize")},
		{"fontStyle", l.get("subtitleFontStyle")},
		{"fontWeight", l.get("subtitleFontWeight")},
		{"lineHeight", l.get("subtitleLineHeight")},
	})
}
