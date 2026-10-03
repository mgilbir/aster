package vega

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/geo"
	"github.com/mgilbir/aster/internal/jsval"
)

// This file ports vega-loader: data formats (json, csv/tsv/dsv, topojson), the
// `parse` type coercions, and type inference.

// seedCollect gives a Collect its initial tuples: literal tuples, values
// parsed with their format, or the result of loading a url. Like upstream,
// values are ingested when the dataflow is created and requests complete before
// the first evaluation.
func (v *runView) seedCollect(c *rtContext, n *opNode, e *entry) {
	var data []jsval.Value
	switch {
	case e.literal != nil:
		data = make([]jsval.Value, len(e.literal))
		var cl jsval.Cloner
		for i, t := range e.literal {
			data[i] = ingestTupleWith(&cl, t)
		}
	case e.ingest != nil && e.ingest.request:
		data = v.request(e.ingest.url, e.ingest.format)
		ingestRows(data)
	case e.ingest != nil:
		data = v.parseValues(e.ingest.values, e.ingest.format)
		ingestRows(data)
	}
	v.checkRows(len(data), v.rowExcess(data))
	n.value = data
	v.g.pulseInput(n, &flowPulse{tuples: data, changed: true})
}

// ingestRows ingests rows that arrive through a change set. ChangeSet.pulse
// reads the tuple id of every added row before ingesting it, which throws for
// a null row (a primitive row is fine and is wrapped).
func ingestRows(data []jsval.Value) {
	var cl jsval.Cloner
	for i := range data {
		if data[i].IsNull() {
			fail("Cannot read properties of null (reading 'Symbol(vega_id)')")
		}
		data[i] = ingestTupleWith(&cl, data[i])
	}
}

// checkRows records n more rows and the excess bytes by which they weigh more
// than the budget counts them for (rowExcess), failing past the row budget.
func (v *runView) checkRows(n int, excess int64) {
	if err := v.bud.AddRows(n); err != nil || v.bud.AddRowExcess(excess) != nil {
		failLimit("data exceeds %d rows", v.limits.MaxRows)
	}
}

// parseValues is Dataflow.parse: read inline values with a format. Unlike a
// url's body, which Dataflow.request catches, a failure here is thrown: out of
// the runtime when the data set is made, or out of the Load transform.
func (v *runView) parseValues(values, fmtSpec jsval.Value) []jsval.Value {
	data, err := v.read(values, nil, fmtSpec)
	if err != nil {
		fail("%s", err.Error())
	}
	return data
}

// request is Dataflow.request: load a url, then parse it. A failed load is a
// warning and the data set is empty. A body that does not parse is a warning
// too, but the data stays what was loaded: the data set is that text, as one
// tuple.
func (v *runView) request(url, fmtSpec jsval.Value) []jsval.Value {
	if !url.IsStr() {
		v.g.warn("Loading failed: no url")
		return nil
	}
	uri := url.StrValue()
	if v.loader == nil {
		v.g.warn("Loading failed " + uri + ": no loader")
		return nil
	}
	href, err := v.loader.Sanitize(v.ctx, uri)
	if err != nil {
		v.g.warn("Loading failed " + uri + ": " + err.Error())
		return nil
	}
	body, err := v.loader.Load(v.ctx, href)
	if err != nil {
		if cerr := v.ctx.Err(); cerr != nil {
			failErr(cerr)
		}
		// A body over the loader's size cap is a resource limit, which ends
		// the render like the budget below, not a failed load.
		if errors.Is(err, budget.ErrLimit) {
			failErr(err)
		}
		v.g.warn("Loading failed " + uri + ": " + err.Error())
		return nil
	}
	if err := v.bud.Load(int64(len(body))); err != nil {
		failErr(err)
	}
	data, err := v.read(jsval.Undefined, body, fmtSpec)
	if err != nil {
		v.g.warn("Data ingestion failed " + uri + ": " + err.Error())
		// The data stays what was loaded, and the data set is that one value:
		// the text, which a tuple wraps as {data: text}.
		return []jsval.Value{jsval.Str(string(body))}
	}
	return data
}

