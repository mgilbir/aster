package svg

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/fuzzutil"
	"github.com/mgilbir/aster/internal/scene"
)

// FuzzRender draws arbitrary serialized scenegraphs. What the renderer
// writes has to be a well-formed XML document whatever the text, URLs and
// attributes of the scene hold, the same on every run, and it may only fail
// with a resource limit or an error the scene itself causes (the sentinels of
// package scene, whose own errors start "scene:", and errMarkHole).
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
	opt := Options{Width: 200, Height: 100, MaxBytes: 4 << 20}
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzutil.Within(t, 10*time.Second, string(data), func() {
			sg, err := scene.FromJSON(data)
			if err != nil {
				return
			}
			out, err := Render(context.Background(), sg, opt)
			if err != nil {
				if !errors.Is(err, budget.ErrLimit) && !errors.Is(err, errMarkHole) && !slices.ContainsFunc(sceneErrors, func(e error) bool { return errors.Is(err, e) }) &&
					!strings.HasPrefix(err.Error(), "scene:") {
					t.Errorf("%q: unexpected error %v", data, err)
				}
				return
			}
			d := xml.NewDecoder(bytes.NewReader([]byte(out)))
			for {
				if _, err := d.Token(); err == io.EOF {
					break
				} else if err != nil {
					t.Fatalf("%q: SVG is not well-formed XML: %v\n%s", data, err, out)
				}
			}
			if again, err := Render(context.Background(), sg, opt); err != nil || again != out {
				t.Errorf("%q: second render differs (%v)", data, err)
			}
		})
	})
}

// sceneErrors are what a scene that upstream cannot draw gives.
var sceneErrors = []error{scene.ErrUnknownInterpolate, scene.ErrNoShapeGenerator, scene.ErrCurveNoArea, scene.ErrInvalidPath}
