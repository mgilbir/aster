package jsval

// StringValue is vega-util's stringValue as a template literal prints it: the
// source text of a value for splicing into an expression. An array prints
// its elements (null as null, undefined as nothing, the rest recursively);
// a string or object is its JSON, with U+2028 and U+2029 escaped because the
// expression parser takes them for line terminators; anything else is
// String(v). It is the detached walk (see Walker): values an expression or
// a transform built are printed with the call's Walker (Walker.StringValue).
func StringValue(v Value) string { return Walker{}.StringValue(v) }
