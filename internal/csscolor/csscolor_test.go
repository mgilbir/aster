package csscolor

import "testing"

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want RGBA
		ok   bool
	}{
		{"#08f", RGBA{0, 0x88, 0xff, 1}, true},
		{"#0088ff80", RGBA{0, 0x88, 0xff, float32(0x80) / 255}, true},
		{"rgb(10, 20, 30)", RGBA{10, 20, 30, 1}, true},
		{"rgba(10 20 30 / 50%)", RGBA{10, 20, 30, 0.5}, true},
		{"hsl(0, 100%, 50%)", RGBA{255, 0, 0, 1}, true},
		{"Teal", RGBA{0, 0x80, 0x80, 1}, true},
		{"transparent", RGBA{}, true},
		{"currentColor", RGBA{}, false},
		{"#12345", RGBA{}, false},
		{"", RGBA{}, false},
	} {
		got, ok := Parse(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("Parse(%q) = %+v %v, want %+v %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
