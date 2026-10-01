package transforms

import (
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

// Accessor reads one value from a tuple.
type Accessor func(jsval.Value) jsval.Value

// Field is vega-util's accessor: a getter together with the name that output
// fields are derived from and the list of input field names it reads. The zero
// Field (nil Get) is the "no field" that upstream spells null.
type Field struct {
	Get Accessor
	// Name is accessorName: for a simple path it is the path segment, for a
	// nested path ("a.b") the whole path text, otherwise whatever the runtime
	// chose for a computed accessor (an expression's source text).
	Name string
	// Fields are the tuple fields the accessor reads (accessorFields).
	Fields []string
}

// IsNil reports whether f is the null field.
func (f Field) IsNil() bool { return f.Get == nil }

// Apply reads the field from t; the null field reads Undefined.
func (f Field) Apply(t jsval.Value) jsval.Value {
	if f.Get == nil {
		return jsval.Undefined
	}
	return f.Get(t)
}

// FieldOf builds vega-util's field(path): it splits the path with the same
// rules as jsval.ParseFieldPath and names the accessor after the single
// segment, or after the whole text for a nested path.
func FieldOf(path string) Field {
	segs := jsval.ParseFieldPath(path)
	name := path
	if len(segs) == 1 {
		name = segs[0]
	}
	return Field{Get: getter(segs), Name: name, Fields: []string{name}}
}

// FieldOfStrict is FieldOf with the accessor's JavaScript semantics for a
// missing step (see strictGetter): the accessors the dataflow runtime builds
// from specification fields, whose panics it recovers and logs.
func FieldOfStrict(path string) Field {
	f := FieldOf(path)
	f.Get = strictGetter(jsval.ParseFieldPath(path))
	return f
}

// FieldsOf maps FieldOf over paths.
func FieldsOf(paths ...string) []Field {
	out := make([]Field, len(paths))
	for i, p := range paths {
		out[i] = FieldOf(p)
	}
	return out
}

// NamedField wraps an arbitrary accessor (an expression, say) with a name and
// the fields it reads.
func NamedField(name string, fields []string, get Accessor) Field {
	return Field{Get: get, Name: name, Fields: fields}
}

// getter is vega-util's getter: plain property reads along the path. Unlike a
// JavaScript member chain it answers Undefined instead of throwing when an
// intermediate step is null or undefined, so the library never panics on
// hostile tuples; FieldOfStrict is the faithful accessor.
func getter(path []string) Accessor {
	switch len(path) {
	case 0:
		return func(v jsval.Value) jsval.Value { return v }
	case 1:
		p := path[0]
		return func(v jsval.Value) jsval.Value { return prop(v, p) }
	}
	return func(v jsval.Value) jsval.Value {
		for _, p := range path {
			v = prop(v, p)
		}
		return v
	}
}

// strictGetter reads the path as a JavaScript member chain does: a property of
// null or undefined (a missing step of a nested path, or a missing tuple) is a
// TypeError, raised as a *jsval.Thrown panic.
func strictGetter(path []string) Accessor {
	switch len(path) {
	case 0:
		return func(v jsval.Value) jsval.Value { return v }
	case 1:
		p := path[0]
		return func(v jsval.Value) jsval.Value { return propOrThrow(v, p) }
	}
	return func(v jsval.Value) jsval.Value {
		for _, p := range path {
			v = propOrThrow(v, p)
		}
		return v
	}
}

// propOrThrow is `v[key]`, a TypeError for null and undefined.
func propOrThrow(v jsval.Value, key string) jsval.Value {
	if v.IsNullish() {
		of := "undefined"
		if v.IsNull() {
			of = "null"
		}
		panic(&jsval.Thrown{Name: "TypeError", Msg: "Cannot read properties of " + of + " (reading '" + key + "')"})
	}
	return prop(v, key)
}

// prop is JavaScript's v[key] for the values datum fields hold.
func prop(v jsval.Value, key string) jsval.Value {
	switch v.Kind() {
	case jsval.KindObj:
		return v.ObjValue().Prop(key)
	case jsval.KindArr:
		if key == "length" {
			return jsval.Int(v.Len())
		}
		if i, ok := arrayIndex(key); ok {
			return v.Index(i)
		}
	case jsval.KindStr:
		if key == "length" {
			return jsval.Int(utf16Len(v.StrValue()))
		}
	}
	return jsval.Undefined
}

func arrayIndex(s string) (int, bool) {
	if s == "" || len(s) > 9 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// MeasureName is upstream's output naming rule for an aggregate-like measure:
// the explicit alias, else op + "_" + field name (just op for count).
func MeasureName(op, field, as string) string {
	if as != "" {
		return as
	}
	if field == "" {
		return op
	}
	return op + "_" + field
}

// KeyFunc maps a tuple to a group key string.
type KeyFunc func(jsval.Value) string

// appendKeyValue appends String(v) to dst without allocating for the common
// string and number cases. A date is written as Date.prototype.toString does in
// the zone z, so dates within one second share a key.
func appendKeyValue(dst []byte, v jsval.Value, z format.Zone) []byte {
	switch v.Kind() {
	case jsval.KindStr:
		return append(dst, v.StrValue()...)
	case jsval.KindNum:
		return jsval.AppendJSNumber(dst, v.NumValue())
	case jsval.KindTimestamp:
		return append(dst, format.DateToString(v.NumValue(), z)...)
	}
	return append(dst, v.AsString()...)
}

// keyString is String(v) with a date in the zone z.
func keyString(v jsval.Value, z format.Zone) string {
	if v.IsTimestamp() {
		return format.DateToString(v.NumValue(), z)
	}
	return v.AsString()
}

// keyAppender is KeyOf in append form: it writes the key of t onto dst, so a
// caller can look the key up in a map (m[string(b)]) without allocating.
func keyAppender(fields []Field, z format.Zone) func(dst []byte, t jsval.Value) []byte {
	switch len(fields) {
	case 0:
		return func(dst []byte, _ jsval.Value) []byte { return dst }
	case 1:
		g := fields[0].Get
		return func(dst []byte, t jsval.Value) []byte { return appendKeyValue(dst, g(t), z) }
	}
	return func(dst []byte, t jsval.Value) []byte {
		for i, f := range fields {
			if i > 0 {
				dst = append(dst, '|')
			}
			dst = appendKeyValue(dst, f.Get(t), z)
		}
		return dst
	}
}

// KeyOf is vega-util's key(fields): the values' string forms joined with '|',
// a date in the zone z. No fields give the constant key "".
func KeyOf(z format.Zone, fields ...Field) KeyFunc {
	if len(fields) == 0 {
		return func(jsval.Value) string { return "" }
	}
	if len(fields) == 1 {
		g := fields[0].Get
		return func(t jsval.Value) string {
			if v := g(t); v.IsStr() {
				return v.StrValue()
			} else {
				var buf [32]byte
				return string(appendKeyValue(buf[:0], v, z))
			}
		}
	}
	app := keyAppender(fields, z)
	return func(t jsval.Value) string {
		var buf [64]byte
		return string(app(buf[:0], t))
	}
}

// KeyOfPaths is KeyOf over field paths, the form the `key` helper takes.
func KeyOfPaths(z format.Zone, paths ...string) KeyFunc { return KeyOf(z, FieldsOf(paths...)...) }
