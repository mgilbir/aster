package vega

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/geo"
	"github.com/mgilbir/aster/internal/jsval"
)

// vega-loader's read (read.js), its readers (formats/*.js) and its parse
// types (type.js), with the JavaScript semantics they have for a format or a
// document of any shape (ADR 0013): a format's options are read as members
// (`format.property`, `schema.type`), a document is whatever the reader
// returns (an array, an object, a string, null), and parse writes the
// fields of whatever rows that document holds, which throws the TypeError a
// strict-mode assignment throws on a primitive. What upstream throws, read
// returns as its error.

// loaderThrow is an exception the loader's JavaScript throws, raised inside
// read and returned as read's error.
type loaderThrow struct{ err error }

func loaderThrowf(format string, args ...any) {
	panic(loaderThrow{fmt.Errorf(format, args...)})
}

// loaderMember is `v[key]`: the TypeError for null or undefined, else the
// member as JavaScript reads it (an own value, a prototype's function, a
// string's or an array's elements and length).
func loaderMember(v jsval.Value, key string) jsval.Value {
	if v.IsNullish() {
		loaderThrowf("Cannot read properties of %s (reading '%s')", nullishName(v), key)
	}
	return jsval.Member(v, key)
}

// loaded is what a reader returns: data, the document as JavaScript holds
// it, and for delimited text its columns (data.columns). own reports rows
// read fresh from text, which parse may write into; rows of a specification
// or a signal are copied first, since a render does not write into its
// input.
type loaded struct {
	data       jsval.Value
	columns    []string
	hasColumns bool
	own        bool
}

// read is vega-loader's read: the reader of the format's type, then parse,
// then the columns dropped. Exactly one of value (a decoded value: inline
// values, a signal's value) and raw (a loaded body) is used. The result is
// the document as read returns it (not yet array()'d: the Load transform
// iterates it with forEach, a change set inserts array(data)).
func (v *runView) read(value jsval.Value, raw []byte, schema jsval.Value) (data jsval.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			t, ok := r.(loaderThrow)
			if !ok {
				panic(r)
			}
			data, err = jsval.Undefined, t.err
		}
	}()
	if !schema.IsTruthy() {
		schema = jsval.Undefined
	}
	ld := v.readFormat(value, raw, schema)
	if p := v.formatOption(schema, "parse"); p.IsTruthy() {
		v.parseTypes(&ld, p)
	}
	// `if (hasOwnProperty(data, 'columns')) delete data.columns`, with
	// Object.hasOwn's TypeError for null or undefined.
	if ld.data.IsNullish() {
		loaderThrowf("Cannot convert undefined or null to object")
	}
	if o := ld.data.ObjValue(); o != nil && o.Has("columns") {
		o = o.Clone()
		o.Delete("columns")
		ld.data = jsval.Obj(o)
	}
	return ld.data, nil
}

// readRows is read, then vega-util's array() of what it returns: the rows a
// change set inserts.
func (v *runView) readRows(value jsval.Value, raw []byte, schema jsval.Value) ([]jsval.Value, error) {
	data, err := v.read(value, raw, schema)
	if err != nil {
		return nil, err
	}
	return insertRows(data), nil
}

// insertRows is vega-util's array() of a document, the rows a change set
// inserts: [] for null or undefined, an array's elements (a copy, which the
// caller may write), else the one value.
func insertRows(data jsval.Value) []jsval.Value {
	switch {
	case data.IsNullish():
		return nil
	case data.IsArr():
		return append([]jsval.Value(nil), data.Items()...)
	}
	return []jsval.Value{data}
}

// readFormat is `formats(schema.type || 'json')(data, schema)`, with read's
// `schema = schema || {}` (no option of which any value inherits).
func (v *runView) readFormat(value jsval.Value, raw []byte, schema jsval.Value) loaded {
	typ := "json"
	t := v.formatOption(schema, "type")
	if t.IsTruthy() {
		// formats(name) looks the name up as a property key.
		typ = v.walk.String(t)
	}
	switch typ {
	case "json":
		return v.readJSONFormat(value, raw, schema)
	case "csv", "tsv", "dsv":
		return v.readDSVFormat(value, raw, schema, typ)
	case "topojson":
		return v.readTopoJSONFormat(value, raw, schema)
	}
	loaderThrowf("Unknown data format type: %s", typ)
	return loaded{}
}

