package vectors

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestVectorFingerprints fails, without node, when a committed vector file is
// no longer vouched for by a recording: its generator or a module it imports,
// one of its inputs, its manifest line, the module set's lockfile or the
// pinned node changed since the outputs were recorded, or an output was
// edited by hand. Re-record with `go run ./internal/cmd/vectors -write` (with
// the pinned node), which rewrites testdata/vectors.lock.
func TestVectorFingerprints(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := Manifest(root)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := ReadLock(root)
	if err != nil {
		t.Fatal(err)
	}
	node, err := PinnedNode(root)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, e := range entries {
		keys[e.Key()] = true
		fp, err := Fingerprint(root, e, node)
		if err != nil {
			t.Errorf("%s:%d: %v", ManifestFile, e.Line, err)
			continue
		}
		switch want, ok := lock[e.Key()]; {
		case !ok:
			t.Errorf("%s is not in %s: record it (go run ./internal/cmd/vectors -write -run %s)", e.Key(), LockFile, e.Key())
		case want != fp:
			t.Errorf("%s: its generator, inputs, command, node %s or modules, or the recorded output changed since it was recorded: re-record it (go run ./internal/cmd/vectors -write -run %s)", e.Key(), node, e.Key())
		}
	}
	for k := range lock {
		if !keys[k] {
			t.Errorf("%s names %s, which the manifest does not list", LockFile, k)
		}
	}
}

// notInManifest are the node scripts under internal/ that record no committed
// vector file of their own, and why.
var notInManifest = map[string]string{
	"internal/vega/layout/testdata/gen_specs.mjs": "writes input specifications (layout/testdata/specs), not vectors",
	"internal/vegalite/testdata/gen_fuzz.mjs":     "records a differential sample for VEGALITE_FUZZ runs; nothing committed",
	"internal/jsmath/testdata/eval_v8.mjs":        "evaluator of go test ./internal/jsmath -update (arm64 recordings)",
	"internal/jsmath/testdata/gen_math.mjs":       "math.json.gz, recorded on arm64 with Linux pow rows (see its header)",
}

// Every generator is in the manifest, so none can drift unchecked.
func TestEveryGeneratorIsInTheManifest(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := Manifest(root)
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, e := range entries {
		imports, err := e.Imports(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range imports {
			covered[f] = true
		}
	}
	var scripts []string
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == "fuzz") {
			return filepath.SkipDir
		}
		if ext := path.Ext(d.Name()); !d.IsDir() && (ext == ".mjs" || ext == ".js") {
			rel, _ := filepath.Rel(root, p)
			scripts = append(scripts, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(scripts)
	if len(scripts) < 40 {
		t.Fatalf("found only %d node scripts under internal/", len(scripts))
	}
	for _, s := range scripts {
		if !covered[s] && notInManifest[s] == "" && !strings.Contains(s, "/enginebench/") {
			t.Errorf("%s is in no entry of %s: list what it records, or say in notInManifest why it records nothing", s, ManifestFile)
		}
	}
	for s := range notInManifest {
		if covered[s] {
			t.Errorf("%s is both in the manifest and in notInManifest", s)
		}
	}
}
