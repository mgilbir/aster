package jsval

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/fuzzutil"
)

// toAny is v as encoding/json decodes it into an interface value.
func toAny(v Value) any {
	switch v.Kind() {
	case KindBool:
		return v.BoolValue()
	case KindNum:
		return v.NumValue()
	case KindStr:
		return v.StrValue()
	case KindArr:
		a := make([]any, 0, v.Len())
		for _, it := range v.Items() {
			a = append(a, toAny(it))
		}
		return a
	case KindObj:
		o, m := v.ObjValue(), map[string]any{}
		for i := 0; i < o.Len(); i++ {
			m[o.KeyAt(i)] = toAny(o.ValueAt(i))
		}
		return m
	}
	return nil
}

// FuzzParseJSON holds ParseJSON to encoding/json and to its own output. The
// two accept the same documents, except where ParseJSON refuses one that
// nests past MaxJSONDepth (encoding/json's bound is 10000). Both read a
// repeated key as the last value, replace invalid UTF-8 and lone surrogate
// escapes with U+FFFD, and read a number outside float64 as +-Inf (which
// json.Unmarshal reports as an error, so those documents are not compared
// as values). What ParseJSON accepts, AppendJSON writes as valid JSON that
// parses again and writes the same bytes: non-finite numbers become null,
// so the second round is the one compared.
func FuzzParseJSON(f *testing.F) {
	for _, s := range []string{
		`{"a":[1,2.5e3,-0,"x\u00e9\ud83d\ude00",null,true,false],"a":{"b":{}}}`,
		`[1e999,-1e999,1E-999,123456789012345678901234567890,0.1e1]`,
		`"\ud800\u0041\udc00 \u0000"`, "\"\xff\xfe\"", `{"__proto__":{"x":1},"":[]}`,
		`[[[[[[[[[[1]]]]]]]]]]`, ` -`, `01`, `1.`, `.5`, `[1,]`, `{"a" 1}`, `tru`, "\"\x01\"", `[] x`,
	} {
		f.Add([]byte(s))
	}
	for _, s := range fuzzutil.Files(f, "../../testdata/*.json", "testdata/numbers.json") {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzutil.Within(t, 10*time.Second, "ParseJSON", func() { parseJSONProps(t, data) })
	})
}

func parseJSONProps(t *testing.T, data []byte) {
	v, err := ParseJSON(data)
	valid := json.Valid(data)
	if err != nil {
		var se *SyntaxError
		switch {
		case errors.Is(err, ErrJSONDepth):
			return
		case !errors.As(err, &se):
			t.Fatalf("%q: error %T %v is neither a *SyntaxError nor ErrJSONDepth", data, err, err)
		case valid:
			t.Fatalf("%q: rejected (%v) but encoding/json accepts it", data, err)
		}
		return
	}
	if !valid {
		t.Fatalf("%q: accepted, but encoding/json rejects it", data)
	}
	var want any
	if json.Unmarshal(data, &want) == nil && !reflect.DeepEqual(toAny(v), want) {
		t.Fatalf("%q: parsed as %v, encoding/json reads %v", data, toAny(v), want)
	}
	out := AppendJSON(nil, v)
	if !json.Valid(out) {
		t.Fatalf("%q: AppendJSON wrote invalid JSON %q", data, out)
	}
	v2, err := ParseJSON(out)
	if err != nil {
		t.Fatalf("%q: its own output %q does not parse: %v", data, out, err)
	}
	if out2 := AppendJSON(nil, v2); !bytes.Equal(out, out2) {
		t.Fatalf("%q: re-encoded as %q, then %q", data, out, out2)
	}
}
