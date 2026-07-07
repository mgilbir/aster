package svgpdf

import "testing"

func TestParseColor(t *testing.T) {
	cases := []struct {
		in   string
		want Color
	}{
		{"#000", Color{0, 0, 0}},
		{"#fff", Color{1, 1, 1}},
		{"#4c78a8", Color{0x4c / 255.0, 0x78 / 255.0, 0xa8 / 255.0}},
		{"#ddd", Color{0xdd / 255.0, 0xdd / 255.0, 0xdd / 255.0}},
		{"rgb(255, 0, 128)", Color{1, 0, 128 / 255.0}},
		{"rgb(100%, 0%, 50%)", Color{1, 0, 0.5}},
		{"white", Color{1, 1, 1}},
		{"black", Color{0, 0, 0}},
		{"steelblue", Color{70 / 255.0, 130 / 255.0, 180 / 255.0}},
		{"White", Color{1, 1, 1}}, // keywords are case-insensitive
	}
	for _, c := range cases {
		got, err := parseColor(c.in)
		if err != nil {
			t.Errorf("parseColor(%q): %v", c.in, err)
			continue
		}
		if !almostEqual(got.R, c.want.R) || !almostEqual(got.G, c.want.G) || !almostEqual(got.B, c.want.B) {
			t.Errorf("parseColor(%q): got %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseColorErrors(t *testing.T) {
	for _, s := range []string{
		"#12345",           // bad hex length
		"#gggggg",          // bad hex digits
		"rgb(1,2)",         // missing component
		"rebeccapurple",    // outside the supported keyword subset
		"hsl(120,50%,50%)", // unsupported color function
		"",
	} {
		if _, err := parseColor(s); err == nil {
			t.Errorf("parseColor(%q): expected error, got none", s)
		}
	}
}

func TestParsePaint(t *testing.T) {
	p, err := parsePaint("none")
	if err != nil || !p.None {
		t.Errorf("parsePaint(none): got %+v, err %v", p, err)
	}
	p, err = parsePaint("transparent")
	if err != nil || !p.None {
		t.Errorf("parsePaint(transparent): got %+v, err %v", p, err)
	}
	p, err = parsePaint("#4c78a8")
	if err != nil || p.None {
		t.Errorf("parsePaint(#4c78a8): got %+v, err %v", p, err)
	}
	// Gradient/pattern references must error, not degrade.
	if _, err := parsePaint("url(#gradient_0)"); err == nil {
		t.Error("parsePaint(url(#...)): expected error, got none")
	}
}

func TestFmtNum(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{-1.5, "-1.5"},
		{0.5, "0.5"},
		{200.5, "200.5"},
		{1.0 / 3.0, "0.3333"},
		{1e-9, "0"},  // rounds to zero, not exponent notation
		{-1e-9, "0"}, // never "-0"
		{124, "124"},
	}
	for _, c := range cases {
		if got := fmtNum(c.in); got != c.want {
			t.Errorf("fmtNum(%g): got %q, want %q", c.in, got, c.want)
		}
	}
}
