//go:build ignore

// gen makes zoneinfo.tar.gz and tzdata.zi from IANA's tzdata release that
// the pinned node's ICU carries (release below; `node -p process.versions.tz`
// names it), compiled as ICU compiles it: from the rearguard form, whose
// daylight saving time is never negative (Europe/Dublin's winter is standard
// time there, as in ICU, where the main form calls it daylight). It downloads
// tzcode and tzdata of the release, checks them against the SHA-256 sums
// below (of tarballs whose IANA signatures were verified), and builds zic and
// the release's sources with make and a C compiler:
//
//	cd internal/nodetz && go run gen.go
//
// A new release needs the new sums, its signatures checked first
// (https://data.iana.org/time-zones/releases/, *.tar.gz.asc).
package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const release = "2026c"

var sums = map[string]string{
	"tzcode" + release + ".tar.gz": "b1cffc3ace4c4c7cd0efba2f7add86ec3d0b79da48bcf03582671fd3c8feace8",
	"tzdata" + release + ".tar.gz": "e4a178a4477f3d0ea77cc31828ff72aa38feff8d61aa13e7e99e142e9d902be4",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run() error {
	tmp, err := os.MkdirTemp("", "nodetz-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	src := filepath.Join(tmp, "src")
	for name, sum := range sums {
		b, err := fetch("https://data.iana.org/time-zones/releases/" + name)
		if err != nil {
			return err
		}
		if h := sha256.Sum256(b); hex.EncodeToString(h[:]) != sum {
			return fmt.Errorf("%s: SHA-256 %x, want %s", name, h, sum)
		}
		if err := untar(b, src); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	// -o version: the release's version file stands; make would remake it
	// from git, or as "-dirty" after extraction reset the files' times.
	mk := exec.Command("make", "-s", "-o", "version", "zic", "tzdata.zi", "rearguard.zi")
	mk.Dir, mk.Stdout, mk.Stderr = src, os.Stderr, os.Stderr
	if err := mk.Run(); err != nil {
		return err
	}
	out := filepath.Join(tmp, "out")
	zic := exec.Command(filepath.Join(src, "zic"), "-b", "slim", "-d", out, filepath.Join(src, "rearguard.zi"))
	zic.Stdout, zic.Stderr = os.Stderr, os.Stderr
	if err := zic.Run(); err != nil {
		return err
	}
	zi, err := os.ReadFile(filepath.Join(src, "tzdata.zi"))
	if err != nil {
		return err
	}
	if first, _, _ := bytes.Cut(zi, []byte("\n")); string(first) != "# version "+release {
		return fmt.Errorf("tzdata.zi begins %q, want release %s", first, release)
	}
	archive, err := pack(zi, out)
	if err != nil {
		return err
	}
	if err := os.WriteFile("tzdata.zi", zi, 0o644); err != nil {
		return err
	}
	return os.WriteFile("zoneinfo.tar.gz", archive, 0o644)
}

func fetch(url string) ([]byte, error) {
	c := http.Client{Timeout: 2 * time.Minute}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// untar extracts the regular files of a release tarball, which has no
// directories, into dir.
func untar(b []byte, dir string) error {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if !filepath.IsLocal(h.Name) || strings.ContainsRune(h.Name, '/') {
			return fmt.Errorf("unexpected entry %q", h.Name)
		}
		data, err := io.ReadAll(io.LimitReader(tr, 16<<20))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, h.Name), data, 0o644); err != nil {
			return err
		}
	}
}

// pack archives every name of zi, zones and links, from zic's output dir:
// a link whose TZif is its target's is a tar hard link to it. The entries
// are sorted and carry no time or owner, so a release packs to the same
// bytes everywhere. +VERSION names the release.
func pack(zi []byte, dir string) ([]byte, error) {
	target := map[string]string{}
	var names []string
	sc := bufio.NewScanner(bytes.NewReader(zi))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		switch {
		case len(f) >= 2 && f[0] == "Z":
			names = append(names, f[1])
		case len(f) == 3 && f[0] == "L":
			names = append(names, f[2])
			target[f[2]] = f[1]
		}
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	tw := tar.NewWriter(zw)
	put := func(h *tar.Header, data []byte) error {
		h.Mode, h.ModTime, h.Format = 0o644, time.Unix(0, 0), tar.FormatUSTAR
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	if err := put(&tar.Header{Name: "+VERSION", Typeflag: tar.TypeReg, Size: int64(len(release) + 1)}, []byte(release+"\n")); err != nil {
		return nil, err
	}
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(n)))
		if err != nil {
			return nil, err
		}
		if t, ok := target[n]; ok {
			if td, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(t))); err == nil && bytes.Equal(td, data) && t < n {
				if err := put(&tar.Header{Name: n, Typeflag: tar.TypeLink, Linkname: t}, nil); err != nil {
					return nil, err
				}
				continue
			}
		}
		if err := put(&tar.Header{Name: n, Typeflag: tar.TypeReg, Size: int64(len(data))}, data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
