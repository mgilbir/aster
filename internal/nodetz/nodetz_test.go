package nodetz

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// The archive holds every name of the release's tzdata.zi, and that release.
func TestArchiveIsTheRelease(t *testing.T) {
	zi, err := os.ReadFile("tzdata.zi")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(zi), "\n")
	if want := "# version " + Version(); lines[0] != want {
		t.Errorf("tzdata.zi begins %q; the archive is release %s", lines[0], Version())
	}
	var names []string
	for _, l := range lines {
		switch f := strings.Fields(l); {
		case len(f) >= 2 && f[0] == "Z":
			names = append(names, f[1])
		case len(f) == 3 && f[0] == "L":
			names = append(names, f[2])
		}
	}
	slices.Sort(names)
	if got := Names(); !slices.Equal(got, names) {
		t.Errorf("the archive has %d names, tzdata.zi %d", len(got), len(names))
	}
	for _, n := range names {
		if _, err := Load(n); err != nil {
			t.Error(err)
		}
	}
}

// The zones are compiled as ICU compiles them, from the rearguard form:
// Dublin's winter is standard time and its summer daylight (the main form
// has them the other way, with a negative save), and Casablanca's +01
// outside Ramadan is daylight time (V8 names it Western European Summer
// Time), where the main form has +01 standard and Ramadan's +00 daylight.
func TestRearguard(t *testing.T) {
	for _, c := range []struct {
		zone   string
		at     time.Time
		dst    bool
		offset int
	}{
		{"Europe/Dublin", time.Date(2021, 1, 15, 12, 0, 0, 0, time.UTC), false, 0},
		{"Europe/Dublin", time.Date(2021, 7, 15, 12, 0, 0, 0, time.UTC), true, 3600},
		{"Africa/Casablanca", time.Date(2021, 1, 15, 12, 0, 0, 0, time.UTC), true, 3600},
	} {
		at := c.at.In(MustLoad(t, c.zone))
		if _, off := at.Zone(); at.IsDST() != c.dst || off != c.offset {
			t.Errorf("%s at %v: DST %v, offset %d; want %v, %d", c.zone, c.at, at.IsDST(), off, c.dst, c.offset)
		}
	}
}

func TestLoadUTC(t *testing.T) {
	for _, n := range []string{"", "UTC"} {
		if loc, err := Load(n); err != nil || loc != time.UTC {
			t.Errorf("Load(%q) = %v, %v; want time.UTC as time.LoadLocation", n, loc, err)
		}
	}
	if _, err := Load("Not/AZone"); err == nil {
		t.Error("Load accepted an unknown zone")
	}
}
