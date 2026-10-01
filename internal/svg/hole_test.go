package svg

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/scene"
)

// A dataflow error stops a run before every mark exists, and a mark created at
// a later index than a missing one leaves a hole in its group's items array.
// Upstream's SVG renderer then reads the hole's marktype and throws; it does
// not draw the marks around it.
func TestGroupWithMissingMarkFailsLikeUpstream(t *testing.T) {
	sg := scene.New()
	root := sg.RootItem()
	root.Items = append(root.Items, nil) // index 0 never created
	sg.AddMark(scene.MarkDef{Type: scene.MarkRect}, nil, 1)
	_, err := Render(context.Background(), sg, Options{Width: 10, Height: 10})
	if err == nil || !strings.Contains(err.Error(), "Cannot read properties of undefined (reading 'marktype')") {
		t.Fatalf("err = %v, want upstream's TypeError", err)
	}
}
