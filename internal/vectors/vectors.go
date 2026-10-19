// Package vectors describes the vector files the unit tests compare the
// engine with: what upstream answered, recorded by a node generator
// (<pkg>/testdata/gen_*.mjs and the like) and committed, so that `go test`
// needs no node. A committed vector file is only as good as its claim to be
// what its generator gives today, with the pinned node and packages; a
// generator edited after recording, an input changed, a package upgraded or
// a recording made on another platform leaves a file that is no longer
// upstream's answer, and the tests then compare the engine with something
// else (audit 2026-10-18, follow-up to C14/C72).
//
// testdata/vectors.txt lists every generator run (Manifest); testdata/
// vectors.lock holds, for each, a fingerprint of everything that determined
// its outputs and of the outputs themselves (Fingerprint). The lock is
// written only by `go run ./internal/cmd/vectors -write`, which re-records the
// outputs; TestVectorFingerprints (in package vectors) recomputes it without
// node and fails when a generator, its inputs, its command, the pinned node,
// a lockfile or an output changed without a re-recording. The heavy CI job
// runs `go run ./internal/cmd/vectors` (check mode), which regenerates every
// file with the pinned node and fails on any difference.
package vectors

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ManifestFile and LockFile are relative to the repository root.
const (
	ManifestFile = "testdata/vectors.txt"
	LockFile     = "testdata/vectors.lock"
)

// Entry is one generator run of the manifest:
//
//	dir | env | command | outputs | inputs | platform
//
// dir is the working directory (relative to the repository root). env is
// space-separated NAME=value pairs, or "-"; NODE_PATH is set by the runner to
// the absolute node_modules of the module set named by MODULES=vl6 (the
// default) or MODULES=vl5. command is node's arguments: the generator, then
// its arguments; a token "glob:PATTERN" expands to the sorted files matching
// PATTERN (relative to dir), and "$OUT" is a scratch directory. outputs is
// one of
//
//	> FILE            the generator's stdout, gzipped (-9) when FILE ends in .gz
//	files FILE...     files the generator writes itself
//	outdir DIR EXT    files the generator writes into $OUT, NAME.json or
//	                  NAME.svg, each stored gzipped as DIR/NAME.EXT.gz
//
// (paths relative to dir). inputs are further files the outputs depend on,
// as globs relative to the repository root ("-" for none); the generator and
// the local modules it imports are found by Imports. platform is "any", or
// the platform the recording must come from, as node names it
// (darwin-arm64, linux-x64): the outputs depend on it, and are checked only
// there.
type Entry struct {
	Line     int
	Dir      string
	Env      []string
	Command  []string
	Mode     string // ">", "files" or "outdir"
	Outputs  []string
	OutExt   string // outdir: "json" or "svg"
	Inputs   []string
	Platform string
	Text     string // the manifest line, normalized
}

// Key names an entry in the lock: its first output.
func (e Entry) Key() string { return path.Join(e.Dir, e.Outputs[0]) }

// Modules is the module set (node_modules directory, relative to the root)
// the entry runs with.
func (e Entry) Modules() string {
	for _, kv := range e.Env {
		if kv == "MODULES=vl5" {
			return "testdata/oracle-node-vl5"
		}
	}
	return "testdata/oracle-node"
}

