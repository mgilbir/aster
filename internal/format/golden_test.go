package format

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/nodetz"
)

// readGolden decodes a gzip-compressed JSON file recorded by testdata/gen.sh.
func readGolden(t testing.TB, name string, v any) {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if err := json.NewDecoder(zr).Decode(v); err != nil {
		t.Fatal(err)
	}
}

// goldenZones lists the recorded time zones: file suffix -> IANA name.
var goldenZones = []struct{ file, name string }{
	{"UTC", "UTC"},
	{"America_New_York", "America/New_York"},
	{"Europe_Amsterdam", "Europe/Amsterdam"},
	{"America_Sao_Paulo", "America/Sao_Paulo"},
	{"Asia_Kolkata", "Asia/Kolkata"},
	{"Australia_Lord_Howe", "Australia/Lord_Howe"},
}

// mustLoc loads a zone of the tzdata release node's recordings were made in
// (internal/nodetz), not the host's: hosts disagree on historical offsets
// (Debian and Go's zoneinfo.zip keep backzone data, e.g. Amsterdam's
// +0:19:32 LMT before 1937; macOS links Amsterdam to Brussels) and a newer
// release may change a zone's rules.
func mustLoc(t testing.TB, name string) *time.Location {
	t.Helper()
	return nodetz.MustLoad(t, name)
}
