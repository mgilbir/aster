package format

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/jsval"
)

// TestDateToStringZoneNames replays testdata/zonenames.json.gz: the zone
// name V8 prints in String(date), for every name of tzdata, at instants
// inside and outside 1970-2037 (C121, audit 2026-10-18). A name the host's
// zoneinfo does not know, or whose offsets there differ from node's tzdata
// at the instant, is skipped: the names are those of node's release.
func TestDateToStringZoneNames(t *testing.T) {
	b, err := os.ReadFile("testdata/zonenames.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsval.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	zones := doc.Get("zones").ObjValue()
	checked, skipped := 0, 0
	for i := 0; i < zones.Len(); i++ {
		name := zones.KeyAt(i)
		loc, err := time.LoadLocation(name)
		if err != nil {
			skipped++
			continue
		}
		z := Local(loc)
		for _, c := range zones.ValueAt(i).Items() {
			ms, want, off := c.Index(0).NumValue(), c.Index(1).StrValue(), c.Index(2).NumValue()
			if z.TimezoneOffset(ms) != off {
				skipped++
				continue
			}
			got := DateToString(ms, z)
			if got = got[strings.IndexByte(got, '(')+1 : len(got)-1]; got != want {
				if _, known := zoneNameTable[name]; known {
					t.Errorf("%s at %v: String(date) names %q; V8 %q", name, ms, got, want)
				}
				continue
			}
			checked++
		}
	}
	if checked < 7000 {
		t.Errorf("only %d instants checked (%d skipped)", checked, skipped)
	}
}
