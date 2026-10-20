package format

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// vega-format memoizes its formatters in a plain object keyed by the
// specifier (`cache[spec] || (cache[spec] = method(spec))`), so a specifier
// that names a member of Object.prototype finds that member, which is the
// "formatter": calling it with a value is the call of that built-in with no
// receiver. A NumberFormat or TimeFormat made for such a specifier does
// that (see callMember).

// objectPrototypeMember reports whether spec names a member Object.prototype
// has (every one is truthy, so the memo returns it).
func objectPrototypeMember(spec string) bool {
	switch spec {
	case "__proto__", "constructor", "hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable",
		"toLocaleString", "toString", "valueOf", "__defineGetter__", "__defineSetter__",
		"__lookupGetter__", "__lookupSetter__":
		return true
	}
	return false
}

// callMember is the output of member called as a formatter on a value whose
// text is text (the number or the date as String(Object(value)) writes it):
// Object(v) is the value itself; toString() with no receiver is
// "[object Undefined]"; isPrototypeOf(v) is false for a primitive and, like
// every method that reads its receiver, a TypeError for an object (a date).
// Anything that is not a function (the prototype object itself) is a
// TypeError too.
func callMember(dst []byte, member, text string, object bool) []byte {
	switch member {
	case "constructor":
		return append(dst, text...)
	case "toString":
		return append(dst, "[object Undefined]"...)
	case "isPrototypeOf":
		if !object {
			return append(dst, "false"...)
		}
	case "__proto__":
		panic(&jsval.Thrown{Name: "TypeError", Msg: "format is not a function"})
	}
	panic(&jsval.Thrown{Name: "TypeError", Msg: "Cannot convert undefined or null to object"})
}
