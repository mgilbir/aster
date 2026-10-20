package vega

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/mgilbir/aster/internal/budget"
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
	// The tuples reach the Collect as a pending pulse (Dataflow.ingest): its
	// value stays empty until it runs, so code that reads it earlier in the
	// first run (a trigger's modify() of the data set) finds no tuple yet.
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

// parseValues is Dataflow.parse of inline values, the rows a change set
// inserts (array() of what read returns). Unlike a url's body, which
// Dataflow.request catches, a failure here is thrown out of the runtime when
// the data set is made.
func (v *runView) parseValues(values, fmtSpec jsval.Value) []jsval.Value {
	data, err := v.readRows(values, nil, fmtSpec)
	if err != nil {
		fail("%s", err.Error())
	}
	return data
}

// loadValues is the Load transform's `df.parse(_.values, _.format)`, whose
// result it iterates with forEach: what read returns must be an array.
func (v *runView) loadValues(values, fmtSpec jsval.Value) []jsval.Value {
	data, err := v.read(values, nil, fmtSpec)
	if err != nil {
		fail("%s", err.Error())
	}
	if !data.IsArr() {
		if data.IsNullish() {
			fail("Cannot read properties of %s (reading 'forEach')", nullishName(data))
		}
		fail("data.forEach is not a function")
	}
	return insertRows(data)
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
	body = stripBOM(body)
	data, err := v.readRows(jsval.Undefined, body, fmtSpec)
	if err != nil {
		failed(uri, true, err)
		// The data stays what was loaded, and the data set is that one value
		// (`res.data || []`): the text, which a tuple wraps as {data: text},
		// and nothing for an empty body.
		if len(body) == 0 {
			return nil
		}
		text, err := textOf(v.ctx, body)
		if err != nil {
			failErr(err)
		}
		return []jsval.Value{jsval.Str(text)}
	}
	return data
}

// utf8BOM is the UTF-8 encoding of U+FEFF, which a text file exported by
// many Windows tools starts with.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// stripBOM is a loaded body without the one UTF-8 byte order mark it may
// start with (C39). Upstream reads a URL's body with fetch's
// Response.text(), whose UTF-8 decode drops it, so that JSON.parse reads the
// document and a CSV's first header is the column's name. vega-loader's
// file path (fs.readFile) keeps it, and then fails to parse what its HTTP
// path reads; the Loader here says nothing of the transport, so every
// loaded body is read as the HTTP path reads it.
func stripBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, utf8BOM)
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
	if !utf8.Valid(b) {
		// Text is decoded as Response.text() and TextDecoder decode it
		// (WHATWG UTF-8): one U+FFFD each maximal subpart of an invalid
		// sequence, which can make the text up to three times longer.
		n := jsval.DecodedLen(b)
		if err := budget.LedgerFrom(ctx).Alloc(int64(n)); err != nil {
			return "", err
		}
		d := make([]byte, 0, n)
		for len(b) > 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			// A chunk ends at a character: a sequence is decoded whole.
			end := min(len(b), textChunk)
			for end < len(b) && !utf8.RuneStart(b[end]) && end-min(len(b), textChunk) < utf8.UTFMax {
				end++
			}
			d = jsval.AppendDecoded(d, b[:end])
			b = b[end:]
		}
		return unsafe.String(unsafe.SliceData(d), len(d)), nil
	}
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
