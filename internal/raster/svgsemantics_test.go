package raster

import "testing"

func inkedPixels(t *testing.T, svg string, scale float64) (n int, col func(x int) int) {
	t.Helper()
	img, err := Render([]byte(svg), Options{Scale: scale})
	if err != nil {
		t.Fatal(err)
	}
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 127 {
			n++
		}
	}
	return n, func(x int) int {
		c := 0
		for y := 0; y < img.Rect.Dy(); y++ {
			if img.Pix[y*img.Stride+4*x+3] > 127 {
				c++
			}
		}
		return c
	}
}

// The initial font-size is CSS's medium, 16px, as browsers, the keyword
// table and the PDF writer have it: an em of an SVG that sets no size is 16
// pixels.
func TestInitialFontSize(t *testing.T) {
	if n, _ := inkedPixels(t, `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><rect width="1em" height="1em"/></svg>`, 1); n != 256 {
		t.Errorf("a 1em square covers %d pixels, want 16x16", n)
	}
}

// vector-effect="non-scaling-stroke" strokes in the host coordinate space
// (SVG 2), as browsers do and Vega relies on for scaled path marks: the
// stroke of a path scaled 4x is its own width, times the output scale only.
func TestNonScalingStroke(t *testing.T) {
	for _, tc := range []struct {
		ve    string
		scale float64
		want  int
	}{{"", 1, 8}, {`vector-effect="non-scaling-stroke"`, 1, 2}, {`style="vector-effect: non-scaling-stroke"`, 2, 4}} {
		svg := `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40"><path d="M0 2.5H20" transform="scale(4)" fill="none" stroke="black" stroke-width="2" ` + tc.ve + `/></svg>`
		if _, col := inkedPixels(t, svg, tc.scale); col(int(40*tc.scale)) != tc.want {
			t.Errorf("%q at scale %v: the stroke is %d pixels wide, want %d", tc.ve, tc.scale, col(int(40*tc.scale)), tc.want)
		}
	}
}
