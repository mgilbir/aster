package format

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
	_ "time/tzdata" // the golden zones must resolve on machines without zoneinfo
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

// mustLoc loads a zone from testdata/zoneinfo (tzdata 2026c, the main build
// node's ICU follows) when pinned there, else from the host. Hosts disagree on
// historical offsets: Debian and Go's zoneinfo.zip keep backzone data (e.g.
// Amsterdam's +0:19:32 LMT before 1937), macOS links Amsterdam to Brussels.
func mustLoc(t testing.TB, name string) *time.Location {
	t.Helper()
	var loc *time.Location
	data, err := os.ReadFile(filepath.Join("testdata", "zoneinfo", filepath.FromSlash(name)))
	if err == nil {
		loc, err = time.LoadLocationFromTZData(name, data)
	} else {
		loc, err = time.LoadLocation(name)
	}
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
