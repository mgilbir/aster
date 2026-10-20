package jsval

// The members each built-in prototype holds in node 24
// (Object.getOwnPropertyNames of it), with the name and length of the
// function each is: the one table of them, which the evaluator's `in`
// operator and property reads (through Member and Prop) answer from.
// TestPrototypeNamesMatchNode checks it against testdata/protonames.json,
// which testdata/gen_protonames.mjs records from node.

// ProtoMember is one member of a prototype: Fn is the name of the function
// it holds ("" for a member that is not one, such as an array's length or
// RegExp.prototype.source, which a value of the kind answers itself), Arity
// that function's length.
type ProtoMember struct {
	Fn    string
	Arity int
}

// ProtoTable maps the members of one prototype to what they are.
type ProtoTable map[string]ProtoMember

// Proto is a prototype on a chain: its members, the one inert function that
// stands for each function member (two reads of a member are the same value,
// and two names of one function, such as trimLeft and trimStart, the same
// function, as in JavaScript), and the prototype it inherits from (nil after
// Object.prototype). The functions print as native code and are never
// called: the engine runs no code a specification supplies.
type Proto struct {
	members ProtoTable
	funcs   map[string]Value
	parent  *Proto
	// longest is the length of the longest member of p and of the
	// prototypes it inherits from: a longer key is no member, answered
	// without hashing it, so a read with a megabyte key costs what a short
	// one does (a map lookup hashes its whole key, on every item a pluck or
	// a path reads).
	longest int
}

// NewProto makes the prototype with these members that inherits from parent.
func NewProto(members ProtoTable, parent *Proto) *Proto {
	p := &Proto{members: members, funcs: make(map[string]Value, len(members)), parent: parent}
	if parent != nil {
		p.longest = parent.longest
	}
	for key := range members {
		p.longest = max(p.longest, len(key))
	}
	type fnKey struct {
		name  string
		arity int
	}
	made := map[fnKey]Value{}
	for key, m := range members {
		if m.Fn == "" {
			continue
		}
		k := fnKey{m.Fn, m.Arity}
		f, ok := made[k]
		if !ok {
			f = NativeMethod(m.Fn, m.Arity)
			made[k] = f
		}
		p.funcs[key] = f
	}
	return p
}

// Has reports whether key is a member of p or of a prototype it inherits
// from (the `in` operator).
func (p *Proto) Has(key string) bool {
	if p == nil || len(key) > p.longest {
		return false
	}
	for ; p != nil; p = p.parent {
		if _, ok := p.members[key]; ok {
			return true
		}
	}
	return false
}

// Get is the inherited member key: its function, or undefined for a member
// that is no function (which the value answers itself) or no member at all.
func (p *Proto) Get(key string) Value {
	if p == nil || len(key) > p.longest {
		return Undefined
	}
	for ; p != nil; p = p.parent {
		if _, ok := p.members[key]; ok {
			return p.funcs[key] // undefined for an accessor
		}
	}
	return Undefined
}

// own is the function p itself holds under key.
func (p *Proto) own(key string) (Value, bool) {
	if len(key) > p.longest {
		return Undefined, false
	}
	f, ok := p.funcs[key]
	return f, ok
}

// protoTable builds a table from the lengths of the methods (-1 for a member
// that is no function), the names of the functions that differ from the
// member's, and the name of the prototype's constructor.
func protoTable(ctor string, arity map[string]int, names map[string]string) ProtoTable {
	t := make(ProtoTable, len(arity))
	for key, n := range arity {
		if n < 0 {
			t[key] = ProtoMember{Arity: -1}
			continue
		}
		fn := key
		if key == "constructor" {
			fn = ctor
		} else if alias, ok := names[key]; ok {
			fn = alias
		}
		t[key] = ProtoMember{Fn: fn, Arity: n}
	}
	return t
}

// The built-in prototypes. Object.prototype's functions are the ones plain
// objects' reads answer too (Inherited).
var (
	ObjectProto   = NewProto(objectProtoTable, nil)
	ArrayProto    = NewProto(arrayProtoTable, ObjectProto)
	StringProto   = NewProto(stringProtoTable, ObjectProto)
	NumberProto   = NewProto(numberProtoTable, ObjectProto)
	BooleanProto  = NewProto(booleanProtoTable, ObjectProto)
	RegExpProto   = NewProto(regexpProtoTable, ObjectProto)
	DateProto     = NewProto(dateProtoTable, ObjectProto)
	functionProto = NewProto(functionProtoTable, ObjectProto)
)

