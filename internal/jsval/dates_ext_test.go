package jsval_test

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

// A Date written as text inside a value, against node 24 in UTC
// (testdata/dates.json from testdata/gen_dates.mjs): String, join, + and
// the relational operators use Date.prototype.toString, which the call's
// Walker takes from its context (jsval.ContextWithDateText); JSON.stringify
// and stringValue use toJSON, the ISO string, null for an Invalid Date.
func TestDatesInValuesAreWrittenAsJavaScriptDoes(t *testing.T) {
	raw, err := os.ReadFile("testdata/dates.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsval.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	ctx := jsval.ContextWithDateText(context.Background(), func(dst []byte, ms float64) []byte {
		return append(dst, format.DateToString(ms, format.UTC)...)
	})
	w := jsval.WalkerFor(ctx)
	if doc.Len() < 10 {
		t.Fatalf("%d vectors", doc.Len())
	}
	for _, c := range doc.Items() {
		ms := c.Get("t").NumValue()
		if c.Get("t").IsStr() {
			ms = math.NaN()
		}
		d := jsval.Timestamp(ms)
		check := func(name, got string) {
			t.Helper()
			if want := c.Get(name).StrValue(); got != want {
				t.Errorf("t=%v %s: got %q, node gives %q", ms, name, got, want)
			}
		}
		check("string", w.String(jsval.ArrOf(d, jsval.Num(1), jsval.Null, jsval.ArrOf(d))))
		check("join", w.Join([]jsval.Value{d, jsval.Str("x"), jsval.Undefined}, "|"))
		check("plus", w.String(jsval.ArrOf(d)))
		check("json", string(w.AppendJSON(nil, jsval.ArrOf(d, jsval.Obj(jsval.ObjectOf("d", d))))))
		check("jsonTop", string(w.AppendJSON(nil, d)))
		check("stringValue", w.StringValue(jsval.ArrOf(d, jsval.ArrOf(d))))
		check("stringValueTop", w.StringValue(d))
		c2, ok := w.Relate(jsval.ArrOf(d), jsval.ArrOf(jsval.Str("T")))
		if got, want := ok && c2 < 0, c.Get("less").BoolValue(); got != want {
			t.Errorf("t=%v [d] < ['T']: got %v, node gives %v", ms, got, want)
		}
		if iso, ok := format.ISOFormat(ms); ok != !math.IsNaN(ms) && math.Abs(ms) <= 8.64e15 || ok && "\""+iso+"\"" != c.Get("jsonTop").StrValue() {
			t.Errorf("t=%v ISOFormat = %q, %v", ms, iso, ok)
		}
	}
}
