package format

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/nodetz"
)

// TestDateToStringZoneNames replays testdata/zonenames.json.gz: the zone
// name V8 prints in String(date), for every name of tzdata, at instants
// inside and outside 1970-2037 (C121, audit 2026-10-18). The zones are those
// of the tzdata release node's ICU carries (internal/nodetz), not the host's:
// the names V8 printed are answers about that release's rules (2026e makes
// Winnipeg's summers standard time, which 2026c's V8 names daylight). Every
// instant is compared, with no tolerance, but those of unresolvedTZ.
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
	if tz := doc.Get("tz").StrValue(); tz != nodetz.Version() {
		t.Fatalf("the recording is of tzdata %s, internal/nodetz of %s: re-make one of them", tz, nodetz.Version())
	}
	zones := doc.Get("zones").ObjValue()
	checked := 0
	for i := 0; i < zones.Len(); i++ {
		name := zones.KeyAt(i)
		z := Local(nodetz.MustLoad(t, name))
		if target, ok := unresolvedTZ[name]; ok {
			if zoneNameTable[name] != zoneNameTable[target] {
				t.Errorf("%s names %v, its link target %s %v", name, zoneNameTable[name], target, zoneNameTable[target])
			}
			agrees := true
			for _, c := range zones.ValueAt(i).Items() {
				agrees = agrees && z.TimezoneOffset(c.Index(0).NumValue()) == c.Index(2).NumValue()
			}
			if agrees {
				t.Errorf("%s: node's offsets are the release's now: remove it from unresolvedTZ", name)
			}
			continue
		}
		if _, ok := zoneNameTable[name]; !ok {
			t.Errorf("%s is not in zoneNameTable", name)
		}
		for _, c := range zones.ValueAt(i).Items() {
			ms, want, off := c.Index(0).NumValue(), c.Index(1).StrValue(), c.Index(2).NumValue()
			if got := z.TimezoneOffset(ms); got != off {
				t.Errorf("%s at %v: offset %v; V8 %v", name, ms, got, off)
			}
			got := DateToString(ms, z)
			if got = got[strings.IndexByte(got, '(')+1 : len(got)-1]; got != want {
				t.Errorf("%s at %v: String(date) names %q; V8 %q", name, ms, got, want)
			}
			checked++
		}
	}
	if want := zones.Len() - len(unresolvedTZ); checked < 14*want {
		t.Errorf("%d instants checked, want 14 for each of %d names", checked, want)
	}
}

// unresolvedTZ are the names node does not resolve from TZ, recorded with
// the host's offset and no DST flag though named as their link target: ICU
// canonicalises them, V8 reads the offset elsewhere. Their recorded instants
// say nothing of the release; they print their target's names.
var unresolvedTZ = map[string]string{
	"Cuba": "America/Havana",
	"Iran": "Asia/Tehran",
	"PRC":  "Asia/Shanghai",
	"ROC":  "Asia/Taipei",
	"ROK":  "Asia/Seoul",
	"W-SU": "Europe/Moscow",
}
