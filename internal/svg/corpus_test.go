package svg

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/scene"
)

type corpusEntry struct {
	Name string `json:"name"`
	View struct {
		Width      float64    `json:"width"`
		Height     float64    `json:"height"`
		Origin     [2]float64 `json:"origin"`
		Background *string    `json:"background"`
	} `json:"view"`
	URLOptions struct {
		BaseURL         string `json:"baseURL"`
		Target          string `json:"target"`
		Rel             string `json:"rel"`
		DefaultProtocol string `json:"defaultProtocol"`
	} `json:"urlOptions"`
	Scene json.RawMessage `json:"scene"`
	SVG   string          `json:"svg"`
}

// loadCorpus reads the golden vectors recorded from upstream Vega by
// ../scene/testdata/gen_corpus.mjs.
func loadCorpus(t testing.TB) []corpusEntry {
	t.Helper()
	f, err := os.Open("../scene/testdata/corpus.json.gz")
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

func (e *corpusEntry) options() Options {
	o := Options{Width: e.View.Width, Height: e.View.Height, Origin: e.View.Origin}
	if e.View.Background != nil {
		o.Background = *e.View.Background
	}
	u := URLOptions{BaseURL: e.URLOptions.BaseURL, Target: e.URLOptions.Target, Rel: e.URLOptions.Rel, DefaultProtocol: e.URLOptions.DefaultProtocol}
	o.Href = DefaultHref(u)
	o.Image = func(url string) ImageInfo {
		if src, ok := SanitizeURL(url, u); ok && url != "" {
			return ImageInfo{Src: src}
		}
		return ImageInfo{}
	}
	return o
}

// firstDiff describes where two strings diverge.
func firstDiff(got, want string) string {
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	i := 0
	for i < n && got[i] == want[i] {
		i++
	}
	lo := i - 80
	if lo < 0 {
		lo = 0
	}
	hi := func(s string) int {
		h := i + 120
		if h > len(s) {
			h = len(s)
		}
		return h
	}
	return "at byte " + itoa(i) + "\n got: ..." + got[lo:hi(got)] + "\nwant: ..." + want[lo:hi(want)]
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// rawValueSpecs lists specs known to diverge because of non-numeric values in
// numeric properties. Item.Raw now reproduces upstream there, so it is empty;
// it stays as the place to record any such divergence found later.
var rawValueSpecs = map[string]string{}

func TestCorpusSVG(t *testing.T) {
	corpus := loadCorpus(t)
	fail := 0
	for _, e := range corpus {
		e := e
		t.Run(e.Name, func(t *testing.T) {
			if why, ok := rawValueSpecs[e.Name]; ok {
				t.Skip(why)
			}
			sg, err := scene.FromJSON(e.Scene)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Render(context.Background(), sg, e.options())
			if err != nil {
				t.Fatal(err)
			}
			if got != e.SVG {
				fail++
				t.Errorf("svg differs %s", firstDiff(got, e.SVG))
			}
		})
	}
	t.Logf("%d specs, %d differ", len(corpus), fail)
}
