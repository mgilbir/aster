package vega

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strings"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/geo"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/transforms"
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
		v.holdSrc(n, int64(len(e.literal)), v.rowExcess(e.literal))
		data = make([]jsval.Value, len(e.literal))
		var cl jsval.Cloner
		for i, t := range e.literal {
			v.work(1)
			data[i] = ingestTupleWith(&cl, t)
		}
	case e.ingest != nil && e.ingest.request:
		data = v.request(e.ingest.url, e.ingest.format)
		if i := v.nullRow(data); i >= 0 {
			// Upstream ingests the rows of a load as they arrive, after the
			// dataflow is made (Dataflow.preload): the TypeError rejects that
			// promise rather than throwing out of the View, so it is the
			// load that failed, reported like one. The data set is empty.
			if err := v.g.report(&LoadError{URL: v.walk.String(e.ingest.url), Parse: true, Err: errNullRow}); err != nil {
				failErr(err)
			}
			data = nil
		}
		v.holdSrcRows(n, data)
		v.ingestRows(data)
	case e.ingest != nil:
		data = v.parseValues(e.ingest.values, e.ingest.format)
		if v.nullRow(data) >= 0 {
			fail("%s", errNullRow.Error())
		}
		v.holdSrcRows(n, data)
		v.ingestRows(data)
	}
	n.value = data
	v.g.pulseInput(n, &flowPulse{tuples: data, changed: true})
}

// errNullRow is the TypeError a change set throws when it ingests a null row:
// ChangeSet.pulse reads the tuple id of every row it adds (a primitive row is
// fine, and is wrapped).
var errNullRow = errors.New("Cannot read properties of null (reading 'Symbol(vega_id)')")

// nullRow is the index of the first null row of data, or -1.
func (v *runView) nullRow(data []jsval.Value) int {
	for i, t := range data {
		v.work(1)
		if t.IsNull() {
			return i
		}
	}
	return -1
}