// formatOption is `format.name` of read's schema (`schema || {}`).
func (v *runView) formatOption(schema jsval.Value, name string) jsval.Value {
	if schema.IsUndefined() {
		return jsval.Undefined
	}
	return jsval.Member(schema, name)
}

// readJSONFormat is formats/json.js: `format.property` selects with vega-util's
// field() getter, a decoded value is used as it is (an iterable that is no
// array, a string, spread to its characters) and copied through JSON when
// format.copy is set, and text is JSON.parse'd.
func (v *runView) readJSONFormat(value jsval.Value, raw []byte, schema jsval.Value) loaded {
	prop := v.formatOption(schema, "property")
	var path []string
	if prop.IsTruthy() {
		p, err := jsval.SplitAccessPathOf(prop, v.walk.String(prop))
		if err != nil {
			loaderThrowf("%s", err.Error())
		}
		path = p
	}
	get := func(d jsval.Value) jsval.Value {
		if !prop.IsTruthy() {
			return d
		}
		for _, seg := range path {
			v.work(1)
			d = loaderMember(d, seg)
		}
		return d
	}
	if value.IsArr() || value.IsObj() {
		d := get(value)
		if d.IsStr() {
			// `[...data]` of a string: its code points.
			d = v.spreadString(d.StrValue())
		}
		if v.formatOption(schema, "copy").IsTruthy() {
			// JSON.parse(JSON.stringify(data)): undefined, or a function,
			// stringifies to undefined, which JSON.parse refuses.
			if d.IsUndefined() || jsval.IsFunction(d) {
				loaderThrowf(`"undefined" is not valid JSON`)
			}
		}
		return loaded{data: d}
	}
	doc := v.parseJSONText(value, raw)
	return loaded{data: get(doc), own: true}
}

// parseJSONText is JSON.parse of a loaded body, or of String(value) for a
// decoded primitive (a string holding a document, a number), polled and
// bounded as any document.
func (v *runView) parseJSONText(value jsval.Value, raw []byte) jsval.Value {
	var doc jsval.Value
	var err error
	switch {
	case value.IsUndefined():
		doc, err = jsval.ParseJSONContext(v.ctx, raw, v.limits.MaxParseBytes)
	case value.IsStr():
		doc, err = jsval.ParseJSONStringContext(v.ctx, value.StrValue(), v.limits.MaxParseBytes)
	default:
		// JSON.parse(String(x)) of a number or a boolean is x itself.
		doc = value
	}
	if err != nil {
		if errors.Is(err, budget.ErrLimit) {
			failErr(err)
		}
		if cerr := v.ctx.Err(); cerr != nil {
			failErr(cerr)
		}
		loaderThrowf("%s", err.Error())
	}
	return doc
}

// spreadString is `[...s]`: an array of the code points of s, each a
// string. The array is charged to the run's ledger before it is made.
func (v *runView) spreadString(s string) jsval.Value {
	n := utf8.RuneCountInString(s)
	v.work(n)
	v.chargeRun(int64(n) * valueBytes)
	out := make([]jsval.Value, 0, n)
	for i := 0; i < len(s); {
		_, size := utf8.DecodeRuneInString(s[i:])
		out = append(out, jsval.Str(s[i:i+size]))
		i += size
	}
	return jsval.Arr(out)
}