// read is vega-loader's read: decode with the format reader, then apply `parse`.
// Exactly one of value (already decoded JSON) and raw (text) is used.
func (v *runView) read(value jsval.Value, raw []byte, schema jsval.Value) ([]jsval.Value, error) {
	typ := "json"
	if t := schema.Get("type"); t.IsTruthy() {
		typ = t.AsString()
	}
	var data []jsval.Value
	var columns []string
	var err error
	switch typ {
	case "json":
		data, err = readJSON(value, raw, schema, v.limits.MaxParseBytes)
	case "csv", "tsv", "dsv":
		delim := ","
		switch typ {
		case "tsv":
			delim = "\t"
		case "dsv":
			delim = schema.Get("delimiter").AsString()
			if delim == "" {
				return nil, fmt.Errorf("dsv format requires a delimiter")
			}
		}
		text := string(raw)
		if !value.IsUndefined() {
			text = value.AsString()
		}
		if hdr := schema.Get("header"); hdr.IsArr() {
			parts := make([]string, 0, hdr.Len())
			for _, h := range hdr.Items() {
				parts = append(parts, stringValue(h))
			}
			text = strings.Join(parts, delim[:1]) + "\n" + text
		}
		left := v.bud.RowsLeft()
		data, columns, err = parseDSVLimit(v.ctx, text, delim[0], int(min(left, math.MaxInt32)))
		if err != nil {
			if cerr := v.ctx.Err(); cerr != nil {
				failErr(cerr)
			}
			failErr(err)
		}
	case "topojson":
		var doc jsval.Value
		doc, err = jsonDocument(value, raw, schema, v.limits.MaxParseBytes)
		if err != nil {
			break
		}
		data, err = geo.TopologyFeatures(doc, schema.Get("feature").AsString(), schema.Get("mesh").AsString(), schema.Get("filter").AsString())
		if err == nil && schema.Get("feature").IsNullish() && schema.Get("mesh").IsNullish() {
			err = fmt.Errorf("Missing TopoJSON feature or mesh parameter.")
		}
	default:
		return nil, fmt.Errorf("Unknown data format type: %s", typ)
	}
	if err != nil {
		// A document past a resource limit ends the render; it is not a
		// body that failed to parse.
		if errors.Is(err, budget.ErrLimit) {
			failErr(err)
		}
		return nil, err
	}
	if p := schema.Get("parse"); !p.IsNullish() && p.IsTruthy() {
		if err := v.applyParse(data, columns, p); err != nil {
			return nil, err
		}
	}
	return data, nil
}

// jsonDocument decodes a JSON payload (or takes an already decoded value) and
// selects the `property` path.
func jsonDocument(value jsval.Value, raw []byte, schema jsval.Value, limit int64) (jsval.Value, error) {
	var doc jsval.Value
	if value.IsUndefined() {
		d, err := jsval.ParseJSONLimit(raw, limit)
		if err != nil {
			return jsval.Undefined, err
		}
		doc = d
	} else if value.IsStr() {
		d, err := jsval.ParseJSONLimit([]byte(value.StrValue()), limit)
		if err != nil {
			return jsval.Undefined, err
		}
		doc = d
	} else {
		doc = value
	}
	if prop := schema.Get("property"); prop.IsTruthy() {
		doc = walkFieldPath(doc, prop.AsString())
	}
	return doc, nil
}

func walkFieldPath(v jsval.Value, path string) jsval.Value {
	for _, seg := range jsval.ParseFieldPath(path) {
		v = propOf(v, jsval.Str(seg))
	}
	return v
}

func readJSON(value jsval.Value, raw []byte, schema jsval.Value, limit int64) ([]jsval.Value, error) {
	doc, err := jsonDocument(value, raw, schema, limit)
	if err != nil {
		return nil, err
	}
	switch doc.Kind() {
	case jsval.KindArr:
		return append([]jsval.Value(nil), doc.Items()...), nil
	case jsval.KindUndefined, jsval.KindNull:
		return nil, nil
	}
	// A single value is one tuple: the changeset that inserts it wraps it in
	// an array (vega-util's array()).
	return []jsval.Value{doc}, nil
}

// parseDSV is d3-dsv's parse: the first row names the columns, later rows
// become objects, and missing cells are empty strings.
func parseDSV(text string, delim byte) ([]jsval.Value, []string) {
	rows, cols, _ := parseDSVLimit(context.Background(), text, delim, math.MaxInt)
	return rows, cols
}

