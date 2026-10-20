// Command vectors regenerates the committed vector files of
// testdata/vectors.txt with the pinned node, and compares them with what is
// committed (see internal/vectors).
//
//	go run ./internal/cmd/vectors              check: regenerate in a scratch copy, fail on any difference
//	go run ./internal/cmd/vectors -write       re-record in place and rewrite testdata/vectors.lock
//	go run ./internal/cmd/vectors -run expr    only the entries whose first output matches "expr"
//	go run ./internal/cmd/vectors -repeat 2    run each generator twice: a generator whose output varies fails
//
// An entry pinned to another platform than node's is reported as not checked
// here, never as passing. node must be the version testdata/oracle-node pins
// (ASTER_ORACLE_ANY_NODE=1 runs another, and its recordings then fail the
// fingerprint check).
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mgilbir/aster/internal/vectors"
)

var (
	write   = flag.Bool("write", false, "re-record the outputs in place and rewrite the lock")
	only    = flag.String("run", "", "only entries whose first output matches this regular expression")
	repeat  = flag.Int("repeat", 1, "run each generator this many times; outputs that differ between runs fail")
	jobs    = flag.Int("j", 4, "generators run at once")
	keep    = flag.Bool("keep", false, "keep the scratch copy (check mode)")
	timeout = flag.Duration("timeout", 30*time.Minute, "per generator run")
	stamp   = flag.Bool("stamp", false, "with -write: also fingerprint the entries pinned to another platform, as committed, without re-recording them (their platform's check vouches for them)")
	anyPlat = flag.Bool("any-platform", false, "check mode: also run the entries pinned to another platform (to see how they differ; never with -write)")
	dump    = flag.String("out", "", "check mode: also write the generated outputs (decompressed) under this directory")
)

func main() {
	flag.Parse()
	if flag.NArg() > 0 {
		// The flag package stops at the first argument that is not a flag:
		// the flags after it would be ignored.
		fmt.Fprintf(os.Stderr, "vectors: unexpected argument %q\n", flag.Arg(0))
		os.Exit(2)
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "vectors:", err)
		os.Exit(1)
	}
}

type result struct {
	e       vectors.Entry
	status  string // same, drift, error, skipped
	details []string
}

func run() error {
	root, err := vectors.Root()
	if err != nil {
		return err
	}
	entries, err := vectors.Manifest(root)
	if err != nil {
		return err
	}
	pin, err := vectors.PinnedNode(root)
	if err != nil {
		return err
	}
	node, err := nodeOut(root, "-p", "process.versions.node")
	if err != nil {
		return fmt.Errorf("node: %w", err)
	}
	if node != pin && os.Getenv("ASTER_ORACLE_ANY_NODE") != "1" {
		return fmt.Errorf("node %s is not the pinned node %s (testdata/oracle-node/package.json volta.node); put it first on PATH, or set ASTER_ORACLE_ANY_NODE=1", node, pin)
	}
	platform, err := nodeOut(root, "-p", "process.platform + '-' + process.arch")
	if err != nil {
		return err
	}
	work := root
	if !*write {
		if work, err = scratch(root); err != nil {
			return err
		}
		if *keep {
			fmt.Println("vectors: scratch copy in", work)
		} else {
			defer func() { _ = os.RemoveAll(work) }()
		}
	}
	re, err := regexp.Compile(*only)
	if err != nil {
		return fmt.Errorf("-run: %w", err)
	}
	var sel []vectors.Entry
	for _, e := range entries {
		if re.MatchString(e.Key()) {
			sel = append(sel, e)
		}
	}
	results := make([]result, len(sel))
	var wg sync.WaitGroup
	next := make(chan int)
	for range max(*jobs, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				results[i] = runEntry(root, work, sel[i], platform)
			}
		}()
	}
	for i := range sel {
		next <- i
	}
	close(next)
	wg.Wait()

	lock, err := vectors.ReadLock(root)
	if err != nil {
		return err
	}
	bad := 0
	for _, r := range results {
		fmt.Printf("%-8s %s (manifest line %d)\n", strings.ToUpper(r.status), r.e.Key(), r.e.Line)
		for _, d := range r.details {
			fmt.Printf("         %s\n", d)
		}
		switch r.status {
		case "same", "written":
			if *write {
				fp, err := vectors.Fingerprint(root, r.e, node)
				if err != nil {
					return err
				}
				lock[r.e.Key()] = fp
			}
		case "skipped":
			if *write && *stamp {
				fp, err := vectors.Fingerprint(root, r.e, node)
				if err != nil {
					return err
				}
				lock[r.e.Key()] = fp
				fmt.Printf("         stamped as committed, not re-recorded here\n")
			}
		default:
			bad++
		}
	}
	if *write {
		if *only == "" {
			// An entry no longer in the manifest leaves the lock.
			keys := map[string]bool{}
			for _, e := range entries {
				keys[e.Key()] = true
			}
			for k := range lock {
				if !keys[k] {
					delete(lock, k)
				}
			}
		}
		if err := vectors.WriteLock(root, lock); err != nil {
			return err
		}
	}
	if bad > 0 {
		return fmt.Errorf("%d of %d entries failed (node %s, %s)", bad, len(sel), node, platform)
	}
	fmt.Printf("vectors: %d entries, node %s, %s\n", len(sel), node, platform)
	return nil
}