// readDSVFormat is formats/dsv.js: csv and tsv set the delimiter, dsv reads
// format.delimiter; a header is `format.header.map(stringValue)` joined with
// the delimiter; d3-dsv's dsvFormat builds a RegExp around the delimiter and
// reads its first character code; the text is `data + ”`.
func (v *runView) readDSVFormat(value jsval.Value, raw []byte, schema jsval.Value, typ string) loaded {
	var delim jsval.Value
	switch typ {
	case "csv":
		delim = jsval.Str(",")
	case "tsv":
		delim = jsval.Str("\t")
	default:
		delim = v.formatOption(schema, "delimiter")
	}
	var text string
	if value.IsUndefined() {
		t, err := textOf(v.ctx, raw)
		if err != nil {
			failErr(err)
		}
		text = t
	} else {
		text = v.walk.String(value)
	}
	if hdr := v.formatOption(schema, "header"); hdr.IsTruthy() {
		if !hdr.IsArr() {
			loaderThrowf("format.header.map is not a function")
		}
		sep := ","
		if !delim.IsUndefined() {
			sep = v.walk.String(delim)
		}
		items := hdr.Items()
		v.work(len(items))
		parts := make([]string, len(items))
		for i, h := range items {
			parts[i] = v.walk.StringValue(h)
		}
		text = strings.Join(parts, sep) + "\n" + text
	}
	v.dsvDelimiterPattern(delim)
	d, ok := delim.Strict()
	if !ok || !delim.IsStr() {
		if delim.IsNullish() {
			loaderThrowf("Cannot read properties of %s (reading 'charCodeAt')", nullishName(delim))
		}
		loaderThrowf("delimiter.charCodeAt is not a function")
	}
	rows, columns, err := parseDSVLimit(v.ctx, text, d, v.bud)
	if err != nil {
		if cerr := v.ctx.Err(); cerr != nil {
			failErr(cerr)
		}
		failErr(err)
	}
	if columns == nil {
		columns = []string{}
	}
	return loaded{data: jsval.Arr(rows), columns: columns, hasColumns: true, own: true}
}

// dsvDelimiterPattern is d3-dsv's `new RegExp("[\"" + delimiter + "\n\r]")`,
// which throws the SyntaxError of a delimiter that makes it no pattern ("-!",
// whose range is out of order, or "](").
func (v *runView) dsvDelimiterPattern(delim jsval.Value) {
	var d string
	switch {
	case delim.IsStr():
		d = delim.StrValue()
	default:
		d = v.walk.String(delim)
	}
	if !strings.ContainsAny(d, `-]\[(){}*+?|^$.`) {
		// A class of plain characters always compiles.
		return
	}
	source := "[\"" + d + "\n\r]"
	led := budget.LedgerFrom(v.ctx)
	var held int64
	_, err := jsval.NewPattern(v.ctx, source, "", func(bytes int64) error {
		if bytes < 0 {
			led.Free(-bytes)
			held += bytes
			return nil
		}
		if err := led.Alloc(bytes); err != nil {
			return err
		}
		held += bytes
		return nil
	})
	led.Free(held)
	if err == nil {
		return
	}
	if errors.Is(err, budget.ErrLimit) {
		failErr(err)
	}
	if cerr := v.ctx.Err(); cerr != nil {
		failErr(cerr)
	}
	loaderThrowf("Invalid regular expression: /%s/: %s", source, err.Error())
}

// readTopoJSONFormat is formats/topojson.js: the json reader, then
// topojson-client's feature or mesh of `data.objects[property]`, the
// features of a collection or the one object.
func (v *runView) readTopoJSONFormat(value jsval.Value, raw []byte, schema jsval.Value) loaded {
	ld := v.readJSONFormat(value, raw, schema)
	feature, mesh := v.formatOption(schema, "feature"), v.formatOption(schema, "mesh")
	var property jsval.Value
	var filter jsval.Value
	useMesh := false
	switch {
	case feature.IsTruthy():
		property = feature
	case mesh.IsTruthy():
		property, useMesh = mesh, true
		filter = v.formatOption(schema, "filter")
	default:
		loaderThrowf("Missing TopoJSON feature or mesh parameter.")
	}
	name := v.walk.String(property)
	objects := loaderMember(ld.data, "objects")
	object := loaderMember(objects, name)
	if name == "__proto__" {
		object = geo.ProtoRead(objects, name) // the prototype, which is truthy
	}
	if !object.IsTruthy() {
		loaderThrowf("Invalid TopoJSON object: %s", name)
	}
	var out jsval.Value
	var err error
	if useMesh {
		out, err = geo.TopoMeshJS(v.ctx, ld.data, object, v.meshFilter(filter))
	} else {
		out, err = geo.TopoFeatureJS(v.ctx, ld.data, object)
	}
	if err != nil {
		if errors.Is(err, budget.ErrLimit) {
			failErr(err)
		}
		if cerr := v.ctx.Err(); cerr != nil {
			failErr(cerr)
		}
		loaderThrowf("%s", err.Error())
	}
	// `object && object.features || [object]`
	if out.IsTruthy() {
		if f := jsval.Member(out, "features"); f.IsTruthy() {
			return loaded{data: f, own: true}
		}
	}
	return loaded{data: jsval.ArrOf(out), own: true}
}

