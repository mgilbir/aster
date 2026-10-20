package jsval

// ObjectPrototypeHas reports whether Object.prototype has a property named
// key, which a plain object (`{}`, as a runtime context's data table is)
// answers to a read of an absent key.
func ObjectPrototypeHas(key string) bool {
	if len(key) < 7 || len(key) > 20 { // valueOf .. propertyIsEnumerable: most keys end here
		return false
	}
	_, ok := objectProtoNames[key]
	return ok
}