// parseDSVLimit is parseDSV that fails, before creating the row that would
// exceed it, once maxRows rows (or 16 cells per allowed row) exist, and stops
// when ctx is cancelled: a small file of one-character rows would otherwise
// cost gigabytes before any row budget is looked at.
func parseDSVLimit(ctx context.Context, text string, delim byte, maxRows int) ([]jsval.Value, []string, error) {
	N := len(text)
	I := 0
	eof := N <= 0
	eol := false
	if N > 0 && text[N-1] == '\n' {
		N--
	}
	if N > 0 && text[N-1] == '\r' {
		N--
	}
	at := func(k int) int {
		if k < 0 || k >= len(text) {
			return -1
		}
		return int(text[k])
	}
	const (
		tokValue = iota
		tokEOL
		tokEOF
	)
	token := func() (int, string) {
		if eof {
			return tokEOF, ""
		}
		if eol {
			eol = false
			return tokEOL, ""
		}
		j := I
		if at(j) == '"' {
			for {
				old := I
				I++
				if old < N && at(I) != '"' {
					continue
				}
				I++
				if at(I) == '"' {
					continue
				}
				break
			}
			i := I
			if i >= N {
				eof = true
			} else {
				c := at(I)
				I++
				if c == '\n' {
					eol = true
				} else if c == '\r' {
					eol = true
					if at(I) == '\n' {
						I++
					}
				}
			}
			end := i - 1
			if end < j+1 {
				end = j + 1
			}
			if end > len(text) {
				end = len(text)
			}
			s := ""
			if j+1 <= end {
				s = text[j+1 : end]
			}
			return tokValue, strings.ReplaceAll(s, `""`, `"`)
		}
		for I < N {
			i := I
			c := at(i)
			I++
			if c == '\n' {
				eol = true
			} else if c == '\r' {
				eol = true
				if at(I) == '\n' {
					I++
				}
			} else if c != int(delim) {
				continue
			}
			return tokValue, text[j:i]
		}
		eof = true
		if j > N {
			return tokValue, ""
		}
		return tokValue, text[j:N]
	}

	var columns []string
	var rows []jsval.Value
	for {
		kind, s := token()
		if kind == tokEOF {
			break
		}
		var row []string
		for kind != tokEOL && kind != tokEOF {
			row = append(row, s)
			kind, s = token()
		}
		if columns == nil {
			columns = row
			if columns == nil {
				columns = []string{}
			}
			continue
		}
		if len(rows) >= maxRows || (len(rows) > 0 && len(rows)*len(columns) > 16*maxRows && maxRows < math.MaxInt/16) {
			return nil, nil, fmt.Errorf("%w: data exceeds the limit of %d rows", budget.ErrLimit, maxRows)
		}
		if len(rows)&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
		o := jsval.NewObject(len(columns))
		for i, name := range columns {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			o.Set(name, jsval.Str(cell))
		}
		rows = append(rows, jsval.Obj(o))
	}
	return rows, columns, nil
}

// -- parse types ---------------------------------------------------------------