// meshFilter is `filters[format.filter]`: vega-loader's filters is a plain
// object, so a name it does not hold may be one of Object.prototype's
// functions, which the mesh then calls as its filter.
func (v *runView) meshFilter(name jsval.Value) geo.MeshFilterJS {
	if name.IsUndefined() {
		return geo.MeshFilterJS{}
	}
	k := v.walk.String(name)
	switch k {
	case "interior":
		return geo.MeshFilterJS{Kind: geo.MeshInterior}
	case "exterior":
		return geo.MeshFilterJS{Kind: geo.MeshExterior}
	case "__proto__":
		return geo.MeshFilterJS{Throws: "filter is not a function"}
	case "constructor", "toString":
		// Object(g) and Object.prototype.toString() are truthy for every
		// geometry: every arc is kept.
		return geo.MeshFilterJS{}
	}
	if _, ok := jsval.Inherited(k); ok {
		// Called as a plain function, with this undefined: ToObject(this)
		// throws.
		return geo.MeshFilterJS{Throws: "Cannot convert undefined or null to object"}
	}
	return geo.MeshFilterJS{}
}

// -- parse ---------------------------------------------------------------------

// parseTypes is read.js's parse(data, types): the fields to parse are
// Object.keys(types) (or of the types inferTypes infers for 'auto'), each
// field's parser is chosen by its type, and every row has each field
// written: `datum[field] = parsers[j](datum[field])`.
func (v *runView) parseTypes(ld *loaded, types jsval.Value) {
	data := ld.data
	// `if (!data.length) return;`
	if !loaderMember(data, "length").IsTruthy() {
		return
	}
	// `fields = data.columns || Object.keys(data[0])`
	var fields []jsval.Value
	if ld.hasColumns {
		fields = make([]jsval.Value, len(ld.columns))
		for i, c := range ld.columns {
			fields[i] = jsval.Str(c)
		}
	} else if c := loaderMember(data, "columns"); c.IsTruthy() {
		if !c.IsArr() {
			// Only inferTypes reads them, as fields.reduce.
			fields = nil
			if strictIs(types, "auto") {
				loaderThrowf("fields.reduce is not a function")
			}
		} else {
			fields = c.Items()
		}
	} else {
		keys := v.loaderKeys(loaderMember(data, "0"))
		fields = make([]jsval.Value, len(keys))
		for i, k := range keys {
			fields[i] = jsval.Str(k)
		}
	}
	var typeOf func(field string) jsval.Value
	var order []string
	if strictIs(types, "auto") {
		names := make([]string, len(fields))
		for i, f := range fields {
			names[i] = v.walk.String(f)
		}
		inferred := make(map[string]string, len(fields))
		for i, f := range fields {
			v.work(1)
			// `types[field] = inferType(data, field)`: inferred for every
			// field, though a string assigned to __proto__ sets nothing.
			t := v.inferFieldType(data, f)
			if names[i] != "__proto__" {
				inferred[names[i]] = t
			}
		}
		order = v.typeKeys(names)
		typeOf = func(field string) jsval.Value { return jsval.Str(inferred[field]) }
	} else {
		order = v.loaderKeys(types)
		typeOf = func(field string) jsval.Value { return jsval.Member(types, field) }
	}
	parsers := make([]loaderParser, len(order))
	for j, field := range order {
		v.work(1)
		parsers[j] = v.loaderParserFor(field, typeOf(field))
	}
	if len(order) == 0 {
		return
	}
	n := jsval.ToNumber(loaderMember(data, "length"))
	if !ld.own {
		data = v.ownedRows(data)
		ld.data = data
	}
	for i := 0; float64(i) < n; i++ {
		v.work(1 + len(order))
		datum := loaderMember(data, strconv.Itoa(i))
		for j, field := range order {
			x := loaderMember(datum, field)
			val := parsers[j].call(v, x, len(order))
			datum = v.assignField(data, i, datum, field, val)
		}
	}
}

