package jsval

// InheritedText is String() of the member of Object.prototype named key, as
// a read of an inherited name yields it: the native-code text of a method
// (Object for the constructor), "[object Object]" for __proto__. ok is false
// for a name that is not a member.
func InheritedText(key string) (string, bool) {
	switch key {
	case "__proto__":
		return "[object Object]", true
	case "constructor":
		return "function Object() { [native code] }", true
	}
	if _, ok := Inherited(key); ok {
		return "function " + key + "() { [native code] }", true
	}
	return "", false
}
