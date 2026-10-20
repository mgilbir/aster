// Package nodetz holds the time zones node's recordings were made in: the
// tzdata release the pinned node's ICU carries (2026c), compiled as ICU
// compiles it (gen.go). The engine reads the host's zoneinfo; a test that
// compares it with a recording reads these, since a host's release may model
// a zone otherwise than node's did (tzdata 2026e moves Winnipeg to permanent
// -05 as standard time, which node's 2026c calls Central Daylight Time in
// summer): the recording's answers hold for node's release only.
package nodetz

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	_ "embed"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed zoneinfo.tar.gz
var archive []byte

type zoneinfo struct {
	version string
	tzif    map[string][]byte
}

var load = sync.OnceValues(func() (*zoneinfo, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	z := &zoneinfo{tzif: map[string][]byte{}}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch h.Typeflag {
		case tar.TypeReg:
			b, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			if h.Name == "+VERSION" {
				z.version = strings.TrimSpace(string(b))
			} else {
				z.tzif[h.Name] = b
			}
		case tar.TypeLink:
			b, ok := z.tzif[h.Linkname]
			if !ok {
				return nil, errors.New("nodetz: " + h.Name + " links to " + h.Linkname + ", not before it")
			}
			z.tzif[h.Name] = b
		default:
			return nil, errors.New("nodetz: unexpected entry " + h.Name)
		}
	}
	if z.version == "" {
		return nil, errors.New("nodetz: no +VERSION")
	}
	return z, nil
})

func must() *zoneinfo {
	z, err := load()
	if err != nil {
		panic(err)
	}
	return z
}

// Version is the tzdata release, as node's process.versions.tz names it.
func Version() string { return must().version }

// Names lists every name of the release, zones and links, sorted.
func Names() []string {
	z := must()
	names := make([]string, 0, len(z.tzif))
	for n := range z.tzif {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Load is time.LoadLocation over the release: "" and "UTC" are time.UTC.
func Load(name string) (*time.Location, error) {
	if name == "" || name == "UTC" {
		return time.UTC, nil
	}
	b, ok := must().tzif[name]
	if !ok {
		return nil, errors.New("nodetz: unknown time zone " + name)
	}
	return time.LoadLocationFromTZData(name, b)
}

// TB is the part of testing.TB that MustLoad uses.
type TB interface {
	Helper()
	Fatal(args ...any)
}

// MustLoad is Load, failing the test on an error.
func MustLoad(t TB, name string) *time.Location {
	t.Helper()
	loc, err := Load(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