var (
	objectProtoTable = protoTable("Object", map[string]int{
		"__defineGetter__": 2, "__defineSetter__": 2, "__lookupGetter__": 1, "__lookupSetter__": 1, "__proto__": -1,
		"constructor": 1, "hasOwnProperty": 1, "isPrototypeOf": 1, "propertyIsEnumerable": 1, "toLocaleString": 0,
		"toString": 0, "valueOf": 0,
	}, nil)
	arrayProtoTable = protoTable("Array", map[string]int{
		"at": 1, "concat": 1, "constructor": 1, "copyWithin": 2, "entries": 0, "every": 1, "fill": 1, "filter": 1,
		"find": 1, "findIndex": 1, "findLast": 1, "findLastIndex": 1, "flat": 0, "flatMap": 1, "forEach": 1,
		"includes": 1, "indexOf": 1, "join": 1, "keys": 0, "lastIndexOf": 1, "length": -1, "map": 1, "pop": 0,
		"push": 1, "reduce": 1, "reduceRight": 1, "reverse": 0, "shift": 0, "slice": 2, "some": 1, "sort": 1,
		"splice": 2, "toLocaleString": 0, "toReversed": 0, "toSorted": 1, "toSpliced": 2, "toString": 0,
		"unshift": 1, "values": 0, "with": 2,
	}, nil)
	stringProtoTable = protoTable("String", map[string]int{
		"anchor": 1, "at": 1, "big": 0, "blink": 0, "bold": 0, "charAt": 1, "charCodeAt": 1, "codePointAt": 1,
		"concat": 1, "constructor": 1, "endsWith": 1, "fixed": 0, "fontcolor": 1, "fontsize": 1, "includes": 1,
		"indexOf": 1, "isWellFormed": 0, "italics": 0, "lastIndexOf": 1, "length": -1, "link": 1,
		"localeCompare": 1, "match": 1, "matchAll": 1, "normalize": 0, "padEnd": 1, "padStart": 1, "repeat": 1,
		"replace": 2, "replaceAll": 2, "search": 1, "slice": 2, "small": 0, "split": 2, "startsWith": 1,
		"strike": 0, "sub": 0, "substr": 2, "substring": 2, "sup": 0, "toLocaleLowerCase": 0,
		"toLocaleUpperCase": 0, "toLowerCase": 0, "toString": 0, "toUpperCase": 0, "toWellFormed": 0, "trim": 0,
		"trimEnd": 0, "trimLeft": 0, "trimRight": 0, "trimStart": 0, "valueOf": 0,
	}, map[string]string{"trimLeft": "trimStart", "trimRight": "trimEnd"})
	numberProtoTable = protoTable("Number", map[string]int{
		"constructor": 1, "toExponential": 1, "toFixed": 1, "toLocaleString": 0, "toPrecision": 1, "toString": 1,
		"valueOf": 0,
	}, nil)
	booleanProtoTable = protoTable("Boolean", map[string]int{
		"constructor": 1, "toString": 0, "valueOf": 0,
	}, nil)
	regexpProtoTable = protoTable("RegExp", map[string]int{
		"compile": 2, "constructor": 2, "dotAll": -1, "exec": 1, "flags": -1, "global": -1, "hasIndices": -1,
		"ignoreCase": -1, "multiline": -1, "source": -1, "sticky": -1, "test": 1, "toString": 0, "unicode": -1,
		"unicodeSets": -1,
	}, nil)
	dateProtoTable = protoTable("Date", map[string]int{
		"constructor": 7, "getDate": 0, "getDay": 0, "getFullYear": 0, "getHours": 0, "getMilliseconds": 0,
		"getMinutes": 0, "getMonth": 0, "getSeconds": 0, "getTime": 0, "getTimezoneOffset": 0, "getUTCDate": 0,
		"getUTCDay": 0, "getUTCFullYear": 0, "getUTCHours": 0, "getUTCMilliseconds": 0, "getUTCMinutes": 0,
		"getUTCMonth": 0, "getUTCSeconds": 0, "getYear": 0, "setDate": 1, "setFullYear": 3, "setHours": 4,
		"setMilliseconds": 1, "setMinutes": 3, "setMonth": 2, "setSeconds": 2, "setTime": 1, "setUTCDate": 1,
		"setUTCFullYear": 3, "setUTCHours": 4, "setUTCMilliseconds": 1, "setUTCMinutes": 3, "setUTCMonth": 2,
		"setUTCSeconds": 2, "setYear": 1, "toDateString": 0, "toGMTString": 0, "toISOString": 0, "toJSON": 1,
		"toLocaleDateString": 0, "toLocaleString": 0, "toLocaleTimeString": 0, "toString": 0, "toTimeString": 0,
		"toUTCString": 0, "valueOf": 0,
	}, map[string]string{"toGMTString": "toUTCString"})
	functionProtoTable = protoTable("Function", map[string]int{
		"apply": 2, "arguments": -1, "bind": 1, "call": 1, "caller": -1, "constructor": 1, "length": -1, "name": -1,
		"toString": 0,
	}, nil)
)

// protoFuncs maps an inherited member to the function that stands for it.
var protoFuncs = ObjectProto.funcs