// Manifest reads the manifest under root.
func Manifest(root string) ([]Entry, error) {
	f, err := os.Open(filepath.Join(root, ManifestFile))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []Entry
	seen := map[string]int{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, " | ")
		if len(parts) != 6 {
			return nil, fmt.Errorf("%s:%d: want dir | env | command | outputs | inputs | platform", ManifestFile, n)
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		e := Entry{Line: n, Dir: parts[0], Command: strings.Fields(parts[2]), Platform: parts[5], Text: strings.Join(parts, " | ")}
		if parts[1] != "-" {
			e.Env = strings.Fields(parts[1])
		}
		if parts[4] != "-" {
			e.Inputs = strings.Fields(parts[4])
		}
		outs := strings.Fields(parts[3])
		if len(outs) < 2 || len(e.Command) == 0 {
			return nil, fmt.Errorf("%s:%d: no command or no output", ManifestFile, n)
		}
		e.Mode = outs[0]
		switch e.Mode {
		case ">":
			if len(outs) != 2 {
				return nil, fmt.Errorf("%s:%d: > takes one file", ManifestFile, n)
			}
			e.Outputs = outs[1:]
		case "files":
			e.Outputs = outs[1:]
		case "outdir":
			if len(outs) != 3 || (outs[2] != "json" && outs[2] != "svg") {
				return nil, fmt.Errorf("%s:%d: outdir takes a directory and json or svg", ManifestFile, n)
			}
			e.Outputs, e.OutExt = outs[1:2], outs[2]
		default:
			return nil, fmt.Errorf("%s:%d: outputs must start with >, files or outdir", ManifestFile, n)
		}
		if e.Platform != "any" && !regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`).MatchString(e.Platform) {
			return nil, fmt.Errorf("%s:%d: platform %q is not any or os-arch", ManifestFile, n, e.Platform)
		}
		if p, dup := seen[e.Key()]; dup {
			return nil, fmt.Errorf("%s:%d: %s is also the output of line %d", ManifestFile, n, e.Key(), p)
		}
		seen[e.Key()] = n
		out = append(out, e)
	}
	return out, sc.Err()
}

// OutputFiles lists the files an entry's outputs are, relative to the root:
// an outdir's every DIR/*.EXT.gz.
func (e Entry) OutputFiles(root string) ([]string, error) {
	var out []string
	for _, o := range e.Outputs {
		p := path.Join(e.Dir, o)
		if e.Mode != "outdir" {
			out = append(out, p)
			continue
		}
		m, err := filepath.Glob(filepath.Join(root, p, "*."+e.OutExt+".gz"))
		if err != nil {
			return nil, err
		}
		if len(m) == 0 {
			return nil, fmt.Errorf("%s: no *.%s.gz", p, e.OutExt)
		}
		for _, f := range m {
			rel, _ := filepath.Rel(root, f)
			out = append(out, filepath.ToSlash(rel))
		}
	}
	sort.Strings(out)
	return out, nil
}

// localImport matches what a generator loads from beside itself: an import,
// a dynamic import, a module hook registered by path, a URL made relative to
// the module.
var localImport = regexp.MustCompile(`(?:from|import\(|register\(|new URL\()\s*'(\.\.?/[^']+)'`)

// Imports lists the generator script of the entry and every local module it
// loads, transitively, relative to the root.
func (e Entry) Imports(root string) ([]string, error) {
	// A worklist, not a recursion: each module is read once.
	seen := map[string]bool{}
	queue := []string{path.Join(e.Dir, e.Command[0])}
	for len(queue) > 0 {
		rel := queue[0]
		queue = queue[1:]
		if seen[rel] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if errors.Is(err, os.ErrNotExist) && len(seen) > 0 {
			// A path in a string the generator writes, not one it loads
			// (gen_wordcloud.mjs rewrites an import to './shim.mjs').
			continue
		}
		if err != nil {
			return nil, err
		}
		seen[rel] = true
		for _, m := range localImport.FindAllSubmatch(b, -1) {
			queue = append(queue, path.Join(path.Dir(rel), string(m[1])))
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}

// InputFiles expands the entry's input globs (relative to the root).
func (e Entry) InputFiles(root string) ([]string, error) {
	var out []string
	for _, g := range e.Inputs {
		m, err := filepath.Glob(filepath.Join(root, g))
		if err != nil {
			return nil, err
		}
		if len(m) == 0 {
			return nil, fmt.Errorf("%s:%d: input %s matches nothing", ManifestFile, e.Line, g)
		}
		for _, f := range m {
			// A directory stands for every file under it.
			err := filepath.WalkDir(f, func(p string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				rel, _ := filepath.Rel(root, p)
				out = append(out, filepath.ToSlash(rel))
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// PinnedNode is the node version the oracle's module set pins (volta.node).
func PinnedNode(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "testdata/oracle-node/package.json"))
	if err != nil {
		return "", err
	}
	var p struct {
		Volta struct{ Node string } `json:"volta"`
	}
	if err := json.Unmarshal(b, &p); err != nil || p.Volta.Node == "" {
		return "", fmt.Errorf("testdata/oracle-node/package.json pins no node (volta.node): %v", err)
	}
	return p.Volta.Node, nil
}

// Content is a file's content for hashing: a gzipped file's decompressed
// bytes, so that recompressing it does not change its fingerprint.
func Content(file string) ([]byte, error) {
	b, err := os.ReadFile(file)
	if err != nil || !strings.HasSuffix(file, ".gz") {
		return b, err
	}
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return io.ReadAll(r)
}

// Fingerprint hashes what determines an entry's outputs (its manifest line,
// generator and imported modules, inputs, the module set's lockfile and the
// node version) and the outputs as they are under root.
func Fingerprint(root string, e Entry, node string) (string, error) {
	h := sha256.New()
	add := func(label string, b []byte) {
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", label, len(b))
		_, _ = h.Write(b)
	}
	add("entry", []byte(e.Text))
	add("node", []byte(node))
	imports, err := e.Imports(root)
	if err != nil {
		return "", err
	}
	inputs, err := e.InputFiles(root)
	if err != nil {
		return "", err
	}
	outputs, err := e.OutputFiles(root)
	if err != nil {
		return "", err
	}
	groups := []struct {
		label string
		files []string
	}{{"source", imports}, {"source", []string{path.Join(e.Modules(), "package-lock.json")}}, {"input", inputs}, {"output", outputs}}
	for _, g := range groups {
		for _, f := range g.files {
			b, err := Content(filepath.Join(root, f))
			if err != nil {
				return "", err
			}
			add(g.label+" "+f, b)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadLock reads the lock: key → fingerprint.
func ReadLock(root string) (map[string]string, error) {
	b, err := os.ReadFile(filepath.Join(root, LockFile))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for i, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("%s:%d: want key TAB fingerprint", LockFile, i+1)
		}
		out[k] = v
	}
	return out, nil
}

// WriteLock writes the lock.
func WriteLock(root string, lock map[string]string) error {
	keys := make([]string, 0, len(lock))
	for k := range lock {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# Written by `go run ./internal/cmd/vectors -write`; checked by TestVectorFingerprints. Do not edit.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s\t%s\n", k, lock[k])
	}
	return os.WriteFile(filepath.Join(root, LockFile), []byte(b.String()), 0o644)
}

// Root finds the repository root from the working directory.
func Root() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ManifestFile)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("repository root not found")
		}
		dir = parent
	}
}
