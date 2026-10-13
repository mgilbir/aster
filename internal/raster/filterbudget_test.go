package raster

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// A merge node is a pass over the filter region, as a primitive is: a merge
// of thousands of nodes is charged for each of them (C28).
func TestFilterMergeNodesCharged(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><defs><filter id="f" filterUnits="userSpaceOnUse" x="0" y="0" width="100" height="100"><feMerge>` +
		strings.Repeat(`<feMergeNode in="SourceAlpha"/>`, 3000) +
		`</feMerge></filter></defs><rect width="100" height="100" fill="red" filter="url(#f)"/></svg>`
	if _, err := Render([]byte(svg), Options{Limits: Limits{MaxPixelOps: 1 << 22}}); !errors.Is(err, errLimit) {
		t.Errorf("3,000 merge nodes over 10,000 pixels within 4 Mi pixel operations: err = %v", err)
	}
}

// Every buffer a filter allocates, its results above all, is charged to the
// canvas budget: twenty results kept alive to a final merge need twenty
// region-sized buffers, which a budget of a few canvases cannot hold (C30).
// Within the budget the image is the same, and the budget is given back.
func TestFilterIntermediatesCharged(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="200"><defs><filter id="f" filterUnits="userSpaceOnUse" x="0" y="0" width="200" height="200">`)
	for i := range 20 {
		fmt.Fprintf(&b, `<feFlood flood-color="#%02x0000" flood-opacity="0.1" result="b%d"/><feGaussianBlur in="SourceAlpha" stdDeviation="%d" result="s%d"/>`, 10*i, i, 1+i%3, i)
	}
	b.WriteString(`<feMerge>`)
	for i := range 20 {
		fmt.Fprintf(&b, `<feMergeNode in="b%d"/><feMergeNode in="s%d"/>`, i, i)
	}
	b.WriteString(`</feMerge></filter></defs><rect x="50" y="50" width="100" height="100" fill="red" filter="url(#f)"/></svg>`)
	canvas := 200 * 200 * 4
	if _, err := Render([]byte(b.String()), Options{Limits: Limits{MaxCanvasBytes: 6 * canvas}}); !errors.Is(err, errLimit) {
		t.Errorf("40 live results in a budget of 6 canvases: err = %v", err)
	}
	big, err := Render([]byte(b.String()), Options{Limits: Limits{MaxCanvasBytes: 100 * canvas}})
	if err != nil {
		t.Fatal(err)
	}
	def, err := Render([]byte(b.String()), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if string(big.Pix) != string(def.Pix) {
		t.Error("the image depends on the canvas budget")
	}
}