// ownedRows is data with its rows copied, so that parse writes the copies:
// the rows of an array, or the array-index members of an object.
func (v *runView) ownedRows(data jsval.Value) jsval.Value {
	var cl jsval.Cloner
	switch data.Kind() {
	case jsval.KindArr:
		items := data.Items()
		out := make([]jsval.Value, len(items))
		for i, t := range items {
			v.work(1)
			out[i] = cloneRow(&cl, t)
		}
		return jsval.Arr(out)
	case jsval.KindObj:
		o := data.ObjValue().Clone()
		for i := 0; i < o.Len(); i++ {
			v.work(1)
			if _, ok := jsval.ArrayIndex(o.KeyAt(i)); ok {
				o.SetAt(i, cloneRow(&cl, o.ValueAt(i)))
			}
		}
		return jsval.Obj(o)
	}
	return data
}

func cloneRow(cl *jsval.Cloner, t jsval.Value) jsval.Value {
	switch t.Kind() {
	case jsval.KindObj:
		return jsval.Obj(cl.Clone(t.ObjValue()))
	case jsval.KindArr:
		return jsval.Arr(append([]jsval.Value(nil), t.Items()...))
	}
	return t
}

// assignField is `data[i][field] = val` in strict mode, datum being data[i]:
// an object's field is set, an array's element; a primitive throws the
// TypeError of an assignment to it. It returns the row as it now is.
func (v *runView) assignField(data jsval.Value, i int, datum jsval.Value, field string, val jsval.Value) jsval.Value {
	switch datum.Kind() {
	case jsval.KindObj:
		o := datum.ObjValue()
		if field == "__proto__" && !o.Has(field) {
			// The assignment is Object.prototype's __proto__ setter, which
			// sets the prototype to an object or null and ignores any other
			// value; no parser makes a prototype a row could read from.
			return datum
		}
		o.Set(field, val)
		return datum
	case jsval.KindArr:
		k, ok := jsval.ArrayIndex(field)
		if !ok {
			// A named own property of the array (jsval.Fields).
			jsval.Fields(datum).Set(field, val)
			return datum
		}
		items := datum.Items()
		if int(k) < len(items) {
			items[k] = val
			return datum
		}
		if uint64(k) > uint64(len(items))+(1<<16) {
			// A sparse write far past the end: no row is that long.
			return datum
		}
		v.work(int(k) - len(items) + 1)
		grown := make([]jsval.Value, k+1)
		copy(grown, items)
		grown[k] = val
		nd := jsval.Arr(grown)
		setIndex(data, i, nd)
		return nd
	case jsval.KindStr:
		s := datum.StrValue()
		if field == "length" {
			loaderThrowf("Cannot assign to read only property 'length' of string '%s'", s)
		}
		if k, ok := jsval.ArrayIndex(field); ok {
			if a, _ := jsval.ToArrayLike(datum); int64(k) < a.Len() {
				loaderThrowf("Cannot assign to read only property '%s' of string '%s'", field, s)
			}
		}
		loaderThrowf("Cannot create property '%s' on string '%s'", field, s)
	case jsval.KindNum:
		loaderThrowf("Cannot create property '%s' on number '%s'", field, v.walk.String(datum))
	case jsval.KindBool:
		loaderThrowf("Cannot create property '%s' on boolean '%s'", field, v.walk.String(datum))
	}
	return datum
}

// setIndex replaces data[i], a row of a document parse owns.
func setIndex(data jsval.Value, i int, row jsval.Value) {
	switch data.Kind() {
	case jsval.KindArr:
		data.Items()[i] = row
	case jsval.KindObj:
		data.ObjValue().Set(strconv.Itoa(i), row)
	}
}

// loaderKeys is Object.keys(v), with its TypeError for null or undefined:
// an object's own keys, a string's or an array's indexes, nothing for a
// number or a boolean.
func (v *runView) loaderKeys(x jsval.Value) []string {
	if x.IsNullish() {
		loaderThrowf("Cannot convert undefined or null to object")
	}
	var keys []string
	forInEach(&v.g.meter, x, func(k string, _ jsval.Value) { keys = append(keys, k) })
	return keys
}

// loaderParser is one field's parser: a typeParsers function, a time
// parser, or one of Object.prototype's functions typeParsers inherits,
// called as a method of the parsers array.
type loaderParser struct {
	kind string // typeParsers' name, "time", or the inherited function's
	tp   *format.TimeParser
}