// nodeOut runs node and returns its trimmed stdout.
func nodeOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("node", args...)
	cmd.Dir = dir
	b, err := cmd.Output()
	return strings.TrimSpace(string(b)), err
}

// scratch copies the checkout (tracked and untracked files git does not
// ignore) into a temporary directory, with the module sets' node_modules
// linked, so a check never writes to the checkout.
func scratch(root string) (string, error) {
	cmd := exec.Command("git", "ls-files", "-z", "-co", "--exclude-standard")
	cmd.Dir = root
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git ls-files: %w", err)
	}
	dir, err := os.MkdirTemp("", "aster-vectors-")
	if err != nil {
		return "", err
	}
	for _, f := range strings.Split(string(b), "\x00") {
		if f == "" || strings.Contains(f, "node_modules") {
			continue
		}
		src := filepath.Join(root, f)
		fi, err := os.Lstat(src)
		if err != nil || !fi.Mode().IsRegular() {
			continue // a deleted file, a symlink (the caches)
		}
		dst := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, data, fi.Mode().Perm()); err != nil {
			return "", err
		}
	}
	for _, set := range []string{"testdata/oracle-node", "testdata/oracle-node-vl5"} {
		nm, err := filepath.EvalSymlinks(filepath.Join(root, set, "node_modules"))
		if err != nil {
			return "", fmt.Errorf("%s/node_modules: %w (npm ci there)", set, err)
		}
		if err := os.Symlink(nm, filepath.Join(dir, set, "node_modules")); err != nil {
			return "", err
		}
	}
	// Upstream's repository at the installed tag (git-ignored; cloned by
	// scripts/record-upstream-vectors.sh), whose example data the scene
	// corpus reads.
	if clones, err := filepath.EvalSymlinks(filepath.Join(root, "testdata", "upstream-clones")); err == nil {
		if err := os.Symlink(clones, filepath.Join(dir, "testdata", "upstream-clones")); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// runEntry regenerates an entry under work and compares its outputs with the
// checkout's (or, with -write, writes them there).
func runEntry(root, work string, e vectors.Entry, platform string) result {
	r := result{e: e}
	if e.Platform != "any" && e.Platform != platform && !(*anyPlat && !*write) {
		r.status = "skipped"
		r.details = []string{fmt.Sprintf("recorded on %s; not checkable on %s", e.Platform, platform)}
		return r
	}
	// Read before generating: a generator that writes its files itself
	// writes them in place under -write.
	committed, err := committedOutputs(root, e)
	if err != nil {
		r.status, r.details = "error", []string{err.Error()}
		return r
	}
	raw := map[string][]byte{}
	for f := range committed {
		raw[f], _ = os.ReadFile(filepath.Join(root, f))
	}
	var first map[string][]byte
	for i := range max(*repeat, 1) {
		outs, err := generate(root, work, e)
		if err != nil {
			r.status, r.details = "error", []string{err.Error()}
			return r
		}
		if i == 0 {
			first = outs
			continue
		}
		for _, d := range diffOutputs(first, outs) {
			r.details = append(r.details, "run "+fmt.Sprint(i+1)+" differs from run 1: "+d)
		}
	}
	if len(r.details) > 0 {
		r.status = "unstable"
		return r
	}
	diffs := diffOutputs(committed, first)
	if *dump != "" {
		for f, b := range first {
			dst := filepath.Join(*dump, strings.TrimSuffix(f, ".gz"))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err == nil {
				_ = os.WriteFile(dst, b, 0o644)
			}
		}
	}
	if !*write {
		r.status = "same"
		if len(diffs) > 0 {
			r.status, r.details = "drift", diffs
		}
		return r
	}
	if err := store(root, committed, raw, first); err != nil {
		r.status, r.details = "error", []string{err.Error()}
		return r
	}
	r.status = "same"
	if len(diffs) > 0 {
		r.status, r.details = "written", diffs
	}
	return r
}

// generate runs the generator in work and returns its outputs, by path
// relative to the root, decompressed.
func generate(root, work string, e vectors.Entry) (map[string][]byte, error) {
	dir := filepath.Join(work, e.Dir)
	out, err := os.MkdirTemp("", "aster-vectors-out-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(out) }()
	var args []string
	for _, a := range e.Command {
		switch {
		case a == "$OUT":
			args = append(args, out)
		case strings.HasPrefix(a, "each:"):
			// One argument per committed output of the outdir, so a
			// curated subset of a corpus is re-recorded as it is.
			m, _ := filepath.Glob(filepath.Join(root, e.Dir, e.Outputs[0], "*."+e.OutExt+".gz"))
			if len(m) == 0 {
				return nil, fmt.Errorf("%s: no committed outputs to re-record", a)
			}
			sort.Strings(m)
			for _, f := range m {
				name := strings.TrimSuffix(filepath.Base(f), "."+e.OutExt+".gz")
				args = append(args, strings.ReplaceAll(a[len("each:"):], "{}", name))
			}
		case strings.HasPrefix(a, "glob:"):
			m, err := filepath.Glob(filepath.Join(dir, a[len("glob:"):]))
			if err != nil || len(m) == 0 {
				return nil, fmt.Errorf("%s matches nothing", a)
			}
			sort.Strings(m)
			args = append(args, m...)
		default:
			args = append(args, a)
		}
	}
	// A file the generator writes itself is removed first, so a generator
	// that stopped writing it fails instead of leaving the old one.
	if e.Mode == "files" {
		for _, o := range e.Outputs {
			_ = os.Remove(filepath.Join(dir, o))
		}
	}
	env := []string{"TZ=UTC", "NODE_PATH=" + filepath.Join(work, e.Modules(), "node_modules"), "LC_ALL=C"}
	zones := []string{""}
	for _, kv := range e.Env {
		switch {
		case strings.HasPrefix(kv, "MODULES="):
		case strings.HasPrefix(kv, "TZLIST="):
			// One run per zone, their outputs concatenated.
			zones = strings.Split(kv[len("TZLIST="):], ",")
		default:
			env = append(env, kv)
		}
	}
	var stdout bytes.Buffer
	for _, zone := range zones {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		cmd := exec.CommandContext(ctx, "node", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		if zone != "" {
			cmd.Env = append(cmd.Env, "TZ="+zone)
		}
		var stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		cancel()
		if err != nil {
			return nil, fmt.Errorf("node %s: %v: %s", strings.Join(e.Command, " "), err, lastLines(stderr.String(), 5))
		}
	}
	got := map[string][]byte{}
	switch e.Mode {
	case ">":
		got[path.Join(e.Dir, e.Outputs[0])] = stdout.Bytes()
	case "files":
		for _, o := range e.Outputs {
			b, err := vectors.Content(filepath.Join(dir, o))
			if err != nil {
				return nil, fmt.Errorf("the generator did not write %s: %w", o, err)
			}
			got[path.Join(e.Dir, o)] = b
		}
	case "outdir":
		m, _ := filepath.Glob(filepath.Join(out, "*."+e.OutExt))
		if len(m) == 0 {
			return nil, fmt.Errorf("the generator wrote no *.%s", e.OutExt)
		}
		for _, f := range m {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			got[path.Join(e.Dir, e.Outputs[0], filepath.Base(f)+".gz")] = b
		}
	}
	return got, nil
}

// committedOutputs reads the checkout's outputs of an entry, decompressed.
func committedOutputs(root string, e vectors.Entry) (map[string][]byte, error) {
	files, err := e.OutputFiles(root)
	if err != nil && e.Mode != "outdir" {
		return nil, err
	}
	out := map[string][]byte{}
	for _, f := range files {
		b, err := vectors.Content(filepath.Join(root, f))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err == nil {
			out[f] = b
		}
	}
	return out, nil
}

// diffOutputs describes how got differs from want, file by file.
func diffOutputs(want, got map[string][]byte) []string {
	var out []string
	for f, w := range want {
		g, ok := got[f]
		switch {
		case !ok:
			out = append(out, f+": committed, but not generated any more")
		case !bytes.Equal(g, w):
			out = append(out, f+": "+firstDifference(w, g))
		}
	}
	for f := range got {
		if _, ok := want[f]; !ok {
			out = append(out, f+": generated, but not committed")
		}
	}
	sort.Strings(out)
	return out
}

// firstDifference shows where two outputs first differ.
func firstDifference(want, got []byte) string {
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	ctx := func(b []byte) string {
		lo, hi := max(i-60, 0), min(i+60, len(b))
		return strings.ReplaceAll(string(b[lo:hi]), "\n", "⏎")
	}
	return fmt.Sprintf("%d bytes committed, %d generated; first difference at byte %d:\n           committed …%s…\n           generated …%s…", len(want), len(got), i, ctx(want), ctx(got))
}

// store writes the generated outputs into the checkout: a file whose content
// did not change gets its committed bytes back (its compression too, which a
// generator writing in place may have changed), and an outdir file no longer
// generated is removed.
func store(root string, committed, raw, got map[string][]byte) error {
	for f, b := range got {
		if c, ok := committed[f]; ok && bytes.Equal(c, b) {
			if err := os.WriteFile(filepath.Join(root, f), raw[f], 0o644); err != nil {
				return err
			}
			continue
		}
		data := b
		if strings.HasSuffix(f, ".gz") {
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			if _, err := zw.Write(b); err != nil {
				return err
			}
			if err := zw.Close(); err != nil {
				return err
			}
			data = buf.Bytes()
		}
		dst := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	for f := range committed {
		if _, ok := got[f]; !ok {
			if err := os.Remove(filepath.Join(root, f)); err != nil {
				return err
			}
		}
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " ⏎ ")
}
