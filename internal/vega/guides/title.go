package guides

import (
	"errors"

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
func NewTitle(spec Value, sc Scope) (*TitlePlan, error) {
	if spec.IsStr() {
		spec = objv("text", spec)
	}
	if !spec.IsObj() {
		return nil, errors.New("title: specification must be a string or an object")
	}
	return &TitlePlan{
		Datum: objv(),
		spec:  spec,
		l:     lookup{spec: spec, config: sc.Config().Get("title")},
	}, nil
}

// Mark returns the title group definition; dataRef feeds the group and its
// text marks.
func (p *TitlePlan) Mark(dataRef Value) Value {
	spec, l := p.spec, p.l
	encode := spec.Get("encode")
	userEncode := encode.Get("group")
	name := userEncode.Get("name")

	children := []Value{buildTitle(spec, l, titleEncode(spec), dataRef)}
	if spec.Get("subtitle").IsTruthy() {
		children = append(children, buildSubtitle(spec, l, encode.Get("subtitle"), dataRef))
	}

	m := obj(
		"role", "title",
		"from", dataRef,
		"encode", jsval.Obj(titleGroupEncode(l, userEncode)),
		"marks", jsval.Arr(children),
		"aria", l.get("aria"),
		"description", l.get("description"),
		"zindex", l.get("zindex"),
		"name", or(name, jsval.Undefined),
		"interactive", userEncode.Get("interactive"),
		"style", userEncode.Get("style"),
	)
	return guideGroup(m)
}

// titleEncode provides backwards compatibility for the deprecated top-level
// title encode block: unless encode.title exists, the whole encode block
// (plus name, interactive and style of the title) customizes the text mark.
func titleEncode(spec Value) Value {
	encode := spec.Get("encode")
	if t := encode.Get("title"); t.IsTruthy() {
		return t
	}
	o := obj("name", spec.Get("name"), "interactive", spec.Get("interactive"), "style", spec.Get("style"))
	return jsval.Obj(extend(o, encode))
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
	return extendEncode(encode, userEncode)
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
	return guideMark(obj("type", "text", "role", role, "style", style, "from", dataRef, "encode", encode), userEncode)
}

func buildTitle(spec Value, l lookup, userEncode, dataRef Value) Value {
	return titleText(spec.Get("text"), l, userEncode, dataRef, "title-text", groupTitleStyle, []kv{
		{"fill", l.get("color")},
		{"font", l.get("font")},
		{"fontSize", l.get("fontSize")},
		{"fontStyle", l.get("fontStyle")},
		{"fontWeight", l.get("fontWeight")},
		{"lineHeight", l.get("lineHeight")},
	})
}

func buildSubtitle(spec Value, l lookup, userEncode, dataRef Value) Value {
	return titleText(spec.Get("subtitle"), l, userEncode, dataRef, "title-subtitle", groupSubtitleStyle, []kv{
		{"fill", l.get("subtitleColor")},
		{"font", l.get("subtitleFont")},
		{"fontSize", l.get("subtitleFontSize")},
		{"fontStyle", l.get("subtitleFontStyle")},
		{"fontWeight", l.get("subtitleFontWeight")},
		{"lineHeight", l.get("subtitleLineHeight")},
	})
}
