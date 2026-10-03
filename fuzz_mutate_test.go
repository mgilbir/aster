package aster_test

import (
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// The differential fuzzer's specification mutator. It edits a parsed spec
// (insertion order preserved) with structure-aware mutations: mark and scale
// types, encoding channels and their properties, transforms and their
// parameters, axis and legend properties, config, data values. Mutants stay
// mostly valid so that they get past parsing and reach the engine.

// slot is a settable position in a JSON tree.
type slot struct {
	obj  *jsval.Object
	key  string
	arr  jsval.Value // array holding the value, when obj is nil
	idx  int
	path []string
}

func (s slot) get() jsval.Value {
	if s.obj != nil {
		return s.obj.Lookup(s.key)
	}
	return s.arr.Index(s.idx)
}

func (s slot) set(v jsval.Value) {
	if s.obj != nil {
		s.obj.Set(s.key, v)
		return
	}
	s.arr.Items()[s.idx] = v
}

func (s slot) name() string {
	if s.obj != nil {
		return s.key
	}
	return ""
}

func (s slot) pathString() string { return strings.Join(s.path, "/") }

// collect lists every slot below root (the root itself is not a slot).
func collect(root jsval.Value) []slot {
	var out []slot
	var walk func(v jsval.Value, path []string)
	walk = func(v jsval.Value, path []string) {
		switch {
		case v.IsObj():
			o := v.ObjValue()
			for i := 0; i < o.Len(); i++ {
				k := o.KeyAt(i)
				p := append(append([]string(nil), path...), k)
				out = append(out, slot{obj: o, key: k, path: p})
				walk(o.ValueAt(i), p)
			}
		case v.IsArr():
			for i := 0; i < v.Len(); i++ {
				p := append(append([]string(nil), path...), strconv.Itoa(i))
				out = append(out, slot{arr: v, idx: i, path: p})
				walk(v.Index(i), p)
			}
		}
	}
	walk(root, nil)
	return out
}

// mut is one mutation session.
type mut struct {
	rng    *rand.Rand
	lite   bool
	root   jsval.Value
	fields []string
	log    []string
}

func (m *mut) pick(xs []string) string { return xs[m.rng.Intn(len(xs))] }

func (m *mut) chance(p float64) bool { return m.rng.Float64() < p }

func parseSnippet(s string) jsval.Value {
	v, err := jsval.ParseJSONString(s)
	if err != nil {
		panic("bad snippet " + s + ": " + err.Error())
	}
	return v
}

// expand fills the placeholders in a JSON snippet: $F a field name, $N a small
// number, $S a string.
func (m *mut) expand(s string) jsval.Value {
	for strings.Contains(s, "$F") {
		s = strings.Replace(s, "$F", m.field(), 1)
	}
	// A negated placeholder ("-$N") must stay a JSON number whatever the
	// number drawn: "--1" is not one.
	for strings.Contains(s, "$N") {
		n := m.number()
		if i := strings.Index(s, "$N"); i > 0 && s[i-1] == '-' && strings.HasPrefix(n, "-") {
			s, n = s[:i-1]+s[i:], n[1:]
		}
		s = strings.Replace(s, "$N", n, 1)
	}
	for strings.Contains(s, "$S") {
		q := string(jsval.AppendJSON(nil, jsval.Str(m.pick(trickyStrings))))
		s = strings.Replace(s, "$S", q[1:len(q)-1], 1)
	}
	return parseSnippet(s)
}

func (m *mut) field() string {
	if len(m.fields) == 0 || m.chance(0.05) {
		return "nofield"
	}
	return m.fields[m.rng.Intn(len(m.fields))]
}

func (m *mut) number() string {
	return m.pick([]string{"0", "1", "-1", "2", "3", "5", "10", "100", "0.5", "0.1", "-0.5", "1e6", "1e-6", "1e15", "-1e15", "1e300", "1e-300", "12345.6789", "7", "4", "20", "50", "0.25", "365"})
}

var trickyStrings = []string{
	"", " ", "NaN", "Infinity", "-Infinity", "null", "undefined", "true", "0", "-0", "1e3", "0x10", " 12 ",
	"日本語のテキスト", "😀👍🏽", "שלום עולם", "العربية", "é́", "a\u0000b", "<b>&amp;\"'</b>", "line1\nline2", "tab\there",
	"2020-01-01", "2020-13-45", "1e999", "constructor", "__proto__", "toString", "\u202e", "a very long string that goes on and on and on and on and on and on and on and on and on and on and on and on",
}

var hugeNumbers = []string{"1e308", "-1e308", "5e-324", "1e-320", "1e-300", "9007199254740993", "-9007199254740993", "1.7976931348623157e308", "4.9e-324", "0", "-0", "123456789012345678901234567890", "0.1", "1e21", "1e-7"}

var markTypes = []string{"bar", "line", "area", "point", "circle", "square", "tick", "rule", "text", "rect", "arc", "trail", "errorbar", "errorband", "boxplot"}

var vegaMarkTypes = []string{"rect", "symbol", "arc", "area", "line", "path", "rule", "text", "trail", "shape"}

var channels = []string{"x", "y", "x2", "y2", "color", "fill", "stroke", "opacity", "fillOpacity", "strokeOpacity", "size", "shape", "strokeDash", "strokeWidth", "detail", "order", "text", "tooltip", "row", "column", "facet", "theta", "theta2", "radius", "radius2", "xOffset", "yOffset", "angle", "href", "key", "description", "latitude", "longitude", "xError", "yError"}

// Property pools by context. Each entry is one JSON member: `"key": value`.
var poolDef = []string{
	`"type":"quantitative"`, `"type":"nominal"`, `"type":"ordinal"`, `"type":"temporal"`, `"type":"geojson"`,
	`"aggregate":"count"`, `"aggregate":"sum"`, `"aggregate":"mean"`, `"aggregate":"median"`, `"aggregate":"min"`, `"aggregate":"max"`,
	`"aggregate":"distinct"`, `"aggregate":"variance"`, `"aggregate":"stdev"`, `"aggregate":"q1"`, `"aggregate":"q3"`, `"aggregate":"ci0"`, `"aggregate":"ci1"`, `"aggregate":"valid"`, `"aggregate":"missing"`, `"aggregate":"stderr"`, `"aggregate":"product"`, `"aggregate":"variancep"`, `"aggregate":"stdevp"`,
	`"bin":true`, `"bin":{"maxbins":$N}`, `"bin":{"step":$N}`, `"bin":{"extent":[0,$N]}`, `"bin":{"nice":false}`, `"bin":{"base":2}`, `"bin":{"anchor":$N}`, `"bin":{"steps":[1,5,10]}`, `"bin":{"minstep":$N}`, `"bin":{"divide":[3,4]}`, `"bin":"binned"`, `"bin":null`,
	`"timeUnit":"year"`, `"timeUnit":"quarter"`, `"timeUnit":"month"`, `"timeUnit":"day"`, `"timeUnit":"date"`, `"timeUnit":"hours"`, `"timeUnit":"minutes"`, `"timeUnit":"seconds"`, `"timeUnit":"milliseconds"`,
	`"timeUnit":"yearmonth"`, `"timeUnit":"yearmonthdate"`, `"timeUnit":"yearweek"`, `"timeUnit":"monthdate"`, `"timeUnit":"hoursminutes"`, `"timeUnit":"utcyear"`, `"timeUnit":"utcmonth"`, `"timeUnit":{"unit":"yearmonth","step":$N}`, `"timeUnit":{"unit":"month","utc":true}`, `"timeUnit":"week"`, `"timeUnit":"dayhours"`,
	`"sort":"ascending"`, `"sort":"descending"`, `"sort":"-x"`, `"sort":"y"`, `"sort":null`, `"sort":{"op":"mean","field":"$F"}`, `"sort":{"op":"count"}`, `"sort":{"field":"$F","order":"descending"}`, `"sort":["a","b","c"]`, `"sort":"-color"`, `"sort":{"encoding":"x"}`, `"sort":{"op":"sum","field":"$F","order":"ascending"}`,
	`"stack":null`, `"stack":"zero"`, `"stack":"center"`, `"stack":"normalize"`, `"stack":true`, `"stack":false`,
	`"format":".2f"`, `"format":"d"`, `"format":"%Y-%m"`, `"format":",.0%"`, `"format":"$S"`, `"format":"s"`, `"format":".3e"`, `"format":"+.1f"`, `"format":"~r"`, `"format":"%b %d"`, `"format":"( "`,
	`"title":null`, `"title":"$S"`, `"title":["multi","line"]`, `"title":""`,
	`"band":$N`, `"band":0.5`, `"bandPosition":$N`, `"value":$N`, `"value":"red"`, `"value":"$S"`, `"value":null`, `"datum":$N`, `"datum":"$S"`, `"datum":{"year":2000}`,
	`"condition":{"param":"pt","value":"red"}`, `"condition":{"test":"datum.$F > 3","value":"red"}`, `"condition":{"test":"isValid(datum.$F)","value":0.2}`, `"condition":[{"test":"datum.$F == 1","value":"blue"},{"test":"datum.$F == 2","value":"green"}]`,
	`"scale":null`, `"scale":{}`, `"axis":null`, `"axis":{}`, `"legend":null`, `"legend":{}`,
	`"field":"$F"`, `"field":"$S"`, `"field":"a.b"`, `"field":"a\\.b"`, `"field":"$F[0]"`,
	`"impute":{"value":0}`, `"impute":{"method":"mean"}`, `"impute":{"keyvals":[1,2,3]}`, `"impute":{"frame":[-1,1],"method":"median"}`,
	`"header":{"labelAngle":45}`, `"header":{"title":null}`, `"header":{"labelOrient":"bottom"}`, `"spacing":$N`, `"align":"each"`, `"center":true`,
	`"bandPosition":0.5`, `"labelExpr":"datum.label + '!'"`,
}

var poolScale = []string{
	`"type":"linear"`, `"type":"log"`, `"type":"pow"`, `"type":"sqrt"`, `"type":"symlog"`, `"type":"time"`, `"type":"utc"`, `"type":"band"`, `"type":"point"`, `"type":"ordinal"`, `"type":"quantile"`, `"type":"quantize"`, `"type":"threshold"`, `"type":"bin-ordinal"`, `"type":"sequential"`,
	`"zero":true`, `"zero":false`, `"nice":true`, `"nice":false`, `"nice":$N`, `"nice":"day"`, `"nice":"month"`, `"nice":{"interval":"week","step":2}`,
	`"domain":[0,$N]`, `"domain":[$N,0]`, `"domain":[-$N,$N]`, `"domain":["a","b","c"]`, `"domain":"unaggregated"`, `"domain":[]`, `"domain":[1,1]`, `"domain":[null,5]`, `"domain":["2000-01-01","2010-01-01"]`, `"domain":[{"year":2000},{"year":2010}]`, `"domain":{"unionWith":[0,100]}`, `"domain":[5,1,3]`, `"domain":[1e300,1e308]`,
	`"domainMin":$N`, `"domainMax":$N`, `"domainMid":$N`, `"domainRaw":[0,1]`,
	`"range":[0,$N]`, `"range":[$N,0]`, `"range":["red","blue"]`, `"range":["#000","#fff","#f00"]`, `"range":"category"`, `"range":"ramp"`, `"range":"heatmap"`, `"range":"symbol"`, `"range":[1,1]`, `"range":[10]`, `"range":[]`, `"range":{"step":$N}`, `"range":[0,1e308]`,
	`"rangeMin":$N`, `"rangeMax":$N`, `"reverse":true`, `"reverse":false`, `"clamp":true`, `"round":true`, `"round":false`,
	`"padding":$N`, `"paddingInner":0.5`, `"paddingOuter":0.5`, `"paddingInner":1`, `"align":0`, `"align":1`, `"align":0.5`,
	`"base":2`, `"base":10`, `"base":0`, `"base":1`, `"base":-1`, `"exponent":0.5`, `"exponent":2`, `"exponent":0`, `"exponent":-1`, `"constant":0`, `"constant":10`, `"constant":-1`,
	`"interpolate":"hcl"`, `"interpolate":"lab"`, `"interpolate":"hsl"`, `"interpolate":"rgb"`, `"interpolate":{"type":"rgb","gamma":2.2}`, `"interpolate":"cubehelix"`, `"interpolate":"hcl-long"`, `"interpolate":"cubehelix-long"`,
	`"scheme":"viridis"`, `"scheme":"blues"`, `"scheme":"category20"`, `"scheme":"tableau10"`, `"scheme":"rainbow"`, `"scheme":"nosuchscheme"`, `"scheme":{"name":"plasma","count":$N}`, `"scheme":{"name":"blues","extent":[0.2,0.8]}`, `"scheme":"sinebow"`, `"scheme":"spectral"`, `"scheme":"turbo"`, `"scheme":"cividis"`,
	`"bins":[0,10,20]`, `"bins":{"step":$N}`, `"zero":$N`,
}

var poolAxis = []string{
	`"orient":"top"`, `"orient":"bottom"`, `"orient":"left"`, `"orient":"right"`, `"grid":true`, `"grid":false`, `"ticks":false`, `"labels":false`, `"domain":false`, `"tickCount":$N`, `"tickCount":0`, `"tickCount":{"interval":"month","step":3}`, `"tickCount":"week"`, `"tickMinStep":$N`, `"tickSize":$N`, `"tickWidth":$N`, `"tickBand":"extent"`, `"tickBand":"center"`,
	`"labelAngle":45`, `"labelAngle":-90`, `"labelAngle":720`, `"labelAngle":0`, `"labelOverlap":true`, `"labelOverlap":false`, `"labelOverlap":"parity"`, `"labelOverlap":"greedy"`, `"labelFlush":true`, `"labelFlush":false`, `"labelFlush":$N`, `"labelFlushOffset":$N`, `"labelBound":true`, `"labelBound":$N`, `"labelPadding":$N`, `"labelLimit":$N`, `"labelLimit":0`, `"labelSeparation":$N`, `"labelAlign":"left"`, `"labelBaseline":"top"`, `"labelFontSize":$N`, `"labelFontWeight":"bold"`, `"labelFont":"monospace"`, `"labelColor":"red"`, `"labelOpacity":0.5`, `"labelLineHeight":$N`, `"labelExpr":"datum.value + '%'"`, `"labelExpr":"datum.label"`, `"labelExpr":"upper(datum.label)"`, `"labelExpr":"datum.index"`,
	`"format":".1f"`, `"format":"%Y"`, `"format":"s"`, `"format":"$S"`, `"format":"d"`, `"formatType":"number"`, `"formatType":"time"`, `"title":null`, `"title":"$S"`, `"title":["a","b"]`, `"titleAngle":45`, `"titleAngle":90`, `"titleAnchor":"start"`, `"titleAnchor":"end"`, `"titlePadding":$N`, `"titleX":$N`, `"titleY":$N`, `"titleLimit":$N`, `"titleFontSize":$N`, `"titleAlign":"left"`, `"titleBaseline":"bottom"`, `"titleOpacity":0.3`, `"titleColor":"blue"`, `"titleLineHeight":$N`,
	`"values":[0,1,2]`, `"values":[]`, `"values":[1e300]`, `"values":["a","b"]`, `"values":[{"year":2000}]`, `"offset":$N`, `"position":$N`, `"minExtent":$N`, `"maxExtent":$N`, `"zindex":0`, `"zindex":1`, `"translate":$N`,
	`"domainColor":"red"`, `"domainWidth":$N`, `"domainDash":[2,2]`, `"domainOpacity":0.4`, `"gridColor":"red"`, `"gridDash":[3,3]`, `"gridWidth":$N`, `"gridOpacity":0.5`, `"gridCap":"round"`, `"tickColor":"blue"`, `"tickDash":[1,1]`, `"tickCap":"round"`, `"tickRound":false`, `"tickExtra":true`, `"tickOffset":$N`, `"bandPosition":$N`, `"description":"$S"`, `"aria":false`, `"style":"x"`,
}

var poolLegend = []string{
	`"orient":"left"`, `"orient":"right"`, `"orient":"top"`, `"orient":"bottom"`, `"orient":"top-left"`, `"orient":"top-right"`, `"orient":"bottom-left"`, `"orient":"bottom-right"`, `"orient":"none"`, `"orient":"nosuch"`,
	`"direction":"horizontal"`, `"direction":"vertical"`, `"type":"symbol"`, `"type":"gradient"`, `"columns":$N`, `"columns":0`, `"columnPadding":$N`, `"rowPadding":$N`, `"symbolType":"square"`, `"symbolType":"triangle-up"`, `"symbolType":"stroke"`, `"symbolType":"M0,0L1,1"`, `"symbolType":"cross"`, `"symbolSize":$N`, `"symbolSize":0`, `"symbolStrokeWidth":$N`, `"symbolOffset":$N`, `"symbolFillColor":"red"`, `"symbolStrokeColor":"blue"`, `"symbolOpacity":0.4`, `"symbolDash":[2,2]`,
	`"gradientLength":$N`, `"gradientThickness":$N`, `"gradientStrokeWidth":$N`, `"gradientOpacity":0.3`, `"title":null`, `"title":"$S"`, `"title":["a","b"]`, `"titleOrient":"left"`, `"titleOrient":"top"`, `"titleAnchor":"middle"`, `"titlePadding":$N`, `"titleLimit":$N`, `"titleAlign":"center"`, `"titleFontSize":$N`, `"titleFontWeight":"lighter"`, `"titleOpacity":0.5`,
	`"values":[1,2,3]`, `"values":[]`, `"values":["a"]`, `"format":".1f"`, `"format":"s"`, `"format":"%Y"`, `"format":"$S"`, `"formatType":"number"`, `"tickCount":$N`, `"tickCount":0`, `"tickMinStep":$N`, `"offset":$N`, `"padding":$N`, `"margin":$N`, `"cornerRadius":$N`, `"fillColor":"#eee"`, `"strokeColor":"red"`, `"strokeWidth":$N`, `"labelLimit":$N`, `"labelOffset":$N`, `"labelOverlap":"greedy"`, `"labelOverlap":true`, `"labelSeparation":$N`, `"labelAlign":"left"`, `"labelBaseline":"top"`, `"labelFontSize":$N`, `"labelColor":"red"`, `"labelOpacity":0.5`, `"labelExpr":"datum.label + '?'"`, `"labelExpr":"datum.value"`,
	`"clipHeight":$N`, `"gridAlign":"none"`, `"gridAlign":"each"`, `"zindex":$N`, `"legendX":$N`, `"legendY":$N`, `"fillOpacity":0.5`, `"tickCount":"month"`, `"aria":false`, `"description":"$S"`, `"symbolLimit":$N`, `"symbolLimit":0`,
}

var poolMark = []string{
	`"filled":true`, `"filled":false`, `"opacity":$N`, `"opacity":0.3`, `"fillOpacity":0.3`, `"strokeOpacity":0.3`, `"size":$N`, `"size":0`, `"strokeWidth":$N`, `"strokeWidth":0`, `"strokeDash":[4,2]`, `"strokeCap":"round"`, `"strokeJoin":"bevel"`, `"strokeMiterLimit":$N`, `"stroke":"red"`, `"fill":"blue"`, `"color":"green"`, `"color":{"x1":0,"y1":0,"x2":1,"y2":1,"gradient":"linear","stops":[{"offset":0,"color":"red"},{"offset":1,"color":"blue"}]}`,
	`"interpolate":"basis"`, `"interpolate":"cardinal"`, `"interpolate":"catmull-rom"`, `"interpolate":"monotone"`, `"interpolate":"natural"`, `"interpolate":"step"`, `"interpolate":"step-before"`, `"interpolate":"step-after"`, `"interpolate":"linear-closed"`, `"interpolate":"bundle"`, `"interpolate":"basis-open"`, `"interpolate":"basis-closed"`, `"interpolate":"cardinal-open"`, `"interpolate":"cardinal-closed"`, `"interpolate":"monotone-x"`, `"interpolate":"monotone-y"`, `"interpolate":"nosuch"`, `"tension":$N`, `"tension":0.5`,
	`"orient":"horizontal"`, `"orient":"vertical"`, `"cornerRadius":$N`, `"cornerRadiusTopLeft":$N`, `"cornerRadiusEnd":$N`, `"innerRadius":$N`, `"outerRadius":$N`, `"padAngle":0.1`, `"radius":$N`, `"radius2":$N`, `"theta":$N`, `"theta2":$N`, `"radiusOffset":$N`, `"thetaOffset":$N`, `"startAngle":$N`,
	`"tooltip":true`, `"tooltip":{"content":"data"}`, `"tooltip":{"content":"encoding"}`, `"invalid":"show"`, `"invalid":"filter"`, `"invalid":null`, `"clip":true`, `"point":true`, `"point":{"filled":false,"fill":"white"}`, `"point":"transparent"`, `"line":true`, `"line":{"color":"red"}`, `"area":true`,
	`"align":"left"`, `"align":"right"`, `"baseline":"top"`, `"baseline":"middle"`, `"baseline":"line-bottom"`, `"angle":$N`, `"angle":45`, `"dx":$N`, `"dy":$N`, `"limit":$N`, `"limit":0`, `"ellipsis":"~"`, `"ellipsis":""`, `"lineBreak":"\n"`, `"lineBreak":"$S"`, `"lineHeight":$N`, `"font":"serif"`, `"font":"monospace"`, `"fontSize":$N`, `"fontSize":0`, `"fontStyle":"italic"`, `"fontWeight":"bold"`, `"fontWeight":900`, `"text":"$S"`, `"text":["a","b"]`, `"shape":"triangle"`, `"shape":"diamond"`, `"shape":"M-1,-1L1,1"`, `"shape":"wedge"`, `"shape":"arrow"`, `"shape":"triangle-left"`, `"shape":"stroke"`,
	`"thickness":$N`, `"width":$N`, `"height":$N`, `"width":{"band":0.5}`, `"height":{"band":0.3}`, `"minBandSize":$N`, `"discreteBandSize":$N`, `"discreteBandSize":{"band":0.5}`, `"binSpacing":$N`, `"binSpacing":0`, `"blend":"multiply"`, `"cursor":"pointer"`, `"aria":false`, `"description":"$S"`, `"href":"http://x/$S"`, `"x":$N`, `"y":$N`, `"x2":$N`, `"y2":$N`, `"xOffset":$N`, `"yOffset":$N`, `"x":"width"`, `"y":"height"`, `"x2":"width"`,
	`"extent":"ci"`, `"extent":"stderr"`, `"extent":"stdev"`, `"extent":"iqr"`, `"extent":"min-max"`, `"extent":$N`, `"box":true`, `"box":{"fill":"red"}`, `"median":{"color":"red"}`, `"rule":true`, `"ticks":true`, `"outliers":false`, `"outliers":{"size":$N}`, `"band":{"opacity":0.5}`, `"borders":true`, `"borders":{"strokeDash":[2,2]}`, `"style":"foo"`, `"timeUnitBandSize":$N`, `"timeUnitBandPosition":0.5`,
}

var poolTop = []string{
	`"width":$N`, `"width":0`, `"width":-1`, `"width":1e6`, `"width":"container"`, `"width":{"step":$N}`, `"width":{"step":0}`, `"height":$N`, `"height":0`, `"height":-5`, `"height":"container"`, `"height":{"step":$N}`, `"padding":$N`, `"padding":0`, `"padding":{"left":$N,"top":$N,"right":0,"bottom":0}`, `"padding":-3`,
	`"autosize":"fit"`, `"autosize":"pad"`, `"autosize":"none"`, `"autosize":"fit-x"`, `"autosize":"fit-y"`, `"autosize":{"type":"fit","contains":"padding"}`, `"autosize":{"type":"pad","resize":true}`,
	`"background":"#eee"`, `"background":"$S"`, `"background":null`, `"background":"rgba(0,0,0,0.5)"`, `"background":"hsl(120,50%,50%)"`, `"description":"$S"`, `"name":"$S"`,
	`"title":"$S"`, `"title":{"text":"$S","subtitle":"$S"}`, `"title":{"text":["a","b"],"anchor":"start","frame":"group","orient":"bottom","offset":$N}`, `"title":{"text":"T","anchor":"end","fontSize":$N,"color":"red","dx":$N,"dy":$N,"align":"right","baseline":"bottom","angle":$N,"limit":$N}`, `"title":{"text":"T","subtitle":["x","y"],"subtitleFontSize":$N,"subtitleColor":"red","subtitlePadding":$N,"lineHeight":$N}`, `"title":null`, `"title":""`,
	`"view":{"stroke":"red"}`, `"view":{"fill":"#eee","strokeWidth":$N}`, `"view":{"continuousWidth":$N,"continuousHeight":$N}`, `"view":{"discreteWidth":$N}`, `"view":{"cornerRadius":$N}`, `"view":{"clip":true}`,
	`"resolve":{"scale":{"x":"independent","y":"independent","color":"independent"}}`, `"resolve":{"axis":{"x":"independent"}}`, `"resolve":{"legend":{"color":"independent"}}`, `"resolve":{"scale":{"color":"independent","size":"independent","shape":"independent","opacity":"independent"}}`,
	`"spacing":$N`, `"spacing":{"row":$N,"column":$N}`, `"bounds":"flush"`, `"bounds":"full"`, `"center":true`, `"center":{"row":true}`, `"columns":$N`, `"columns":1`, `"align":"each"`, `"align":"all"`, `"align":"none"`, `"align":{"row":"none","column":"each"}`,
	`"usermeta":{"a":1}`, `"datasets":{"x":[{"a":1}]}`,
	`"config":{"axis":{"grid":false,"labelFontSize":$N}}`, `"config":{"legend":{"orient":"bottom"}}`, `"config":{"view":{"stroke":null}}`, `"config":{"mark":{"color":"red","opacity":0.5}}`, `"config":{"range":{"category":["red","green","blue"],"ramp":["white","black"],"heatmap":["white","red"],"ordinal":["red","green"]}}`, `"config":{"font":"monospace","fontSize":$N}`, `"config":{"background":"#ffe"}`, `"config":{"numberFormat":".2f","timeFormat":"%Y"}`, `"config":{"axisX":{"labelAngle":30},"axisY":{"tickCount":$N}}`, `"config":{"axisBand":{"grid":true},"axisQuantitative":{"tickCount":3},"axisTemporal":{"format":"%y"}}`,
	`"config":{"title":{"anchor":"start","fontSize":$N}}`, `"config":{"scale":{"bandPaddingInner":0.5,"pointPadding":0.3,"rectBandPaddingInner":0.1,"xReverse":true,"continuousPadding":$N,"maxBandSize":$N,"minBandSize":$N,"useUnaggregatedDomain":true,"zero":false,"round":false,"barBandPaddingInner":0.3}}`, `"config":{"bar":{"binSpacing":$N,"continuousBandSize":$N,"discreteBandSize":$N}}`, `"config":{"line":{"strokeWidth":$N,"point":true}}`, `"config":{"invalidValues":"show"}`, `"config":{"invalidValues":null}`, `"config":{"customFormatTypes":true}`, `"config":{"timeUnit":{"utc":true}}`, `"config":{"text":{"fontSize":$N}}`, `"config":{"style":{"cell":{"stroke":"red"}}}`, `"config":{"header":{"labelFontSize":$N,"titleFontSize":$N,"labelOrient":"bottom"}}`, `"config":{"facet":{"spacing":$N,"columns":$N}}`, `"config":{"concat":{"spacing":$N,"columns":$N}}`, `"config":{"legend":{"symbolSize":$N,"labelLimit":$N,"titleLimit":$N,"gradientLength":$N}}`, `"config":{"padding":$N}`, `"config":{"countTitle":"N"}`, `"config":{"fieldTitle":"functional"}`, `"config":{"fieldTitle":"plain"}`, `"config":{"lineBreak":"\n"}`, `"config":{"tick":{"thickness":$N,"bandSize":$N}}`, `"config":{"point":{"size":$N,"filled":true}}`, `"config":{"rect":{"minBandSize":$N}}`, `"config":{"boxplot":{"extent":$N,"size":$N}}`, `"config":{"errorbar":{"ticks":true,"extent":"stdev"}}`, `"config":{"projection":{"type":"albers"}}`, `"config":{"mark":{"invalid":"filter"}}`, `"config":{"locale":{"number":{"decimal":",","thousands":".","grouping":[3],"currency":["",""]},"time":{"dateTime":"%x, %X","date":"%d/%m/%Y","time":"%-I:%M:%S %p","periods":["AM","PM"],"days":["a","b","c","d","e","f","g"],"shortDays":["a","b","c","d","e","f","g"],"months":["1","2","3","4","5","6","7","8","9","10","11","12"],"shortMonths":["1","2","3","4","5","6","7","8","9","10","11","12"]}}}`,
	`"params":[{"name":"pt","select":{"type":"point","on":"click"}}]`, `"params":[{"name":"pt","select":"point"},{"name":"iv","select":{"type":"interval","encodings":["x"]}}]`, `"params":[{"name":"pt","select":{"type":"point","fields":["$F"]},"bind":"legend"}]`,
	`"params":[{"name":"pt","value":$N,"bind":{"input":"range","min":0,"max":100}}]`, `"params":[{"name":"pt","select":{"type":"interval"}}]`, `"params":[{"name":"pt","select":"interval","bind":"scales"}]`, `"params":[{"name":"pt","value":"a","bind":{"input":"select","options":["a","b"]}}]`,
	`"projection":{"type":"mercator"}`, `"projection":{"type":"albersUsa","scale":$N}`, `"projection":{"type":"equalEarth","center":[0,0],"rotate":[0,0,0]}`, `"projection":{"type":"orthographic","clipAngle":90}`,
}

// poolTransformOps are whole VL transforms to append.
var poolVLTransform = []string{
	`{"filter":"datum.$F > $N"}`, `{"filter":"datum.$F != null"}`, `{"filter":"isValid(datum.$F)"}`, `{"filter":"isNaN(datum.$F)"}`, `{"filter":{"field":"$F","range":[0,$N]}}`, `{"filter":{"field":"$F","oneOf":[1,2,3,"a"]}}`, `{"filter":{"field":"$F","lt":$N}}`, `{"filter":{"field":"$F","gte":$N}}`, `{"filter":{"field":"$F","equal":"$S"}}`, `{"filter":{"field":"$F","valid":true}}`, `{"filter":{"timeUnit":"year","field":"$F","range":[2000,2005]}}`, `{"filter":{"not":{"field":"$F","equal":1}}}`, `{"filter":{"and":[{"field":"$F","gt":0},{"field":"$F","lt":100}]}}`, `{"filter":{"or":["datum.$F>1",{"field":"$F","lt":0}]}}`, `{"filter":"false"}`, `{"filter":"true"}`, `{"filter":"datum.$F"}`,
	`{"calculate":"datum.$F * 2","as":"c1"}`, `{"calculate":"log(datum.$F)","as":"c1"}`, `{"calculate":"datum.$F + ''","as":"c1"}`, `{"calculate":"null","as":"c1"}`, `{"calculate":"[1,2,3]","as":"c1"}`, `{"calculate":"{a:1}","as":"c1"}`, `{"calculate":"random()","as":"c1"}`, `{"calculate":"format(datum.$F, '.2f')","as":"c1"}`, `{"calculate":"timeFormat(datum.$F, '%Y')","as":"c1"}`, `{"calculate":"datum.$F / 0","as":"c1"}`, `{"calculate":"sqrt(-1)","as":"c1"}`, `{"calculate":"datum.$F * 1e308","as":"c1"}`, `{"calculate":"toDate(datum.$F)","as":"c1"}`, `{"calculate":"length(datum.$F)","as":"c1"}`, `{"calculate":"pow(datum.$F, 0.5)","as":"c1"}`, `{"calculate":"datum.$F > 1 ? 'a' : 'b'","as":"c1"}`, `{"calculate":"upper(datum.$F)","as":"c1"}`, `{"calculate":"substring(datum.$F,1,3)","as":"c1"}`, `{"calculate":"datum.$F","as":"$F"}`,
	`{"bin":true,"field":"$F","as":"b1"}`, `{"bin":{"maxbins":$N},"field":"$F","as":["b1","b1_end"]}`, `{"bin":{"step":$N,"extent":[0,100]},"field":"$F","as":"b1"}`,
	`{"aggregate":[{"op":"count","as":"n"}],"groupby":["$F"]}`, `{"aggregate":[{"op":"mean","field":"$F","as":"m"}],"groupby":["$F"]}`, `{"aggregate":[{"op":"sum","field":"$F","as":"m"},{"op":"max","field":"$F","as":"mx"}]}`, `{"aggregate":[{"op":"median","field":"$F","as":"m"},{"op":"q1","field":"$F","as":"q"},{"op":"distinct","field":"$F","as":"d"}],"groupby":["$F"]}`, `{"aggregate":[{"op":"argmax","field":"$F","as":"m"}],"groupby":["$F"]}`, `{"aggregate":[{"op":"stdev","field":"$F","as":"s"},{"op":"variance","field":"$F","as":"v"},{"op":"ci0","field":"$F","as":"c"},{"op":"ci1","field":"$F","as":"c1"}],"groupby":["$F"]}`, `{"aggregate":[{"op":"values","field":"$F","as":"vs"}]}`,
	`{"window":[{"op":"rank","as":"r"}],"sort":[{"field":"$F","order":"descending"}]}`, `{"window":[{"op":"row_number","as":"r"}]}`, `{"window":[{"op":"sum","field":"$F","as":"cs"}],"frame":[null,0]}`, `{"window":[{"op":"mean","field":"$F","as":"ma"}],"frame":[-2,2],"groupby":["$F"]}`, `{"window":[{"op":"lag","field":"$F","param":1,"as":"lg"}]}`, `{"window":[{"op":"lead","field":"$F","as":"ld"}]}`, `{"window":[{"op":"first_value","field":"$F","as":"fv"},{"op":"last_value","field":"$F","as":"lv"}],"frame":[null,null]}`, `{"window":[{"op":"cume_dist","as":"cd"},{"op":"percent_rank","as":"pr"},{"op":"ntile","param":4,"as":"nt"},{"op":"dense_rank","as":"dr"}],"sort":[{"field":"$F"}]}`, `{"window":[{"op":"nth_value","field":"$F","param":2,"as":"nv"}]}`, `{"window":[{"op":"count","as":"c"}],"ignorePeers":true,"sort":[{"field":"$F"}]}`,
	`{"stack":"$F","groupby":["$F"],"as":["s0","s1"],"offset":"zero"}`, `{"stack":"$F","groupby":["$F"],"as":["s0","s1"],"offset":"normalize","sort":[{"field":"$F"}]}`, `{"stack":"$F","as":["s0","s1"],"offset":"center"}`,
	`{"fold":["$F","$F"],"as":["k","v"]}`, `{"flatten":["$F"]}`, `{"sample":$N}`, `{"sample":0}`, `{"timeUnit":"yearmonth","field":"$F","as":"tu"}`, `{"timeUnit":"month","field":"$F","as":"tu"}`,
	`{"impute":"$F","key":"$F","value":0}`, `{"impute":"$F","key":"$F","method":"mean","groupby":["$F"]}`, `{"impute":"$F","key":"$F","keyvals":[1,2,3],"method":"max"}`,
	`{"density":"$F","bandwidth":$N,"steps":$N,"extent":[0,100]}`, `{"density":"$F","groupby":["$F"],"counts":true,"cumulative":true}`, `{"loess":"$F","on":"$F","bandwidth":0.5}`, `{"regression":"$F","on":"$F","method":"linear"}`, `{"regression":"$F","on":"$F","method":"poly","order":$N}`, `{"regression":"$F","on":"$F","method":"log"}`, `{"regression":"$F","on":"$F","method":"exp","params":true}`, `{"regression":"$F","on":"$F","method":"pow","groupby":["$F"]}`, `{"regression":"$F","on":"$F","method":"quad","extent":[0,10]}`,
	`{"quantile":"$F","probs":[0.25,0.5,0.75]}`, `{"quantile":"$F","step":0.1,"groupby":["$F"]}`, `{"joinaggregate":[{"op":"mean","field":"$F","as":"jm"}],"groupby":["$F"]}`, `{"joinaggregate":[{"op":"count","as":"jc"}]}`, `{"pivot":"$F","value":"$F","groupby":["$F"]}`, `{"pivot":"$F","value":"$F","op":"max"}`,
	`{"lookup":"$F","from":{"data":{"values":[{"k":1,"v":"one"},{"k":2,"v":"two"}]},"key":"k","fields":["v"]}}`, `{"lookup":"$F","from":{"data":{"values":[{"k":1,"v":"one"}]},"key":"k"},"as":"lk","default":"none"}`,
	`{"extent":"$F","param":"ex"}`, `{"filter":{"param":"pt"}}`,
}

// Vega-side pools.
var poolVGScale = append([]string{
	`"type":"linear"`, `"type":"log"`, `"type":"pow"`, `"type":"sqrt"`, `"type":"symlog"`, `"type":"time"`, `"type":"utc"`, `"type":"band"`, `"type":"point"`, `"type":"ordinal"`, `"type":"quantile"`, `"type":"quantize"`, `"type":"threshold"`, `"type":"bin-ordinal"`, `"type":"sequential"`, `"type":"diverging"`, `"type":"identity"`,
	`"domainRaw":[0,1]`, `"range":{"step":$N}`, `"range":"width"`, `"range":"height"`, `"range":"category"`, `"range":"diverging"`, `"range":"ordinal"`, `"range":"symbol"`, `"range":{"scheme":"blues"}`, `"range":{"scheme":"viridis","count":$N}`, `"range":{"scheme":"reds","extent":[0.3,1]}`, `"range":[{"signal":"width"},0]`, `"range":[0,{"signal":"height"}]`,
	`"domain":{"data":"table","field":"$F"}`, `"domain":{"data":"table","fields":["$F","$F"]}`, `"domain":{"data":"table","field":"$F","sort":true}`, `"domain":{"data":"table","field":"$F","sort":{"op":"count","order":"descending"}}`, `"domain":{"data":"table","field":"$F","sort":{"op":"mean","field":"$F"}}`,
	`"domainMin":{"signal":"width"}`, `"padding":{"signal":"height"}`, `"round":true`, `"nice":{"signal":"width"}`,
}, poolScale...)

var poolVGAxis = append([]string{
	`"scale":"x"`, `"scale":"y"`, `"scale":"nosuchscale"`, `"orient":"left"`, `"orient":"bottom"`, `"grid":true`, `"gridScale":"x"`, `"gridScale":"y"`, `"bandPosition":$N`, `"tickBand":"extent"`, `"labelFlush":true`, `"labelOverlap":"parity"`, `"title":"$S"`, `"format":"$S"`, `"values":[1,2,3]`, `"encode":{"labels":{"update":{"fill":{"value":"red"},"angle":{"value":45}}}}`, `"encode":{"ticks":{"update":{"stroke":{"value":"red"}}}}`,
}, poolAxis...)

var poolVGLegend = append([]string{
	`"fill":"color"`, `"stroke":"color"`, `"size":"size"`, `"shape":"shape"`, `"opacity":"opacity"`, `"fill":"nosuchscale"`, `"encode":{"symbols":{"update":{"fill":{"value":"red"},"size":{"value":200}}}}`, `"encode":{"labels":{"update":{"fontSize":{"value":18}}}}`, `"encode":{"title":{"update":{"fill":{"value":"green"}}}}`,
}, poolLegend...)

var poolVGSignal = []string{
	`"value":$N`, `"value":"$S"`, `"value":null`, `"value":[1,2,3]`, `"value":{"a":1}`, `"update":"$N + 1"`, `"update":"width / 2"`, `"update":"datum"`, `"update":"[width, height]"`, `"update":"1/0"`, `"update":"sqrt(-1)"`, `"update":"now()"`, `"bind":{"input":"range","min":0,"max":10}`, `"react":false`, `"init":"$N"`,
}

var poolVGTransformParams = []string{
	`"as":["p","q"]`, `"as":["x"]`, `"as":[]`, `"as":"x"`, `"groupby":[]`, `"groupby":["$F"]`, `"groupby":["$F","$F"]`, `"field":"$F"`, `"field":"nofield"`, `"fields":["$F"]`, `"fields":["$F","$F"]`, `"fields":[]`, `"sort":{"field":"$F"}`, `"sort":{"field":"$F","order":"descending"}`, `"sort":{"field":["$F","$F"],"order":["ascending","descending"]}`,
	`"extent":[0,$N]`, `"extent":[$N,0]`, `"extent":[]`, `"steps":$N`, `"steps":0`, `"steps":-1`, `"maxbins":$N`, `"maxbins":0`, `"maxbins":1`, `"minstep":$N`, `"step":$N`, `"step":0`, `"nice":false`, `"base":$N`, `"anchor":$N`, `"divide":[2]`,
	`"offset":"center"`, `"offset":"normalize"`, `"offset":"zero"`, `"method":"pow"`, `"method":"linear"`, `"method":"quad"`, `"method":"poly"`, `"order":$N`, `"order":0`, `"bandwidth":$N`, `"bandwidth":0`, `"bandwidth":-1`, `"cumulative":true`, `"counts":true`, `"density":true`, `"ops":["mean"]`, `"ops":["count","sum","max","min","median","q1","q3","stdev","variance","distinct","valid","missing"]`, `"frame":[null,0]`, `"frame":[-1,1]`, `"frame":[0,0]`, `"frame":[$N,-$N]`, `"ignorePeers":true`, `"cross":true`, `"drop":false`, `"key":"$F"`, `"keyvals":[1,2,3]`, `"value":$N`, `"probs":[0.5]`, `"probs":[]`, `"probs":[0,1]`, `"size":[$N,$N]`, `"size":[0,0]`, `"padding":$N`, `"iterations":$N`, `"iterations":0`, `"thresholds":[1,2]`, `"levels":$N`, `"levels":0`, `"smooth":false`, `"bandwidth":[$N,$N]`, `"expr":"datum.$F * 2"`, `"expr":"null"`, `"expr":"NaN"`, `"expr":"datum.$F + 1e308"`, `"expr":"datum.$F ? 1 : 0"`, `"expr":"$F"`,
	`"projection":"projection"`, `"shape":"line"`, `"shape":"arc"`, `"shape":"curve"`, `"shape":"diagonal"`, `"shape":"orthogonal"`, `"orient":"horizontal"`, `"orient":"vertical"`, `"orient":"radial"`, `"radius":{"signal":"width"}`, `"startAngle":$N`, `"endAngle":$N`, `"sort":true`, `"limit":$N`, `"limit":0`, `"stopwords":"a"`, `"case":"upper"`, `"pattern":"\\w+"`, `"pattern":"["`, `"as":["a","b","c","d","e","f"]`, `"mode":"$S"`, `"separation":true`, `"method":"squarify"`, `"method":"binary"`, `"ratio":$N`, `"paddingInner":$N`, `"paddingOuter":$N`, `"round":true`, `"radius":true`,
}

var poolVGFilterExpr = []string{"datum.$F > $N", "isValid(datum.$F)", "datum.$F != null", "true", "false", "!isNaN(datum.$F)", "datum.$F", "0", "datum.$F < 1e308", "isDate(datum.$F)"}

var poolVGTransform = []string{
	`{"type":"filter","expr":"$FE"}`, `{"type":"formula","expr":"datum.$F * 2","as":"f1"}`, `{"type":"formula","expr":"datum.$F + ''","as":"f1"}`,
	`{"type":"aggregate","groupby":["$F"],"fields":["$F"],"ops":["mean"],"as":["m"]}`, `{"type":"aggregate","fields":["$F","$F"],"ops":["min","max"]}`,
	`{"type":"collect","sort":{"field":"$F","order":"descending"}}`, `{"type":"bin","field":"$F","extent":[0,$N],"maxbins":$N}`, `{"type":"stack","groupby":["$F"],"field":"$F","sort":{"field":"$F"}}`,
	`{"type":"window","ops":["rank","sum"],"fields":[null,"$F"],"sort":{"field":"$F"}}`, `{"type":"extent","field":"$F","signal":"ex"}`, `{"type":"kde","field":"$F","bandwidth":$N}`, `{"type":"regression","x":"$F","y":"$F","method":"poly","order":3}`,
	`{"type":"loess","x":"$F","y":"$F"}`, `{"type":"quantile","field":"$F","probs":[0.1,0.9]}`, `{"type":"sample","size":$N}`, `{"type":"identifier","as":"id"}`, `{"type":"fold","fields":["$F","$F"]}`, `{"type":"impute","field":"$F","key":"$F","value":0}`,
	`{"type":"joinaggregate","fields":["$F"],"ops":["mean"],"as":["jm"],"groupby":["$F"]}`, `{"type":"pivot","field":"$F","value":"$F"}`, `{"type":"countpattern","field":"$F"}`, `{"type":"project","fields":["$F"],"as":["pp"]}`,
	`{"type":"density","extent":[0,$N],"distribution":{"function":"normal","mean":0,"stdev":1}}`, `{"type":"density","extent":[0,$N],"distribution":{"function":"uniform","min":0,"max":5}}`, `{"type":"timeunit","field":"$F","units":["year","month"]}`, `{"type":"sequence","start":0,"stop":$N,"step":1}`,
}

func randKey(o *jsval.Object, rng *rand.Rand) (string, bool) {
	if o.Len() == 0 {
		return "", false
	}
	return o.KeyAt(rng.Intn(o.Len())), true
}

// kindOf classifies the object at path (with its parent key names) for the
// property pools.
func kindOf(path []string, lite bool) string {
	if len(path) == 0 {
		return "top"
	}
	last := path[len(path)-1]
	prev := ""
	if len(path) >= 2 {
		prev = path[len(path)-2]
	}
	isIdx := func(s string) bool { _, err := strconv.Atoi(s); return err == nil }
	switch {
	case last == "scale":
		return "scale"
	case last == "axis":
		return "axis"
	case last == "legend":
		return "legend"
	case last == "mark":
		return "mark"
	case last == "config":
		return "config"
	case prev == "encoding":
		return "def"
	case last == "encoding":
		return "encoding"
	case lite && (last == "spec" || (prev == "layer" && isIdx(last)) || ((prev == "vconcat" || prev == "hconcat" || prev == "concat") && isIdx(last))):
		return "top"
	case !lite && prev == "scales" && isIdx(last):
		return "vgscale"
	case !lite && prev == "axes" && isIdx(last):
		return "vgaxis"
	case !lite && prev == "legends" && isIdx(last):
		return "vglegend"
	case !lite && prev == "signals" && isIdx(last):
		return "vgsignal"
	case !lite && prev == "transform" && isIdx(last):
		return "vgtransform"
	case !lite && prev == "marks" && isIdx(last):
		return "vgmark"
	}
	return ""
}

func poolFor(kind string) []string {
	switch kind {
	case "scale":
		return poolScale
	case "axis":
		return poolAxis
	case "legend":
		return poolLegend
	case "mark":
		return poolMark
	case "def":
		return poolDef
	case "top", "config":
		return poolTop
	case "vgscale":
		return poolVGScale
	case "vgaxis":
		return poolVGAxis
	case "vglegend":
		return poolVGLegend
	case "vgsignal":
		return poolVGSignal
	case "vgtransform":
		return poolVGTransformParams
	}
	return nil
}

// objectsByKind finds all objects (with their slots) of a pooled kind.
func (m *mut) objectsByKind(all []slot) map[string][]slot {
	out := map[string][]slot{}
	if m.root.IsObj() {
		out["top"] = append(out["top"], slot{path: nil})
	}
	for _, s := range all {
		v := s.get()
		if !v.IsObj() {
			continue
		}
		if k := kindOf(s.path, m.lite); k != "" {
			out[k] = append(out[k], s)
		}
	}
	return out
}

func (m *mut) objAt(s slot) *jsval.Object {
	if len(s.path) == 0 {
		return m.root.ObjValue()
	}
	return s.get().ObjValue()
}

// setMember parses `"key":value` and stores it into o.
func (m *mut) setMember(o *jsval.Object, member string) {
	v := m.expand("{" + member + "}")
	ov := v.ObjValue()
	for i := 0; i < ov.Len(); i++ {
		o.Set(ov.KeyAt(i), ov.ValueAt(i))
	}
	m.log = append(m.log, "set "+member)
}

// A mutator applies one edit; it reports whether it found somewhere to edit.
type mutator struct {
	name   string
	weight int
	fn     func(m *mut, all []slot) bool
}

func mutators() []mutator {
	return []mutator{
		{"prop", 40, mutProp},
		{"delprop", 8, mutDelete},
		{"num", 12, mutNumber},
		{"str", 6, mutString},
		{"data", 14, mutData},
		{"marktype", 8, mutMarkType},
		{"channel", 10, mutChannel},
		{"transform", 10, mutTransform},
		{"wrap", 4, mutWrap},
		{"expr", 6, mutExpr},
		{"arr", 4, mutArray},
		{"nullify", 3, mutNullify},
	}
}

func mutProp(m *mut, all []slot) bool {
	by := m.objectsByKind(all)
	var kinds []string
	for k := range by {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	if len(kinds) == 0 {
		return false
	}
	// Prefer the more specific kinds over "top".
	k := kinds[m.rng.Intn(len(kinds))]
	if k == "top" && m.chance(0.7) && len(kinds) > 1 {
		k = kinds[m.rng.Intn(len(kinds))]
	}
	pool := poolFor(k)
	if len(pool) == 0 {
		return false
	}
	objs := by[k]
	o := m.objAt(objs[m.rng.Intn(len(objs))])
	if o == nil {
		return false
	}
	member := pool[m.rng.Intn(len(pool))]
	m.setMember(o, member)
	return true
}

func mutDelete(m *mut, all []slot) bool {
	var objs []slot
	for _, s := range all {
		if s.get().IsObj() && s.get().ObjValue().Len() > 0 {
			objs = append(objs, s)
		}
	}
	if len(objs) == 0 {
		return false
	}
	o := objs[m.rng.Intn(len(objs))].get().ObjValue()
	k, _ := randKey(o, m.rng)
	if k == "$schema" || k == "values" && m.chance(0.7) {
		return false
	}
	o.Delete(k)
	m.log = append(m.log, "delete "+k)
	return true
}

func mutNumber(m *mut, all []slot) bool {
	var nums []slot
	for _, s := range all {
		if s.get().IsNum() {
			nums = append(nums, s)
		}
	}
	if len(nums) == 0 {
		return false
	}
	s := nums[m.rng.Intn(len(nums))]
	x := s.get().NumValue()
	var y float64
	switch m.rng.Intn(12) {
	case 0:
		y = 0
	case 1:
		y = -x
	case 2:
		y = x * 10
	case 3:
		y = x / 10
	case 4:
		y = 1e308
	case 5:
		y = 1e-308
	case 6:
		y = x + 1
	case 7:
		y = math.Floor(x)
	case 8:
		y = 5e-324
	case 9:
		y = -1
	case 10:
		y = x * 1e6
	default:
		y = 0.5
	}
	s.set(jsval.Num(y))
	m.log = append(m.log, "num "+s.pathString())
	return true
}

func mutString(m *mut, all []slot) bool {
	var strs []slot
	for _, s := range all {
		if !s.get().IsStr() {
			continue
		}
		n := s.name()
		if n == "$schema" || n == "type" || n == "field" || n == "name" || n == "scale" || n == "data" || n == "from" || n == "signal" || n == "expr" {
			if !m.chance(0.1) {
				continue
			}
		}
		strs = append(strs, s)
	}
	if len(strs) == 0 {
		return false
	}
	s := strs[m.rng.Intn(len(strs))]
	s.set(jsval.Str(m.pick(trickyStrings)))
	m.log = append(m.log, "str "+s.pathString())
	return true
}

func mutNullify(m *mut, all []slot) bool {
	if len(all) == 0 {
		return false
	}
	s := all[m.rng.Intn(len(all))]
	if s.name() == "$schema" {
		return false
	}
	switch m.rng.Intn(4) {
	case 0:
		s.set(jsval.Null)
	case 1:
		s.set(jsval.Str(""))
	case 2:
		s.set(jsval.Num(0))
	default:
		s.set(jsval.ArrOf())
	}
	m.log = append(m.log, "nullify "+s.pathString())
	return true
}

// mutData edits inline data rows.
func mutData(m *mut, all []slot) bool {
	var tables []slot
	for _, s := range all {
		if s.name() == "values" && s.get().IsArr() && s.get().Len() > 0 && s.get().Index(0).IsObj() {
			tables = append(tables, s)
		}
	}
	if len(tables) == 0 {
		return false
	}
	t := tables[m.rng.Intn(len(tables))]
	arr := t.get()
	rows := arr.Items()
	row := rows[m.rng.Intn(len(rows))]
	ro := row.ObjValue()
	if ro == nil {
		return false
	}
	pickField := func() string {
		k, _ := randKey(ro, m.rng)
		return k
	}
	cell := func() jsval.Value {
		switch m.rng.Intn(6) {
		case 0:
			return jsval.Null
		case 1:
			return jsval.Str(m.pick(trickyStrings))
		case 2:
			return parseSnippet(m.pick(hugeNumbers))
		case 3:
			return jsval.Bool(m.chance(0.5))
		case 4:
			return parseSnippet(m.pick([]string{"[]", "{}", "[1,2]", `{"a":1}`, `["x"]`}))
		default:
			return jsval.Num(float64(m.rng.Intn(21) - 10))
		}
	}
	switch m.rng.Intn(9) {
	case 0, 1, 2:
		k := pickField()
		if k == "" {
			return false
		}
		ro.Set(k, cell())
		m.log = append(m.log, "cell "+k)
	case 3: // a whole column
		k := pickField()
		if k == "" {
			return false
		}
		v := cell()
		for _, r := range rows {
			if o := r.ObjValue(); o != nil && m.chance(0.6) {
				o.Set(k, v)
			}
		}
		m.log = append(m.log, "column "+k)
	case 4: // drop a field in a row
		if k := pickField(); k != "" {
			ro.Delete(k)
		}
		m.log = append(m.log, "dropfield")
	case 5: // duplicate rows
		var dup []jsval.Value
		dup = append(dup, rows...)
		for i := 0; i < 1+m.rng.Intn(3); i++ {
			dup = append(dup, jsval.Obj(row.ObjValue().Clone()))
		}
		t.set(jsval.Arr(dup))
		m.log = append(m.log, "duprows")
	case 6: // one row, or none
		if m.chance(0.5) {
			t.set(jsval.Arr([]jsval.Value{row}))
		} else {
			t.set(jsval.ArrOf())
		}
		m.log = append(m.log, "fewrows")
	case 7: // an all-null row
		n := jsval.NewObject(ro.Len())
		for i := 0; i < ro.Len(); i++ {
			n.Set(ro.KeyAt(i), jsval.Null)
		}
		t.set(jsval.Arr(append(append([]jsval.Value(nil), rows...), jsval.Obj(n))))
		m.log = append(m.log, "nullrow")
	default: // extreme numeric column
		k := pickField()
		if k == "" {
			return false
		}
		v := parseSnippet(m.pick(hugeNumbers))
		for _, r := range rows {
			if o := r.ObjValue(); o != nil && o.Lookup(k).IsNum() && m.chance(0.4) {
				o.Set(k, v)
			}
		}
		m.log = append(m.log, "extreme "+k)
	}
	return true
}

func mutMarkType(m *mut, all []slot) bool {
	for _, i := range m.rng.Perm(len(all)) {
		s := all[i]
		if m.lite {
			if s.name() == "mark" {
				v := s.get()
				t := m.pick(markTypes)
				switch {
				case v.IsStr():
					s.set(jsval.Str(t))
				case v.IsObj():
					v.ObjValue().Set("type", jsval.Str(t))
				default:
					continue
				}
				m.log = append(m.log, "mark "+t)
				return true
			}
		} else if s.name() == "type" && len(s.path) >= 2 && s.path[len(s.path)-2] != "type" {
			// a vega mark: marks/<i>/type (also nested)
			if len(s.path) >= 3 && s.path[len(s.path)-3] == "marks" && s.get().IsStr() && s.get().StrValue() != "group" {
				t := m.pick(vegaMarkTypes)
				s.set(jsval.Str(t))
				m.log = append(m.log, "mark "+t)
				return true
			}
		}
	}
	return false
}

func mutChannel(m *mut, all []slot) bool {
	if !m.lite {
		return false
	}
	var encs []slot
	for _, s := range all {
		if s.name() == "encoding" && s.get().IsObj() {
			encs = append(encs, s)
		}
	}
	if len(encs) == 0 {
		return false
	}
	e := encs[m.rng.Intn(len(encs))].get().ObjValue()
	switch m.rng.Intn(5) {
	case 0: // rename
		k, ok := randKey(e, m.rng)
		if !ok {
			return false
		}
		v := e.Lookup(k)
		e.Delete(k)
		nk := m.pick(channels)
		e.Set(nk, v)
		m.log = append(m.log, "rename "+k+"->"+nk)
	case 1: // swap
		if e.Len() < 2 {
			return false
		}
		a, b := e.KeyAt(m.rng.Intn(e.Len())), e.KeyAt(m.rng.Intn(e.Len()))
		va, vb := e.Lookup(a), e.Lookup(b)
		e.Set(a, vb)
		e.Set(b, va)
		m.log = append(m.log, "swap "+a+","+b)
	case 2: // copy a def to another channel
		k, ok := randKey(e, m.rng)
		if !ok {
			return false
		}
		nk := m.pick(channels)
		e.Set(nk, deepCopy(e.Lookup(k)))
		m.log = append(m.log, "copy "+k+"->"+nk)
	case 3: // remove
		k, ok := randKey(e, m.rng)
		if !ok {
			return false
		}
		e.Delete(k)
		m.log = append(m.log, "remove "+k)
	default: // add a fresh def
		typ := m.pick([]string{"quantitative", "nominal", "ordinal", "temporal"})
		nk := m.pick(channels)
		def := m.expand(`{"field":"$F","type":"` + typ + `"}`)
		if m.chance(0.2) {
			def = m.expand(`{"aggregate":"count","type":"quantitative"}`)
		}
		e.Set(nk, def)
		m.log = append(m.log, "add "+nk)
	}
	return true
}

func deepCopy(v jsval.Value) jsval.Value {
	switch {
	case v.IsObj():
		o := v.ObjValue()
		c := jsval.NewObject(o.Len())
		for i := 0; i < o.Len(); i++ {
			c.Set(o.KeyAt(i), deepCopy(o.ValueAt(i)))
		}
		return jsval.Obj(c)
	case v.IsArr():
		items := make([]jsval.Value, v.Len())
		for i := range items {
			items[i] = deepCopy(v.Index(i))
		}
		return jsval.Arr(items)
	}
	return v
}

func mutTransform(m *mut, all []slot) bool {
	if m.lite {
		// Append a transform to a unit or the top-level spec.
		var hosts []*jsval.Object
		if m.root.IsObj() {
			hosts = append(hosts, m.root.ObjValue())
		}
		for _, s := range all {
			if k := kindOf(s.path, true); k == "top" && s.get().IsObj() {
				hosts = append(hosts, s.get().ObjValue())
			}
		}
		h := hosts[m.rng.Intn(len(hosts))]
		t := m.expand(m.pick(poolVLTransform))
		cur := h.Lookup("transform")
		if cur.IsArr() {
			items := append([]jsval.Value(nil), cur.Items()...)
			pos := m.rng.Intn(len(items) + 1)
			items = append(items[:pos], append([]jsval.Value{t}, items[pos:]...)...)
			h.Set("transform", jsval.Arr(items))
		} else {
			h.Set("transform", jsval.ArrOf(t))
		}
		m.log = append(m.log, "transform")
		return true
	}
	// Vega: append a transform to a data set.
	var datas []slot
	for _, s := range all {
		if len(s.path) == 2 && s.path[0] == "data" && s.get().IsObj() {
			datas = append(datas, s)
		}
	}
	if len(datas) == 0 {
		return false
	}
	d := datas[m.rng.Intn(len(datas))].get().ObjValue()
	snippet := m.pick(poolVGTransform)
	if strings.Contains(snippet, "$FE") {
		q := string(jsval.AppendJSON(nil, jsval.Str(m.pick(poolVGFilterExpr))))
		snippet = strings.ReplaceAll(snippet, "$FE", q[1:len(q)-1])
	}
	t := m.expand(snippet)
	cur := d.Lookup("transform")
	if cur.IsArr() {
		items := append([]jsval.Value(nil), cur.Items()...)
		pos := m.rng.Intn(len(items) + 1)
		items = append(items[:pos], append([]jsval.Value{t}, items[pos:]...)...)
		d.Set("transform", jsval.Arr(items))
	} else {
		d.Set("transform", jsval.ArrOf(t))
	}
	m.log = append(m.log, "transform")
	return true
}

// mutWrap embeds a Vega-Lite unit spec in a compound spec.
func mutWrap(m *mut, all []slot) bool {
	if !m.lite || !m.root.IsObj() {
		return false
	}
	o := m.root.ObjValue()
	if !o.Has("mark") || !o.Has("encoding") {
		return false
	}
	inner := jsval.NewObject(o.Len())
	outer := jsval.NewObject(4)
	for i := 0; i < o.Len(); i++ {
		k := o.KeyAt(i)
		switch k {
		case "$schema", "config", "background", "padding", "autosize", "title", "description", "params", "usermeta", "name":
			outer.Set(k, o.ValueAt(i))
		default:
			inner.Set(k, o.ValueAt(i))
		}
	}
	in := jsval.Obj(inner)
	switch m.rng.Intn(6) {
	case 0:
		outer.Set("layer", jsval.ArrOf(in, deepCopy(in)))
		mm := deepCopy(in).ObjValue()
		mm.Set("mark", jsval.Str(m.pick(markTypes)))
		outer.Set("layer", jsval.ArrOf(in, jsval.Obj(mm)))
	case 1:
		outer.Set("vconcat", jsval.ArrOf(in, deepCopy(in)))
	case 2:
		outer.Set("hconcat", jsval.ArrOf(in, deepCopy(in)))
	case 3:
		outer.Set("facet", m.expand(`{"column":{"field":"$F","type":"nominal"}}`))
		outer.Set("spec", in)
	case 4:
		outer.Set("facet", m.expand(`{"row":{"field":"$F","type":"ordinal"},"column":{"field":"$F","type":"nominal"}}`))
		outer.Set("spec", in)
	default:
		outer.Set("concat", jsval.ArrOf(in, deepCopy(in), deepCopy(in)))
		outer.Set("columns", jsval.Int(2))
	}
	m.root = jsval.Obj(outer)
	m.log = append(m.log, "wrap")
	return true
}

var exprWraps = []string{"floor(%s)", "ceil(%s)", "abs(%s)", "sqrt(%s)", "log(%s)", "exp(%s)", "isValid(%s)", "toNumber(%s)", "toString(%s)", "toDate(%s)", "-(%s)", "!(%s)", "(%s) / 0", "(%s) * 1e308", "(%s) + ''", "(%s) || null", "length(%s)", "format(%s, '.3s')", "timeFormat(%s, '%%b %%d')", "round(%s)", "min(%s, 0)", "max(%s, 1e300)", "clamp(%s, -1, 1)", "(%s) % 0", "pow(%s, 100000)", "isNaN(%s)", "isFinite(%s)", "[%s][1]", "(%s) ? 1 : 0", "datum ? %s : 0", "sin(%s)", "atan2(%s, 0)"}

func mutExpr(m *mut, all []slot) bool {
	var exprs []slot
	for _, s := range all {
		if !s.get().IsStr() {
			continue
		}
		switch s.name() {
		case "expr", "signal", "calculate", "filter", "update", "test", "labelExpr":
			exprs = append(exprs, s)
		}
	}
	if len(exprs) == 0 {
		return false
	}
	s := exprs[m.rng.Intn(len(exprs))]
	e := s.get().StrValue()
	var out string
	switch m.rng.Intn(4) {
	case 0:
		out = strings.Replace(e, "+", "-", 1)
	case 1:
		out = strings.Replace(e, "datum.", "datum.zzz_", 1)
	default:
		out = strings.Replace(m.pick(exprWraps), "%s", e, 1)
		out = strings.ReplaceAll(out, "%%", "%")
	}
	s.set(jsval.Str(out))
	m.log = append(m.log, "expr "+s.pathString())
	return true
}

func mutArray(m *mut, all []slot) bool {
	var arrs []slot
	for _, s := range all {
		if s.get().IsArr() && s.get().Len() > 0 && s.name() != "values" {
			arrs = append(arrs, s)
		}
	}
	if len(arrs) == 0 {
		return false
	}
	s := arrs[m.rng.Intn(len(arrs))]
	items := append([]jsval.Value(nil), s.get().Items()...)
	i := m.rng.Intn(len(items))
	switch m.rng.Intn(4) {
	case 0:
		items = append(items[:i], items[i+1:]...)
	case 1:
		items = append(items, deepCopy(items[i]))
	case 2:
		j := m.rng.Intn(len(items))
		items[i], items[j] = items[j], items[i]
	default:
		for a, b := 0, len(items)-1; a < b; a, b = a+1, b-1 {
			items[a], items[b] = items[b], items[a]
		}
	}
	s.set(jsval.Arr(items))
	m.log = append(m.log, "array "+s.pathString())
	return true
}

// fieldsOf lists the field names a spec mentions (encoding and transform
// fields, and the keys of inline data).
func fieldsOf(root jsval.Value) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] && !strings.ContainsAny(s, ".[]\\") {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range collect(root) {
		v := s.get()
		if s.name() == "field" && v.IsStr() {
			add(v.StrValue())
		}
		if s.name() == "values" && v.IsArr() && v.Len() > 0 && v.Index(0).IsObj() {
			o := v.Index(0).ObjValue()
			for i := 0; i < o.Len(); i++ {
				add(o.KeyAt(i))
			}
		}
	}
	sort.Strings(out)
	return out
}

// mutate applies 1-3 random edits to a copy of base.
func mutate(base jsval.Value, lite bool, rng *rand.Rand) (jsval.Value, []string) {
	m := &mut{rng: rng, lite: lite, root: deepCopy(base)}
	m.fields = fieldsOf(m.root)
	ms := mutators()
	total := 0
	for _, x := range ms {
		total += x.weight
	}
	n := 1 + rng.Intn(3)
	for done, tries := 0, 0; done < n && tries < 30; tries++ {
		r := rng.Intn(total)
		var chosen mutator
		for _, x := range ms {
			if r < x.weight {
				chosen = x
				break
			}
			r -= x.weight
		}
		if chosen.fn(m, collect(m.root)) {
			done++
		}
	}
	return m.root, m.log
}