// ingestRows ingests rows that arrive through a change set, none of them
// null (nullRow).
func (v *runView) ingestRows(data []jsval.Value) {
	var cl jsval.Cloner
	for i := range data {
		v.work(1)
		data[i] = ingestTupleWith(&cl, data[i])
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

// request is Dataflow.request: load a url, then parse it. A failed load is
// reported (a *LoadError) and the data set is empty. A body that does not
// parse is reported too, but the data stays what was loaded: the data set is
// that text, as one tuple. Options.Report decides whether either ends the
// render.
func (v *runView) request(url, fmtSpec jsval.Value) []jsval.Value {
	failed := func(uri string, parse bool, err error) {
		if e := v.g.report(&LoadError{URL: uri, Parse: parse, Err: err}); e != nil {
			failErr(e)
		}
	}
	if !url.IsStr() {
		failed("", false, errors.New("no url"))
		return nil
	}
	uri := url.StrValue()
	if v.loader == nil {
		failed(uri, false, errors.New("no loader"))
		return nil
	}
	href, err := v.loader.Sanitize(v.ctx, uri)
	if err != nil {
		failed(uri, false, err)
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
		failed(uri, false, err)
		return nil
	}
	if err := v.bud.Load(int64(len(body))); err != nil {
		failErr(err)
	}
	data, err := v.read(jsval.Undefined, body, fmtSpec)
	if err != nil {
		failed(uri, true, err)
		// The data stays what was loaded, and the data set is that one value:
		// the text, which a tuple wraps as {data: text}.
		text, err := textOf(v.ctx, body)
		if err != nil {
			failErr(err)
		}
		return []jsval.Value{jsval.Str(text)}
	}
	return data
}

// textChunk is how much of a loaded body textOf copies between two polls of
// the call's context.
const textChunk = 1 << 20

// textOf is the loaded body b as a string, copied a chunk at a time with the
// call's context polled in between: a body is as large as the load budget
// allows (32 MiB under WithMemoryLimit(64 MiB)), and its copy in one piece
// was the longest stretch of a call without a poll.
func textOf(ctx context.Context, b []byte) (string, error) {
	// The copy is memory the load budget, which counted the bytes, does not:
	// it is charged to the call's ledger first, and stays charged, since the
	// text (or the substrings DSV fields take of it) lives as long as the data.
	if err := budget.LedgerFrom(ctx).Alloc(int64(len(b))); err != nil {
		return "", err
	}
	if len(b) <= textChunk {
		return string(b), nil
	}
	var sb strings.Builder
	sb.Grow(len(b))
	for len(b) > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n := min(len(b), textChunk)
		sb.Write(b[:n])
		b = b[n:]
	}
	return sb.String(), nil
}

// read is vega-loader's read: decode with the format reader, then apply `parse`.
// Exactly one of value (already decoded JSON) and raw (text) is used.
func (v *runView) read(value jsval.Value, raw []byte, schema jsval.Value) ([]jsval.Value, error) {
	typ := "json"
	if t := schema.Get("type"); t.IsTruthy() {
		typ = v.walk.String(t)
	}
	var data []jsval.Value
	var columns []string
	var err error
	switch typ {
	case "json":
		data, err = readJSON(v.ctx, value, raw, schema, v.limits.MaxParseBytes)
	case "csv", "tsv", "dsv":
		delim := ","
		switch typ {
		case "tsv":
			delim = "\t"
		case "dsv":
			delim = v.walk.String(schema.Get("delimiter"))
			if delim == "" {
				return nil, fmt.Errorf("dsv format requires a delimiter")
			}
		}
		text, err := textOf(v.ctx, raw)
		if err != nil {
			failErr(err)
		}
		if !value.IsUndefined() {
			text = v.walk.String(value)
		}
		if hdr := schema.Get("header"); hdr.IsArr() {
			// joined with the whole delimiter, as vega-loader does
			parts := make([]string, 0, hdr.Len())
			for _, h := range hdr.Items() {
				parts = append(parts, v.walk.StringValue(h))
			}
			text = strings.Join(parts, delim) + "\n" + text
		}
		data, columns, err = parseDSVLimit(v.ctx, text, delim, v.bud)
		if err != nil {
			if cerr := v.ctx.Err(); cerr != nil {
				failErr(cerr)
			}
			failErr(err)
		}
	case "topojson":
		var doc jsval.Value
		doc, err = jsonDocument(v.ctx, value, raw, schema, v.limits.MaxParseBytes)
		if err != nil {
			break
		}
		// vega-loader tests `format.feature`, then `format.mesh`, for
		// truthiness: an absent one is not the object named "undefined".
		name := func(k string) string {
			if p := schema.Get(k); p.IsTruthy() {
				return v.walk.String(p)
			}
			return ""
		}
		data, err = geo.TopologyFeatures(v.ctx, doc, name("feature"), name("mesh"), v.walk.String(schema.Get("filter")))
	default:
		return nil, fmt.Errorf("Unknown data format type: %s", typ)
	}
	if err != nil {
		// A document past a resource limit ends the render, as the end of
		// the call does while a document is parsed: neither is a body that
		// failed to parse.
		if errors.Is(err, budget.ErrLimit) {
			failErr(err)
		}
		if cerr := v.ctx.Err(); cerr != nil {
			failErr(cerr)
		}
		return nil, err
	}
	if p := schema.Get("parse"); !p.IsNullish() && p.IsTruthy() {
		if typ == "json" && !value.IsUndefined() && !value.IsStr() {
			// The rows are the caller's objects (the specification's inline
			// values, or a signal's value), which upstream's parse writes
			// into. A render does not write into its input: it parses
			// copies, which ingest would make anyway.
			var cl jsval.Cloner
			for i, t := range data {
				v.work(1)
				if o := t.ObjValue(); o != nil {
					data[i] = jsval.Obj(cl.Clone(o))
				}
			}
		}
		if err := v.applyParse(data, columns, p); err != nil {
			return nil, err
		}
	}
	return data, nil
}

// jsonDocument decodes a JSON payload (or takes an already decoded value) and
// selects the `property` path. The decoding polls ctx, the call's.
func jsonDocument(ctx context.Context, value jsval.Value, raw []byte, schema jsval.Value, limit int64) (jsval.Value, error) {
	var doc jsval.Value
	if value.IsUndefined() {
		d, err := jsval.ParseJSONContext(ctx, raw, limit)
		if err != nil {
			return jsval.Undefined, err
		}
		doc = d
	} else if value.IsStr() {
		d, err := jsval.ParseJSONStringContext(ctx, value.StrValue(), limit)
		if err != nil {
			return jsval.Undefined, err
		}
		doc = d
	} else {
		doc = value
	}
	if prop := schema.Get("property"); prop.IsTruthy() {
		doc = walkFieldPath(doc, jsval.WalkerFor(ctx).String(prop))
	}
	return doc, nil
}

func walkFieldPath(v jsval.Value, path string) jsval.Value {
	for _, seg := range jsval.ParseFieldPath(path) {
		v = propOf(v, jsval.Str(seg))
	}
	return v
}

func readJSON(ctx context.Context, value jsval.Value, raw []byte, schema jsval.Value, limit int64) ([]jsval.Value, error) {
	doc, err := jsonDocument(ctx, value, raw, schema, limit)
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
// become objects, and missing cells are empty strings. The delimiter is the
// first character of delim (d3-dsv's charCodeAt(0)).
func parseDSV(text, delim string) ([]jsval.Value, []string) {
	rows, cols, _ := parseDSVLimit(context.Background(), text, delim, nil)
	return rows, cols
}

// maxDSVColumns bounds the columns of delimited text. Every row is an object
// with a field per column, so a header line a few hundred kilobytes long
// would otherwise make each line, even a blank one, cost megabytes; no
// spreadsheet writes a tenth as many.
const maxDSVColumns = 1 << 16

// dsvCellsPerRow is the cells a row may have on average when the budget only
// counts rows (Budget.RowBytes is 0): with it set, each row's weight is
// charged instead.
const dsvCellsPerRow = 16

// parseDSVLimit is parseDSV charging each row to the row budget bud (nil:
// unbounded) before it makes it, and stopping when ctx is done: a small file
// of one-character rows, or of blank lines under a wide header, would
// otherwise cost gigabytes before any budget is looked at. With
// Budget.RowBytes set, a row is charged its weight (its fields and their
// text); without, the cells of all rows are bounded to dsvCellsPerRow per row
// the budget allows.
func parseDSVLimit(ctx context.Context, text, delim string, bud *budget.Budget) ([]jsval.Value, []string, error) {
	// d3-dsv compares UTF-16 code units with the delimiter's first one; the
	// text here is UTF-8, so the delimiter is its first character, and a
	// character in the text that encodes as several bytes is compared
	// whole. (A delimiter outside the Basic Multilingual Plane is one code
	// point here and two code units there, where it can never match.)
	// An invalid byte is a character of one byte; an empty delimiter is
	// charCodeAt(0) of "", NaN, which no character equals.
	_, dn := utf8.DecodeRuneInString(delim)
	dlm := delim[:dn]
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
	// width is the bytes of the character at k, which the quoted path skips
	// past as d3 skips one code unit.
	width := func(k int) int {
		if k >= len(text) || text[k] < utf8.RuneSelf {
			return 1
		}
		_, n := utf8.DecodeRuneInString(text[k:])
		return n
	}
	isDelim := func(k int) bool {
		if dn == 0 {
			return false
		}
		if dn == 1 {
			return text[k] == dlm[0]
		}
		return strings.HasPrefix(text[k:], dlm)
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
				I += width(I)
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
			} else if !isDelim(i) {
				continue
			} else {
				I = i + dn
			}
			return tokValue, text[j:i]
		}
		eof = true
		if j > N {
			return tokValue, ""
		}
		return tokValue, text[j:N]
	}

	meter := budget.NewMeter(ctx)
	maxCells := int64(math.MaxInt64)
	if bud != nil && bud.MaxRows > 0 && bud.RowBytes <= 0 {
		maxCells = budget.Mul(int64(bud.MaxRows), dsvCellsPerRow)
	}
	var columns []string
	var rows []jsval.Value
	var cells, excess int64
	for {
		kind, s := token()
		if kind == tokEOF {
			break
		}
		var row []string
		for kind != tokEOL && kind != tokEOF {
			if len(row) >= maxDSVColumns {
				return nil, nil, fmt.Errorf("%w: delimited data with more than %d columns", budget.ErrLimit, maxDSVColumns)
			}
			row = append(row, s)
			kind, s = token()
		}
		if err := meter.Work(int64(len(row)) + 1); err != nil {
			return nil, nil, err
		}
		if columns == nil {
			columns = row
			if columns == nil {
				columns = []string{}
			}
			continue
		}
		// The row about to be made: an object with a field per column,
		// holding the text of its cells (jsval.RowCost).
		cells += int64(len(columns))
		if cells > maxCells {
			return nil, nil, fmt.Errorf("%w: delimited data cells %d exceeds the limit of %d (%d per data row)", budget.ErrLimit, cells, maxCells, dsvCellsPerRow)
		}
		if bud != nil && bud.RowBytes > 0 {
			w := jsval.ObjectCost(len(columns))
			for i := range min(len(row), len(columns)) {
				w += int64(len(row[i]))
			}
			excess += max(w-bud.RowBytes, 0)
		}
		if err := bud.ReserveWeight(int64(len(rows))+1, excess); err != nil {
			return nil, nil, err
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
			fields = o.Keys() // read, not kept: typeKeys copies it
		}
	}
	var typeOf map[string]string
	var order []string
	if types.IsStr() && types.StrValue() == "auto" {
		// Upstream's inferTypes assigns types[field] for each field in
		// turn, and the parse goes over Object.keys(types).
		order = v.typeKeys(fields)
		typeOf = inferTypes(&v.g.meter, v.walk, data, order)
	} else if o := types.ObjValue(); o != nil {
		typeOf = make(map[string]string, o.Len())
		for i := 0; i < o.Len(); i++ {
			v.work(1)
			typeOf[o.KeyAt(i)] = v.walk.String(o.ValueAt(i))
		}
		order = v.jsKeyOrder(o.Keys())
	} else {
		return nil
	}
	parsers := make([]func(jsval.Value) jsval.Value, len(order))
	for i, field := range order {
		v.work(1)
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
				s := v.walk.String(x)
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
		v.work(1 + len(order))
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

// typeKeys is Object.keys of the object upstream's inferTypes builds over
// fields: each field once, where it first appears (a CSV header may repeat a
// column), but not __proto__, whose assignment of a string sets nothing, in
// JavaScript's key order (jsKeyOrder). The repeats are found in a set, a step
// a field: a header has up to 65,536 columns, and the first row of JSON data
// as many keys as the document gives it.
func (v *runView) typeKeys(fields []string) []string {
	seen := make(map[string]struct{}, len(fields))
	keys := make([]string, 0, len(fields))
	for _, f := range fields {
		v.work(1)
		if _, dup := seen[f]; dup || f == "__proto__" {
			continue
		}
		seen[f] = struct{}{}
		keys = append(keys, f)
	}
	return v.jsKeyOrder(keys)
}

// jsKeyOrder puts keys, distinct, in the order Object.keys lists an object's
// own keys: array-index keys ascending, then the rest as they were added. It
// is the order the parsers run in, which a row missing a field gains it in,
// and which picks the field an illegal pattern names. The sort it may need is
// charged before it runs.
func (v *runView) jsKeyOrder(keys []string) []string {
	v.work(len(keys) * (1 + bits.Len(uint(len(keys)))))
	return transforms.OrderedKeys(keys)
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
			return jsval.Num(format.ParseDate(v.walk.String(x), v.zone))
		}
	case "string":
		return func(x jsval.Value) jsval.Value {
			if x.IsNullish() || (x.IsStr() && x.StrValue() == "") {
				return jsval.Null
			}
			return jsval.Str(v.walk.String(x))
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

func inferTypes(m *budget.Meter, w jsval.Walker, data []jsval.Value, fields []string) map[string]string {
	out := make(map[string]string, len(fields))
	for _, f := range fields {
		out[f] = inferType(m, w, data, f)
	}
	return out
}

func validValue(x jsval.Value) bool {
	return !x.IsNullish() && !(x.IsNum() && math.IsNaN(x.NumValue()))
}

func testBoolean(_ jsval.Walker, x jsval.Value) bool {
	if x.IsBool() {
		return true
	}
	return x.IsStr() && (x.StrValue() == "true" || x.StrValue() == "false")
}

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
