package scene

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

type corpusEntry struct {
	Name  string          `json:"name"`
	Scene json.RawMessage `json:"scene"`
}

// loadCorpus reads the golden vectors recorded from upstream Vega by
// testdata/gen_corpus.mjs: each entry has the scenegraph as vega left it
// (including the bounds vega computed) and the SVG it rendered.
func loadCorpus(t testing.TB) []corpusEntry {
	t.Helper()
	f, err := os.Open("testdata/corpus.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Corpus []corpusEntry `json:"corpus"`
	}
	if err := json.NewDecoder(zr).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return doc.Corpus
}

func decodeNum(v jsval.Value) float64 {
	if v.IsObj() {
		switch v.Get("$num").AsString() {
		case "NaN":
			return math.NaN()
		case "Infinity":
			return math.Inf(1)
		case "-Infinity":
			return math.Inf(-1)
		}
	}
	return v.NumValue()
}

func sameBits(a, b float64) bool {
	return a == b || (math.IsNaN(a) && math.IsNaN(b))
}

// compareBounds walks a computed mark against the recorded one.
func compareBounds(m *Mark, rec jsval.Value, path string, report func(string, ...any)) {
	check := func(what string, b *Bounds, want jsval.Value) {
		if want.Len() != 4 {
			return
		}
		got := [4]float64{b.X1, b.Y1, b.X2, b.Y2}
		for i := 0; i < 4; i++ {
			w := decodeNum(want.Index(i))
			if !sameBits(got[i], w) {
				report("%s %s bounds: got %v want %v", path, what, got, [4]float64{
					decodeNum(want.Index(0)), decodeNum(want.Index(1)), decodeNum(want.Index(2)), decodeNum(want.Index(3))})
				return
			}
		}
	}
	check("mark", &m.Bounds, rec.Get("rbounds"))
	items := rec.Get("items").Items()
	for i, it := range m.Items {
		if i >= len(items) {
			break
		}
		p := fmt.Sprintf("%s/%s[%d]", path, m.Type, i)
		check("item", &it.Bounds, items[i].Get("rbounds"))
		if m.Type == MarkGroup {
			for j, child := range it.Items {
				compareBounds(child, items[i].Get("items").Index(j), fmt.Sprintf("%s/%d", p, j), report)
			}
		}
	}
}

// TestCorpusBounds recomputes the bounds of every scenegraph in the corpus and
// requires exact agreement with the bounds Vega computed.
func TestCorpusBounds(t *testing.T) {
	skip := map[string]string{}
	// "rbounds" are upstream's bounds recomputed from scratch on the final
	// items by its own bound functions (see gen_corpus.mjs).
	corpus := loadCorpus(t)
	for _, e := range corpus {
		e := e
		t.Run(e.Name, func(t *testing.T) {
			if why, ok := skip[e.Name]; ok {
				t.Skip(why)
			}
			sg, err := FromJSON(e.Scene)
			if err != nil {
				t.Fatal(err)
			}
			if err := NewBounder(nil).BoundTree(sg.Root); err != nil {
				t.Fatal(err)
			}
			rec, err := jsval.ParseJSON(e.Scene)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			compareBounds(sg.Root, rec, "root", func(f string, a ...any) {
				n++
				if n <= 5 {
					t.Errorf(f, a...)
				}
			})
			if n > 5 {
				t.Errorf("... and %d more", n-5)
			}
		})
	}
}
