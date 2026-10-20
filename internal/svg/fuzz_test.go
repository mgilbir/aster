package svg

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/fuzzutil"
	"github.com/mgilbir/aster/internal/scene"
)

// FuzzRender draws arbitrary serialized scenegraphs. Whatever the text, URLs
// and attributes of the scene hold, what the renderer writes has to follow
// the grammar of its output (svgcheck.Check: its elements, under their
// parents, with its attributes) and parse back to exactly the elements,
// attributes and text runs the writer wrote, so no value made markup. It is
// the same on every run, and it may only fail with a resource limit or an
// error the scene itself causes (the sentinels of package scene, whose own
// errors start "scene:", and errMarkHole).
func FuzzRender(f *testing.F) {
	corpus := loadCorpus(f)
	slices.SortFunc(corpus, func(a, b corpusEntry) int { return len(a.Scene) - len(b.Scene) })
	// the 24 smallest scenes of the upstream corpus
	for _, e := range corpus[:24] {
		if len(e.Scene) <= fuzzutil.MaxSeedBytes {
			f.Add([]byte(e.Scene))
		}
	}
	f.Add([]byte(`{"marktype":"group","items":[{"items":[{"marktype":"text","items":[{"text":"<a&b>\u0001\ud800","href":"javascript:alert(1)","tooltip":"\"x\""}]},` +
		`{"marktype":"symbol","items":[{"fill":{"gradient":"linear","id":"g\"1","stops":[{"offset":0,"color":"red\"/>"}]},"size":-1}]}]}]}`))
	// Strings where numbers belong, each of which upstream writes into the
	// output as String(value): positions, angles and sizes in transforms and
	// style attributes, and the string-valued properties beside them.
	for _, mt := range []string{"symbol", "path", "rule", "group", "text", "image", "arc", "rect", "line", "area"} {
		for _, prop := range []string{"x", "y", "angle", "width", "fontSize", "path", "url", "href", "fill", "strokeWidth", "opacity", "strokeDash"} {
			item := `{"x":1,"y":2,"angle":30,"text":"t","url":"a.png","href":"http://a.example/",` + strconv.Quote(prop) + `:"1)\"/><script>alert(1)</script><g x=\""}`
			f.Add([]byte(`{"marktype":"group","items":[{"items":[{"marktype":"` + mt + `","items":[` + item + `,` + item + `]}]}]}`))
		}
	}
	opt := Options{Width: 200, Height: 100, MaxBytes: 4 << 20}
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzutil.Within(t, 10*time.Second, string(data), func() {
			sg, err := scene.FromJSON(data)
			if err != nil {
				return
			}
			out := renderStructure(t, sg, strconv.Quote(string(data)))
			if out == "" {
				return
			}
			if again, err := Render(context.Background(), sg, opt); err != nil || again != out {
				t.Errorf("%q: second render differs (%v)", data, err)
			}
		})
	})
}

// sceneErrors are what a scene that upstream cannot draw gives.
var sceneErrors = []error{scene.ErrUnknownInterpolate, scene.ErrUnknownOrient, scene.ErrNoShapeGenerator, scene.ErrCurveNoArea, scene.ErrInvalidPath,
	scene.ErrTextSplit, scene.ErrPathMatch, scene.ErrGradientStops, scene.ErrGradientStop}
