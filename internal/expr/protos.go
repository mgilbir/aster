package expr

// The string-named members each kind of value inherits from its prototype in
// node (Object.getOwnPropertyNames of Object.prototype, Array.prototype,
// RegExp.prototype, Date.prototype), which the `in` operator finds, and the
// object literal keys vega-expression's code generator rejects (vega-util's
// DisallowedObjectProperties). TestFunctionTable checks them against
// testdata/functions.json, which testdata/gen_functions.mjs records.

// objectProtoMembers are Object.prototype's: what every object inherits.
var objectProtoMembers = map[string]bool{
	"__defineGetter__": true, "__defineSetter__": true, "__lookupGetter__": true,
	"__lookupSetter__": true, "__proto__": true, "constructor": true, "hasOwnProperty": true,
	"isPrototypeOf": true, "propertyIsEnumerable": true, "toLocaleString": true, "toString": true,
	"valueOf": true,
}

// arrayProtoMembers are Array.prototype's.
var arrayProtoMembers = map[string]bool{
	"at": true, "concat": true, "constructor": true, "copyWithin": true, "entries": true,
	"every": true, "fill": true, "filter": true, "find": true, "findIndex": true, "findLast": true,
	"findLastIndex": true, "flat": true, "flatMap": true, "forEach": true, "includes": true,
	"indexOf": true, "join": true, "keys": true, "lastIndexOf": true, "length": true, "map": true,
	"pop": true, "push": true, "reduce": true, "reduceRight": true, "reverse": true, "shift": true,
	"slice": true, "some": true, "sort": true, "splice": true, "toLocaleString": true,
	"toReversed": true, "toSorted": true, "toSpliced": true, "toString": true, "unshift": true,
	"values": true, "with": true,
}

// regexpProtoMembers are RegExp.prototype's.
var regexpProtoMembers = map[string]bool{
	"compile": true, "constructor": true, "dotAll": true, "exec": true, "flags": true, "global": true,
	"hasIndices": true, "ignoreCase": true, "multiline": true, "source": true, "sticky": true,
	"test": true, "toString": true, "unicode": true, "unicodeSets": true,
}

// dateProtoMembers are Date.prototype's.
var dateProtoMembers = map[string]bool{
	"constructor": true, "getDate": true, "getDay": true, "getFullYear": true, "getHours": true,
	"getMilliseconds": true, "getMinutes": true, "getMonth": true, "getSeconds": true,
	"getTime": true, "getTimezoneOffset": true, "getUTCDate": true, "getUTCDay": true,
	"getUTCFullYear": true, "getUTCHours": true, "getUTCMilliseconds": true, "getUTCMinutes": true,
	"getUTCMonth": true, "getUTCSeconds": true, "getYear": true, "setDate": true, "setFullYear": true,
	"setHours": true, "setMilliseconds": true, "setMinutes": true, "setMonth": true,
	"setSeconds": true, "setTime": true, "setUTCDate": true, "setUTCFullYear": true,
	"setUTCHours": true, "setUTCMilliseconds": true, "setUTCMinutes": true, "setUTCMonth": true,
	"setUTCSeconds": true, "setYear": true, "toDateString": true, "toGMTString": true,
	"toISOString": true, "toJSON": true, "toLocaleDateString": true, "toLocaleString": true,
	"toLocaleTimeString": true, "toString": true, "toTimeString": true,
	"toUTCString": true, "valueOf": true,
}

// disallowedProperties are the object literal keys code generation rejects:
// Object.prototype's methods, __proto__ and then.
var disallowedProperties = map[string]bool{
	"__defineGetter__": true, "__defineSetter__": true, "__lookupGetter__": true,
	"__lookupSetter__": true, "__proto__": true, "constructor": true, "hasOwnProperty": true,
	"isPrototypeOf": true, "propertyIsEnumerable": true, "then": true, "toLocaleString": true,
	"toString": true, "valueOf": true,
}