// loaderParserFor is the parser read.js chooses for field's type.
func (v *runView) loaderParserFor(field string, typ jsval.Value) loaderParser {
	if typ.IsTruthy() {
		if !typ.IsStr() {
			loaderThrowf("type.startsWith is not a function")
		}
		t := typ.StrValue()
		if strings.HasPrefix(t, "date:") || strings.HasPrefix(t, "utc:") {
			// `type.split(/:(.+)?/, 2)[1]`: what follows the first colon, up
			// to a line terminator, which `.` does not match; undefined when
			// that is nothing.
			rest := t[strings.IndexByte(t, ':')+1:]
			if k := strings.IndexAny(rest, "\n\r  "); k >= 0 {
				rest = rest[:k]
			}
			if rest == "" {
				loaderThrowf("Cannot read properties of undefined (reading '0')")
			}
			pattern := rest
			if n := len(pattern); (pattern[0] == '\'' && pattern[n-1] == '\'') || (pattern[0] == '"' && pattern[n-1] == '"') {
				if n >= 2 {
					pattern = pattern[1 : n-1]
				} else {
					pattern = ""
				}
			}
			var tp *format.TimeParser
			if strings.HasPrefix(t, "utc:") {
				tp = v.locale.UTCParse(pattern)
			} else {
				tp = v.locale.TimeParse(pattern)
			}
			return loaderParser{kind: "time", tp: tp}
		}
	}
	// `typeParsers[type]`, a plain object's member by the type's property key.
	k := v.walk.String(typ)
	switch k {
	case "boolean", "integer", "number", "date", "string", "unknown":
		return loaderParser{kind: k}
	case "__proto__":
		return loaderParser{kind: k}
	}
	if _, ok := jsval.Inherited(k); ok {
		return loaderParser{kind: k}
	}
	loaderThrowf("Illegal format pattern: %s:%s", field, k)
	return loaderParser{}
}

// call is `parsers[j](x)`; n is parsers.length, for the inherited functions
// called with the parsers array as their receiver.
func (p loaderParser) call(v *runView, x jsval.Value, n int) jsval.Value {
	switch p.kind {
	case "time":
		// d3-time-format's parse: `string += ""`.
		ms, ok, err := p.tp.ParseErr(v.walk.String(x))
		if err != nil {
			panic(loaderThrow{err}) // d3 throws for a %c, %x or %X it cannot parse: the load fails
		}
		if ok {
			return jsval.Timestamp(ms)
		}
		return jsval.Null
	case "boolean":
		return toBooleanValue(x)
	case "integer", "number":
		return toNumberValue(x)
	case "date":
		// toDate(_) with Date.parse: a number stays, a date stays.
		if x.IsNullish() || (x.IsStr() && x.StrValue() == "") {
			return jsval.Null
		}
		if x.IsNum() || x.IsTimestamp() {
			return x
		}
		return jsval.Num(format.ParseDate(v.walk.String(x), v.zone))
	case "string":
		if x.IsNullish() || (x.IsStr() && x.StrValue() == "") {
			return jsval.Null
		}
		return jsval.Str(v.walk.String(x))
	case "unknown":
		return x
	case "__proto__":
		// typeParsers.__proto__ is Object.prototype, no function.
		loaderThrowf("parsers[j] is not a function")
	case "constructor":
		// Object(x): a new object for null or undefined, x itself for an
		// object. A primitive becomes its wrapper object upstream, which
		// jsval does not model: it stays the primitive, which reads,
		// prints and compares as the wrapper does but for typeof and ===.
		if x.IsNullish() {
			return jsval.Obj(jsval.NewObject(0))
		}
		return x
	case "toString":
		return jsval.Str("[object Array]")
	case "hasOwnProperty", "propertyIsEnumerable":
		// The parsers array's own keys: its indexes, and length (which is
		// not enumerable).
		k := v.walk.String(x)
		if idx, ok := jsval.ArrayIndex(k); ok && int(idx) < n {
			return jsval.True
		}
		return jsval.Bool(k == "length" && p.kind == "hasOwnProperty")
	case "isPrototypeOf":
		return jsval.False
	case "__defineGetter__", "__defineSetter__":
		loaderThrowf("Object.prototype.%s: Expecting function", p.kind)
	case "__lookupGetter__", "__lookupSetter__":
		if v.walk.String(x) == "__proto__" {
			if p.kind == "__lookupGetter__" {
				return jsval.NativeFunction("get __proto__")
			}
			return jsval.NativeFunction("set __proto__")
		}
		return jsval.Undefined
	case "valueOf":
		// The parsers array itself, an array of functions jsval does not
		// model (their source text); each stands as a native function.
		out := make([]jsval.Value, n)
		for i := range out {
			out[i] = jsval.NativeFunction("parse")
		}
		return jsval.Arr(out)
	case "toLocaleString":
		// The parsers' sources joined, which jsval does not model.
		parts := make([]string, n)
		for i := range parts {
			parts[i] = "function parse() { [native code] }"
		}
		return jsval.Str(strings.Join(parts, ","))
	}
	return x
}

