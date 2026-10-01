package svgdiff

import "testing"

func TestCompare(t *testing.T) {
	base := `<svg class="marks" width="100"><g transform="translate(5,5)"><path d="M0,0h10.5v3Z"/><text x="1">A 1.5</text></g></svg>`
	cases := []struct {
		name string
		got  string
		want bool
	}{
		{"identical", base, true},
		{"noise", `<svg class="marks" width="100"><g transform="translate(5.000001,5)"><path d="M0,0h10.500000001v3Z"/><text x="1">A 1.5</text></g></svg>`, true},
		{"moved", `<svg class="marks" width="100"><g transform="translate(6,5)"><path d="M0,0h10.5v3Z"/><text x="1">A 1.5</text></g></svg>`, false},
		{"attr missing", `<svg class="marks"><g transform="translate(5,5)"><path d="M0,0h10.5v3Z"/><text x="1">A 1.5</text></g></svg>`, false},
		{"command differs", `<svg class="marks" width="100"><g transform="translate(5,5)"><path d="M0,0H10.5v3Z"/><text x="1">A 1.5</text></g></svg>`, false},
		{"text differs", `<svg class="marks" width="100"><g transform="translate(5,5)"><path d="M0,0h10.5v3Z"/><text x="1">B 1.5</text></g></svg>`, false},
		{"extra child", `<svg class="marks" width="100"><g transform="translate(5,5)"><path d="M0,0h10.5v3Z"/><text x="1">A 1.5</text><path/></g></svg>`, false},
	}
	for _, tc := range cases {
		r, err := Compare([]byte(tc.got), []byte(base), DefaultOptions)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if r.Equal != tc.want {
			t.Errorf("%s: Equal=%v want %v (diff: %s)", tc.name, r.Equal, tc.want, r.Diff)
		}
	}
}

// Vega writes the characters XML 1.0 forbids verbatim; the engine drops them.
// The reference document is read without them, the engine's never is.
func TestForbiddenCharactersInReference(t *testing.T) {
	doc := func(text string) []byte {
		return []byte(`<svg class="marks"><text x="1" y="a` + text + `">a` + text + `b</text></svg>`)
	}
	tol := DefaultOptions
	for _, c := range []string{"\x00", "\x01", "\x1f", "￾", "￿"} {
		r, err := Compare(doc(""), doc(c), tol)
		if err != nil || !r.Equal {
			t.Errorf("%U in want: err %v, equal %v (%s)", []rune(c)[0], err, r.Equal, r.Diff)
		}
		if _, err := Compare(doc(c), doc(""), tol); err == nil {
			t.Errorf("%U in got: no parse error", []rune(c)[0])
		}
	}
	// What is not forbidden is kept, and everything else is still compared.
	if r, err := Compare(doc(""), doc("\t"), tol); err != nil || r.Equal {
		t.Errorf("a tab in want: err %v, equal %v", err, r.Equal)
	}
	if r, err := Compare([]byte(`<svg><text>ab</text></svg>`), []byte(`<svg><text>a`+"\x00"+`c</text></svg>`), tol); err != nil || r.Equal {
		t.Errorf("a dropped character hid a difference: err %v, equal %v", err, r.Equal)
	}
}
