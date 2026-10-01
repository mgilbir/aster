package vega

import (
	"context"
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/scene"
)

func walk(t *testing.T, m *scene.Mark, depth int) {
	for _, it := range m.Items {
		if m.Type != scene.MarkGroup {
			t.Logf("%*s%v href=%q", depth, "", m.Type, it.Href)
		}
		for _, c := range it.Items {
			walk(t, c, depth+1)
		}
	}
}

func TestZZ(t *testing.T) {
	b, err := os.ReadFile(os.Getenv("ZZ"))
	if err != nil {
		t.Skip()
	}
	res, err := renderJSON(t, context.Background(), string(b))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("warnings %q", res.Warnings)
	walk(t, res.Scenegraph.Root, 0)
}