// inferFieldType is type.js's inferType(data, field): each row's
// `values[i][field]` (the row itself for a falsy field) is tested for being a
// boolean, an integer, a number and a date; the first type every valid value
// passes, else 'string'.
func (v *runView) inferFieldType(data, field jsval.Value) string {
	n := jsval.ToNumber(loaderMember(data, "length"))
	key := v.walk.String(field)
	a := [4]int{1, 2, 3, 4}
	t := 0
	for i := 0; float64(i) < n; i++ {
		v.work(1)
		x := loaderMember(data, strconv.Itoa(i))
		if field.IsTruthy() {
			x = loaderMember(x, key)
		}
		for j := range typeTests {
			if a[j] != 0 && validValue(x) && !typeTests[j](v.walk, x) {
				a[j] = 0
				t++
				if t == len(typeTests) {
					return "string"
				}
			}
		}
	}
	for _, u := range a {
		if u != 0 {
			return typeList[u-1]
		}
	}
	return "string"
}

// -- inference -----------------------------------------------------------------

func validValue(x jsval.Value) bool {
	return !x.IsNullish() && !(x.IsNum() && math.IsNaN(x.NumValue()))
}

func testBoolean(_ jsval.Walker, x jsval.Value) bool {
	if x.IsBool() {
		return true
	}
	return x.IsStr() && (x.StrValue() == "true" || x.StrValue() == "false")
}

// testNumber is `!Number.isNaN(+_) && !(_ instanceof Date)`.
func testNumber(_ jsval.Walker, x jsval.Value) bool {
	if x.IsTimestamp() {
		return false
	}
	return !math.IsNaN(jsval.ToNumber(x))
}

func testInteger(w jsval.Walker, x jsval.Value) bool {
	if !testNumber(w, x) {
		return false
	}
	f := jsval.ToNumber(x)
	return !math.IsInf(f, 0) && f == math.Trunc(f)
}

// testDate is `!Number.isNaN(Date.parse(_))`, Date.parse of String(_).
func testDate(w jsval.Walker, x jsval.Value) bool {
	if x.IsTimestamp() {
		return !math.IsNaN(x.NumValue())
	}
	var s string
	if x.IsStr() {
		s = x.StrValue()
	} else {
		s = w.String(x)
	}
	return !math.IsNaN(format.ParseDate(s, format.UTC))
}

var typeTests = [...]func(jsval.Walker, jsval.Value) bool{testBoolean, testInteger, testNumber, testDate}
var typeList = [...]string{"boolean", "integer", "number", "date"}

// inferType is inferType over rows that are all objects (the tests').
func inferType(m *budget.Meter, w jsval.Walker, data []jsval.Value, field string) string {
	if len(data) == 0 {
		return "unknown"
	}
	a := [4]int{1, 2, 3, 4}
	t := 0
	for _, d := range data {
		charge(m, 1)
		x := d.Get(field)
		for j := range typeTests {
			if a[j] != 0 && validValue(x) && !typeTests[j](w, x) {
				a[j] = 0
				t++
				if t == len(typeTests) {
					return "string"
				}
			}
		}
	}
	for _, u := range a {
		if u != 0 {
			return typeList[u-1]
		}
	}
	return "string"
}

// applyParse is parse over rows parse may write into, with the columns of
// delimited text (nil for JSON rows).
func (v *runView) applyParse(rows []jsval.Value, columns []string, types jsval.Value) (err error) {
	defer func() {
		if r := recover(); r != nil {
			t, ok := r.(loaderThrow)
			if !ok {
				panic(r)
			}
			err = t.err
		}
	}()
	ld := loaded{data: jsval.Arr(rows), columns: columns, hasColumns: columns != nil, own: true}
	v.parseTypes(&ld, types)
	return nil
}
