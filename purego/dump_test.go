package purego_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/aster/purego"
)

// TestDumpSVG writes purego's SVG for the specs named in PUREGO_DUMP (comma
// separated paths) into PUREGO_DUMP_DIR. It is a debugging aid.
func TestDumpSVG(t *testing.T) {
	list, dir := os.Getenv("PUREGO_DUMP"), os.Getenv("PUREGO_DUMP_DIR")
	if list == "" || dir == "" {
		t.Skip("set PUREGO_DUMP and PUREGO_DUMP_DIR")
	}
	c, err := purego.New(purego.WithLoader(corpusLoader(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, f := range strings.Split(list, ",") {
		spec, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var out string
		if strings.HasSuffix(f, ".vl.json") {
			out, err = c.VegaLiteToSVG(spec)
		} else {
			out, err = c.VegaToSVG(spec)
		}
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		name := strings.TrimSuffix(filepath.Base(f), ".json") + ".svg"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
