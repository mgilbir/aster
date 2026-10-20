package expr

import "github.com/mgilbir/aster/internal/jsval"

// The string-named members the d3 colours rgb(), hsl(), lab() and hcl() make
// inherit from their class's prototype and d3's Color.prototype (C156), which
// the `in` operator finds and a property read answers. The built-in
// prototypes (Object's, Array's, String's, Number's, Boolean's, RegExp's and
// Date's) are jsval's, the one table of them (jsval.ObjectProto and so on);
// these are the colours', which only the expression language has.
// TestFunctionTable checks them against testdata/functions.json, which
// testdata/gen_functions.mjs records.
//
// A function member reads as one shared inert function (jsval.NewProto) that
// prints as native code and that the engine never calls: an expression can
// only call the functions of its own table. A d3 colour's methods print as
// native code too, where d3's print their source.

// member is a function member: the name of the function it holds and its length.
func member(name string, arity int) jsval.ProtoMember {
	return jsval.ProtoMember{Fn: name, Arity: arity}
}

// colorProto is d3-color's Color.prototype, which every colour class extends.
var colorProto = jsval.ProtoTable{
	"constructor": member("Color", 0), "copy": member("copy", 1), "displayable": member("displayable", 0),
	"formatHex": member("color_formatHex", 0), "formatHex8": member("color_formatHex8", 0),
	"formatHsl": member("color_formatHsl", 0), "formatRgb": member("color_formatRgb", 0),
	"hex": member("color_formatHex", 0), "toString": member("color_formatRgb", 0),
}

// rgbProto, hslProto, labProto and hclProto are d3-color's Rgb, Hsl, Lab and Hcl prototypes.
var rgbProto = jsval.ProtoTable{
	"brighter": member("brighter", 1), "clamp": member("clamp", 0), "constructor": member("Rgb", 4), "darker": member("darker", 1),
	"displayable": member("displayable", 0), "formatHex": member("rgb_formatHex", 0), "formatHex8": member("rgb_formatHex8", 0),
	"formatRgb": member("rgb_formatRgb", 0), "hex": member("rgb_formatHex", 0), "rgb": member("rgb", 0),
	"toString": member("rgb_formatRgb", 0),
}

var hslProto = jsval.ProtoTable{
	"brighter": member("brighter", 1), "clamp": member("clamp", 0), "constructor": member("Hsl", 4), "darker": member("darker", 1),
	"displayable": member("displayable", 0), "formatHsl": member("formatHsl", 0), "rgb": member("rgb", 0),
}

var labProto = jsval.ProtoTable{
	"brighter": member("brighter", 1), "constructor": member("Lab", 4), "darker": member("darker", 1), "rgb": member("rgb", 0),
}

var hclProto = jsval.ProtoTable{
	"brighter": member("brighter", 1), "constructor": member("Hcl", 4), "darker": member("darker", 1), "rgb": member("rgb", 0),
}

// colourForIn are the inherited members a for-in loop visits on a colour of
// each class (d3 defines its methods as enumerable), after the colour's own
// channels, in the order it visits them.
var colourForIn = map[string][]string{
	"Rgb": {"brighter", "darker", "rgb", "clamp", "displayable", "hex", "formatHex", "formatHex8", "formatRgb", "toString", "constructor", "copy", "formatHsl"},
	"Hsl": {"brighter", "darker", "rgb", "clamp", "displayable", "formatHsl", "constructor", "copy", "hex", "formatHex", "formatHex8", "formatRgb", "toString"},
	"Lab": {"brighter", "darker", "rgb", "constructor", "copy", "displayable", "hex", "formatHex", "formatHex8", "formatHsl", "formatRgb", "toString"},
	"Hcl": {"brighter", "darker", "rgb", "constructor", "copy", "displayable", "hex", "formatHex", "formatHex8", "formatHsl", "formatRgb", "toString"},
}

// disallowedProperties are the object literal keys code generation rejects:
// vega-util's DisallowedObjectProperties, Object.prototype's methods,
// __proto__ and then.
var disallowedProperties = map[string]bool{
	"__defineGetter__": true, "__defineSetter__": true, "__lookupGetter__": true, "__lookupSetter__": true,
	"__proto__": true, "constructor": true, "hasOwnProperty": true, "isPrototypeOf": true,
	"propertyIsEnumerable": true, "then": true, "toLocaleString": true, "toString": true, "valueOf": true,
}

var (
	colorP   = jsval.NewProto(colorProto, jsval.ObjectProto)
	colourPs = map[string]*jsval.Proto{
		"Rgb": jsval.NewProto(rgbProto, colorP), "Hsl": jsval.NewProto(hslProto, colorP),
		"Lab": jsval.NewProto(labProto, colorP), "Hcl": jsval.NewProto(hclProto, colorP),
	}
)

// objectProtoMember reports whether key names a member of Object.prototype.
func objectProtoMember(key string) bool { return jsval.ObjectPrototypeHas(key) }
