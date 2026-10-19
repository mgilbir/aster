package expr

import "github.com/mgilbir/aster/internal/jsval"

// The string-named members each kind of value inherits from its prototype in
// node (Object.getOwnPropertyNames of each prototype on the chain), which the
// `in` operator finds and a property read answers (C156): the members of
// Object.prototype for every object, of Array.prototype, String.prototype and
// so on for the others, and of d3's colour classes for the colours rgb(),
// hsl(), lab() and hcl() make. TestFunctionTable checks them against
// testdata/functions.json, which testdata/gen_functions.mjs records.
//
// A function member reads as an inert function (jsval.NativeFunction) that
// prints as native code and that the engine never calls: an expression can
// only call the functions of its own table. Each is one shared value, so two
// reads of a member are the same function, and aliases (trimLeft and
// trimStart) are one function, as in JavaScript. A d3 colour's methods print
// as native code too, where d3's print their source.

// protoTable is one prototype's members: each the name of the function it
// holds, or "" for one that is not a function (an accessor such as
// RegExp.prototype.source, which a value of the kind answers itself).
type protoTable map[string]string

// objectProto is Object.prototype: what every object inherits.
var objectProto = protoTable{
	"__defineGetter__": "__defineGetter__", "__defineSetter__": "__defineSetter__",
	"__lookupGetter__": "__lookupGetter__", "__lookupSetter__": "__lookupSetter__", "__proto__": "",
	"constructor": "Object", "hasOwnProperty": "hasOwnProperty", "isPrototypeOf": "isPrototypeOf",
	"propertyIsEnumerable": "propertyIsEnumerable", "toLocaleString": "toLocaleString", "toString": "toString",
	"valueOf": "valueOf",
}

// arrayProto is Array.prototype's.
var arrayProto = protoTable{
	"at": "at", "concat": "concat", "constructor": "Array", "copyWithin": "copyWithin", "entries": "entries",
	"every": "every", "fill": "fill", "filter": "filter", "find": "find", "findIndex": "findIndex",
	"findLast": "findLast", "findLastIndex": "findLastIndex", "flat": "flat", "flatMap": "flatMap",
	"forEach": "forEach", "includes": "includes", "indexOf": "indexOf", "join": "join", "keys": "keys",
	"lastIndexOf": "lastIndexOf", "length": "", "map": "map", "pop": "pop", "push": "push", "reduce": "reduce",
	"reduceRight": "reduceRight", "reverse": "reverse", "shift": "shift", "slice": "slice", "some": "some",
	"sort": "sort", "splice": "splice", "toLocaleString": "toLocaleString", "toReversed": "toReversed",
	"toSorted": "toSorted", "toSpliced": "toSpliced", "toString": "toString", "unshift": "unshift",
	"values": "values", "with": "with",
}

// stringProto is String.prototype's.
var stringProto = protoTable{
	"anchor": "anchor", "at": "at", "big": "big", "blink": "blink", "bold": "bold", "charAt": "charAt",
	"charCodeAt": "charCodeAt", "codePointAt": "codePointAt", "concat": "concat", "constructor": "String",
	"endsWith": "endsWith", "fixed": "fixed", "fontcolor": "fontcolor", "fontsize": "fontsize",
	"includes": "includes", "indexOf": "indexOf", "isWellFormed": "isWellFormed", "italics": "italics",
	"lastIndexOf": "lastIndexOf", "length": "", "link": "link", "localeCompare": "localeCompare",
	"match": "match", "matchAll": "matchAll", "normalize": "normalize", "padEnd": "padEnd",
	"padStart": "padStart", "repeat": "repeat", "replace": "replace", "replaceAll": "replaceAll",
	"search": "search", "slice": "slice", "small": "small", "split": "split", "startsWith": "startsWith",
	"strike": "strike", "sub": "sub", "substr": "substr", "substring": "substring", "sup": "sup",
	"toLocaleLowerCase": "toLocaleLowerCase", "toLocaleUpperCase": "toLocaleUpperCase",
	"toLowerCase": "toLowerCase", "toString": "toString", "toUpperCase": "toUpperCase",
	"toWellFormed": "toWellFormed", "trim": "trim", "trimEnd": "trimEnd", "trimLeft": "trimStart",
	"trimRight": "trimEnd", "trimStart": "trimStart", "valueOf": "valueOf",
}

// numberProto is Number.prototype's.
var numberProto = protoTable{
	"constructor": "Number", "toExponential": "toExponential", "toFixed": "toFixed",
	"toLocaleString": "toLocaleString", "toPrecision": "toPrecision", "toString": "toString",
	"valueOf": "valueOf",
}

// booleanProto is Boolean.prototype's.
var booleanProto = protoTable{
	"constructor": "Boolean", "toString": "toString", "valueOf": "valueOf",
}

// regexpProto is RegExp.prototype's.
var regexpProto = protoTable{
	"compile": "compile", "constructor": "RegExp", "dotAll": "", "exec": "exec", "flags": "", "global": "",
	"hasIndices": "", "ignoreCase": "", "multiline": "", "source": "", "sticky": "", "test": "test",
	"toString": "toString", "unicode": "", "unicodeSets": "",
}