func (v *runView) applyParse(data []jsval.Value, columns []string, types jsval.Value) error {
	if len(data) == 0 {
		return nil
	}
	fields := columns
	if fields == nil {
		if o := data[0].ObjValue(); o != nil {
			fields = append([]string(nil), o.Keys()...)
		}
	}
	var typeOf map[string]string
	var order []string
	if types.IsStr() && types.StrValue() == "auto" {
		typeOf = inferTypes(data, fields)
		order = make([]string, 0, len(typeOf))
		for _, f := range fields {
			if _, dup := typeOf[f]; dup && !contains(order, f) {
				order = append(order, f)
			}
		}
	} else if o := types.ObjValue(); o != nil {
		typeOf = map[string]string{}
		for i := 0; i < o.Len(); i++ {
			typeOf[o.KeyAt(i)] = o.ValueAt(i).AsString()
			order = append(order, o.KeyAt(i))
		}
	} else {
		return nil
	}
	parsers := make([]func(jsval.Value) jsval.Value, len(order))
	for i, field := range order {
		t := typeOf[field]
		if strings.HasPrefix(t, "date:") || strings.HasPrefix(t, "utc:") {
			utc := strings.HasPrefix(t, "utc:")
			pattern := t[strings.IndexByte(t, ':')+1:]
			if n := len(pattern); n >= 2 && ((pattern[0] == '\'' && pattern[n-1] == '\'') || (pattern[0] == '"' && pattern[n-1] == '"')) {
				pattern = pattern[1 : n-1]
			}
			var tp *format.TimeParser
			if utc {
				tp = v.locale.UTCParse(pattern)
			} else {
				tp = v.locale.TimeParse(pattern)
			}
			parsers[i] = func(x jsval.Value) jsval.Value {
				s := x.AsString()
				if x.IsNullish() {
					s = "null"
				}
				if ms, ok := tp.Parse(s); ok {
					return jsval.Timestamp(ms)
				}
				return jsval.Null
			}
			continue
		}
		p := v.typeParser(t)
		if p == nil {
			return fmt.Errorf("Illegal format pattern: %s:%s", field, t)
		}
		parsers[i] = p
	}
	for _, d := range data {
		o := d.ObjValue()
		if o == nil {
			continue
		}
		for j, field := range order {
			o.Set(field, parsers[j](o.Lookup(field)))
		}
	}
	return nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func (v *runView) typeParser(t string) func(jsval.Value) jsval.Value {
	switch t {
	case "boolean":
		return toBooleanValue
	case "integer", "number":
		return toNumberValue
	case "date":
		return func(x jsval.Value) jsval.Value {
			if x.IsNullish() || (x.IsStr() && x.StrValue() == "") {
				return jsval.Null
			}
			if x.IsNum() || x.IsTimestamp() {
				return x
			}
			return jsval.Num(format.ParseDate(x.AsString(), v.zone))
		}
	case "string":
		return func(x jsval.Value) jsval.Value {
			if x.IsNullish() || (x.IsStr() && x.StrValue() == "") {
				return jsval.Null
			}
			return jsval.Str(x.AsString())
		}
	case "unknown":
		return func(x jsval.Value) jsval.Value { return x }
	}
	return nil
}

// toBooleanValue is vega-util's toBoolean.
func toBooleanValue(x jsval.Value) jsval.Value {
	if x.IsNullish() || (x.IsStr() && x.StrValue() == "") {
		return jsval.Null
	}
	if !x.IsTruthy() || (x.IsStr() && (x.StrValue() == "false" || x.StrValue() == "0")) {
		return jsval.False
	}
	return jsval.True
}

// toNumberValue is vega-util's toNumber: `+x` unless empty.
func toNumberValue(x jsval.Value) jsval.Value {
	if x.IsNullish() || (x.IsStr() && x.StrValue() == "") {
		return jsval.Null
	}
	return jsval.Num(jsval.ToNumber(x))
}

// -- inference -----------------------------------------------------------------

func inferTypes(data []jsval.Value, fields []string) map[string]string {
	out := make(map[string]string, len(fields))
	for _, f := range fields {
		out[f] = inferType(data, f)
	}
	return out
}

func validValue(x jsval.Value) bool {
	return !x.IsNullish() && !(x.IsNum() && math.IsNaN(x.NumValue()))
}

func testBoolean(x jsval.Value) bool {
	if x.IsBool() {
		return true
	}
	return x.IsStr() && (x.StrValue() == "true" || x.StrValue() == "false")
}

func testNumber(x jsval.Value) bool {
	if x.IsTimestamp() {
		return false
	}
	return !math.IsNaN(jsval.ToNumber(x))
}

func testInteger(x jsval.Value) bool {
	if !testNumber(x) {
		return false
	}
	f := jsval.ToNumber(x)
	return !math.IsInf(f, 0) && f == math.Trunc(f)
}

func testDate(x jsval.Value) bool {
	if x.IsTimestamp() {
		return !math.IsNaN(x.NumValue())
	}
	var s string
	if x.IsStr() {
		s = x.StrValue()
	} else {
		s = x.AsString()
	}
	return !math.IsNaN(format.ParseDate(s, format.UTC))
}

var typeTests = [...]func(jsval.Value) bool{testBoolean, testInteger, testNumber, testDate}
var typeList = [...]string{"boolean", "integer", "number", "date"}

func inferType(data []jsval.Value, field string) string {
	if len(data) == 0 {
		return "unknown"
	}
	a := [4]int{1, 2, 3, 4}
	t := 0
	for _, d := range data {
		x := d.Get(field)
		for j := range typeTests {
			if a[j] != 0 && validValue(x) && !typeTests[j](x) {
				a[j] = 0
				t++
				if t == len(typeTests) {
					return "string"
				}
			}
		}
	}
	u := 0
	for _, x := range a {
		if u == 0 {
			u = x
		}
	}
	if u-1 < 0 {
		return "string"
	}
	return typeList[u-1]
}
