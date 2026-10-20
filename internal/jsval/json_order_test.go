package jsval

import "testing"

// C154 and C155 (audit 2026-10-18): what JSON.stringify(JSON.parse(text))
// gives in node 24 for each text (recorded with node -e).
func TestJSONRoundTripMatchesJavaScript(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		// Array-index keys enumerate first, ascending; the rest in
		// document order (4294967295 and "01" are not indexes).
		{`{"b":1,"1":2,"a":3,"0":4,"4294967295":5,"4294967294":6,"01":7,"10":8,"-1":9,"1.5":10}`,
			`{"0":4,"1":2,"10":8,"4294967294":6,"b":1,"a":3,"4294967295":5,"01":7,"-1":9,"1.5":10}`},
		{`{"b":1,"1":2,"b":3}`, `{"1":2,"b":3}`},
		// A lone surrogate is kept, and written as its escape.
		{`["\ud83d","\ude00","😀","a\udc00b","\ud800\ud800"]`, `["\ud83d","\ude00","😀","a\udc00b","\ud800\ud800"]`},
		{`{"\udfff":1}`, `{"\udfff":1}`},
	} {
		v, err := ParseJSONString(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got := string(AppendJSON(nil, v)); got != c.want {
			t.Errorf("JSON.stringify(JSON.parse(%s))\n got %s\nwant %s", c.in, got, c.want)
		}
	}
	// A string made by an expression holds a lone surrogate the same way.
	lone := Str("x" + string(AppendSurrogate(nil, 0xDBFF)))
	if got := string(AppendJSON(nil, lone)); got != `"x\udbff"` {
		t.Errorf("JSON of a lone surrogate = %s", got)
	}
	// A high surrogate's bytes next to a low one's are their character.
	pair := Str(string(AppendSurrogate(AppendSurrogate(nil, 0xD83D), 0xDE00)))
	if got := string(AppendJSON(nil, pair)); got != `"😀"` {
		t.Errorf("JSON of a split pair = %s", got)
	}
	// An object keeps JavaScript's order however its keys are added.
	o := NewObject(0)
	for _, k := range []string{"z", "3", "a", "1", "2"} {
		o.Set(k, Null)
	}
	o.Delete("1")
	o.Set("0", Null)
	if got := string(AppendJSON(nil, Obj(o))); got != `{"0":null,"2":null,"3":null,"z":null,"a":null}` {
		t.Errorf("key order %s", got)
	}
	big := NewObject(0)
	for i := 40; i >= 0; i-- {
		big.Set(string(rune('a'+i%26))+string(rune('0'+i/26)), Num(float64(i)))
		big.Set(JSNumberString(float64(i)), Num(float64(i)))
	}
	for i := 0; i <= 40; i++ {
		if got := big.KeyAt(i); got != JSNumberString(float64(i)) {
			t.Fatalf("key %d of an indexed object is %q", i, got)
		}
		if v, _ := big.Get(JSNumberString(float64(i))); v.NumValue() != float64(i) {
			t.Fatalf("Get(%d) = %v after reordering", i, v)
		}
	}
}

// A syntax error names the character the text holds there, decoded, or the
// byte when the text is not valid UTF-8 (a BOM's first byte is not 'ï').
func TestJSONSyntaxErrorNamesTheCharacter(t *testing.T) {
	for in, want := range map[string]string{
		"\xef\xbb\xbf{}": "unexpected character '\\ufeff' at offset 0",
		"é":              "unexpected character 'é' at offset 0",
		"\xff":           "unexpected byte 0xFF at offset 0",
		"[x]":            "unexpected character 'x' at offset 1",
	} {
		_, err := ParseJSONString(in)
		if err == nil || err.Error() != "json: "+want {
			t.Errorf("ParseJSON(%+q) = %v, want json: %s", in, err, want)
		}
	}
}