// dateProto is Date.prototype's.
var dateProto = protoTable{
	"constructor": "Date", "getDate": "getDate", "getDay": "getDay", "getFullYear": "getFullYear",
	"getHours": "getHours", "getMilliseconds": "getMilliseconds", "getMinutes": "getMinutes",
	"getMonth": "getMonth", "getSeconds": "getSeconds", "getTime": "getTime",
	"getTimezoneOffset": "getTimezoneOffset", "getUTCDate": "getUTCDate", "getUTCDay": "getUTCDay",
	"getUTCFullYear": "getUTCFullYear", "getUTCHours": "getUTCHours",
	"getUTCMilliseconds": "getUTCMilliseconds", "getUTCMinutes": "getUTCMinutes", "getUTCMonth": "getUTCMonth",
	"getUTCSeconds": "getUTCSeconds", "getYear": "getYear", "setDate": "setDate", "setFullYear": "setFullYear",
	"setHours": "setHours", "setMilliseconds": "setMilliseconds", "setMinutes": "setMinutes",
	"setMonth": "setMonth", "setSeconds": "setSeconds", "setTime": "setTime", "setUTCDate": "setUTCDate",
	"setUTCFullYear": "setUTCFullYear", "setUTCHours": "setUTCHours",
	"setUTCMilliseconds": "setUTCMilliseconds", "setUTCMinutes": "setUTCMinutes", "setUTCMonth": "setUTCMonth",
	"setUTCSeconds": "setUTCSeconds", "setYear": "setYear", "toDateString": "toDateString",
	"toGMTString": "toUTCString", "toISOString": "toISOString", "toJSON": "toJSON",
	"toLocaleDateString": "toLocaleDateString", "toLocaleString": "toLocaleString",
	"toLocaleTimeString": "toLocaleTimeString", "toString": "toString", "toTimeString": "toTimeString",
	"toUTCString": "toUTCString", "valueOf": "valueOf",
}

// colorProto is d3-color's Color.prototype, which every colour class extends.
var colorProto = protoTable{
	"constructor": "Color", "copy": "copy", "displayable": "displayable", "formatHex": "color_formatHex",
	"formatHex8": "color_formatHex8", "formatHsl": "color_formatHsl", "formatRgb": "color_formatRgb",
	"hex": "color_formatHex", "toString": "color_formatRgb",
}

// rgbProto, hslProto, labProto and hclProto are d3-color's Rgb, Hsl, Lab and Hcl prototypes.
var rgbProto = protoTable{
	"brighter": "brighter", "clamp": "clamp", "constructor": "Rgb", "darker": "darker",
	"displayable": "displayable", "formatHex": "rgb_formatHex", "formatHex8": "rgb_formatHex8",
	"formatRgb": "rgb_formatRgb", "hex": "rgb_formatHex", "rgb": "rgb", "toString": "rgb_formatRgb",
}

var hslProto = protoTable{
	"brighter": "brighter", "clamp": "clamp", "constructor": "Hsl", "darker": "darker",
	"displayable": "displayable", "formatHsl": "formatHsl", "rgb": "rgb",
}

var labProto = protoTable{
	"brighter": "brighter", "constructor": "Lab", "darker": "darker", "rgb": "rgb",
}

var hclProto = protoTable{
	"brighter": "brighter", "constructor": "Hcl", "darker": "darker", "rgb": "rgb",
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

// proto is a prototype on a chain: its members, the shared function value of
// each function member, and the prototype it inherits from (nil after
// Object.prototype).
type proto struct {
	members protoTable
	funcs   map[string]jsval.Value
	parent  *proto
}

func newProto(members protoTable, parent *proto) *proto {
	p := &proto{members: members, funcs: map[string]jsval.Value{}, parent: parent}
	byName := map[string]jsval.Value{}
	for key, name := range members {
		if name == "" {
			continue
		}
		if parent == nil {
			// Object.prototype's functions are jsval's, which plain objects'
			// reads answer too.
			p.funcs[key], _ = jsval.Inherited(key)
			continue
		}
		if _, ok := byName[name]; !ok {
			byName[name] = jsval.NativeFunction(name)
		}
		p.funcs[key] = byName[name]
	}
	return p
}

var (
	objectP  = newProto(objectProto, nil)
	arrayP   = newProto(arrayProto, objectP)
	stringP  = newProto(stringProto, objectP)
	numberP  = newProto(numberProto, objectP)
	booleanP = newProto(booleanProto, objectP)
	regexpP  = newProto(regexpProto, objectP)
	dateP    = newProto(dateProto, objectP)
	colorP   = newProto(colorProto, objectP)
	colourPs = map[string]*proto{
		"Rgb": newProto(rgbProto, colorP), "Hsl": newProto(hslProto, colorP),
		"Lab": newProto(labProto, colorP), "Hcl": newProto(hclProto, colorP),
	}
)

// has reports whether key is a member of p or of a prototype it inherits
// from.
func (p *proto) has(key string) bool {
	for ; p != nil; p = p.parent {
		if _, ok := p.members[key]; ok {
			return true
		}
	}
	return false
}

// get is the inherited member key: its function, or undefined for a member
// that is not one (which the value answers itself) or no member at all.
func (p *proto) get(key string) jsval.Value {
	for ; p != nil; p = p.parent {
		if _, ok := p.members[key]; ok {
			return p.funcs[key] // undefined for an accessor
		}
	}
	return jsval.Undefined
}

// objectProtoMember reports whether key names a member of Object.prototype.
func objectProtoMember(key string) bool {
	_, ok := objectProto[key]
	return ok
}
