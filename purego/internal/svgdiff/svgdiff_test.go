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
