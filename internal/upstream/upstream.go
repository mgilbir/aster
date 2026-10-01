// Package upstream replays upstream's own test suites against the engine.
//
// scripts/record-upstream-vectors.sh runs the test files of Vega's and d3's packages (from the git
// tag of the installed version) against the installed package, recording every call a test makes
// with upstream's actual answer (testdata/oracle-node/record-upstream-tests.mjs). The result is one
// JSON file per package in testdata/upstream-vectors-cache, git-ignored because it is derived.
//
// A replay test loads a package's vectors with Start, maps each recorded call to a Go function in an
// adapter, and hands the Go answer (in the recorded encoding, see Num, Date, Undefined) to Check,
// which compares it with upstream's exactly. A replay skips when the cache is absent, and fails
// instead with ASTER_ORACLE=require (CI); it also fails when it replays fewer vectors than its floor,
// so it cannot silently shrink.
//
// Known divergences are listed in testdata/upstream-vectors/known-divergences.txt, one per line:
//
//	<package> TAB <signature> TAB <reason citing upstream>
//
// Each replay asserts its entries exactly: a vector that diverges and is not listed fails, and a
// listed vector that no longer diverges fails too.
//
// One root cause shows up in too many vectors to list them one by one: a numeric answer that is a
// few units in the last place off. A line whose signature is `*ulp<=N` accepts, for that package, any
// vector whose answer equals upstream's except for numbers within N ulps. It is asserted the same way:
// the replay fails when no vector uses it, so it cannot outlive its cause.
package upstream

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const (
	cacheDir        = "testdata/upstream-vectors-cache"
	divergencesFile = "testdata/upstream-vectors/known-divergences.txt"
)

// Call is one recorded call. Values are as decoded from JSON, with the recorder's markers
// (objects with a "$" key) left in place: see Num, IsMarker.
type Call struct {
	Fn              string              `json:"fn"`
	Op              string              `json:"op"`
	Method          string              `json:"method"`
	ConstructedWith []any               `json:"constructedWith"`
	Chain           [][]json.RawMessage `json:"chain"`
	Via             [][]json.RawMessage `json:"via"`
	ViaChain        [][]json.RawMessage `json:"viaChain"`
	Args            []any               `json:"args"`
	Params          any                 `json:"params"`
	Input           any                 `json:"input"`
	Output          any                 `json:"output"`
	Value           any                 `json:"value"`
	Result          any                 `json:"result"`
	Threw           *string             `json:"threw"`
	Oversized       any                 `json:"oversized"`
	Instance        int                 `json:"instance"`
	Sequence        int                 `json:"sequence"`

	// HasResult is set when the call has a result (it may be null).
	HasResult bool `json:"-"`
}

// File is one package's recorded vectors.
type File struct {
	Package  string `json:"package"`
	Version  string `json:"version"`
	TimeZone string `json:"timeZone"`
	Calls    []Call `json:"calls"`
}

// Step is one recorded step: a method call (Method set), a plain call of the object (Method empty), or
// a field write (Method "set:<field>").
type Step struct {
	Method string
	Args   []any
}

func steps(raw [][]json.RawMessage) []Step {
	out := make([]Step, len(raw))
	for i, step := range raw {
		_ = json.Unmarshal(step[0], &out[i].Method)
		_ = json.Unmarshal(step[1], &out[i].Args)
	}
	return out
}

// ChainSteps is the configuration of a builder before the question was asked: `domain([0, 1])`,
// `set:opacity(NaN)`, in order.
func (c *Call) ChainSteps() []Step { return steps(c.Chain) }

// ViaSteps are the steps taken from the first result to the one asked: `[tickFormat(5)]` for a call
// of the function `scale.tickFormat(5)` returned; a step with no method is a plain call.
func (c *Call) ViaSteps() []Step { return steps(c.Via) }

// ViaChainSteps is the configuration made on the object the via steps led to, after they were taken.
func (c *Call) ViaChainSteps() []Step { return steps(c.ViaChain) }

// Chained returns the i-th configuration step of a builder chain: method and decoded arguments.
func (c *Call) Chained(i int) (method string, args []any) {
	s := steps(c.Chain[i : i+1])[0]
	return s.Method, s.Args
}

// Signature identifies a vector in the known-divergences file: a readable rendering of the call
// that does not contain a tab or newline.
func (c *Call) Signature() string {
	var b strings.Builder
	name := c.Fn
	if name == "" {
		name = c.Op
	}
	b.WriteString(name)
	if c.Op != "" {
		fmt.Fprintf(&b, "#%d.%d", c.Instance, c.Sequence)
	}
	if len(c.ConstructedWith) > 0 || strings.HasSuffix(name, "()") {
		b.WriteString(jsonText(c.ConstructedWith))
	}
	for i := range c.Chain {
		m, a := c.Chained(i)
		fmt.Fprintf(&b, ".%s%s", m, jsonText(a))
	}
	for _, v := range c.ViaSteps() {
		fmt.Fprintf(&b, ">%s%s", v.Method, jsonText(v.Args))
	}
	for _, v := range c.ViaChainSteps() {
		fmt.Fprintf(&b, "~%s%s", v.Method, jsonText(v.Args))
	}
	if c.Method != "" {
		b.WriteString("::" + c.Method)
	}
	if c.Args != nil || c.Op == "" {
		b.WriteString(jsonText(c.Args))
	}
	s := b.String()
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
}

func jsonText(v any) string {
	if v == nil {
		return "()"
	}
	b, _ := json.Marshal(v)
	s := string(b)
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return "(" + s[1:len(s)-1] + ")"
	}
	return s
}

// Replay is one test's replay of a package's vectors.
type Replay struct {
	t        testing.TB
	File     *File
	known    map[string]string // signature -> reason, for this package
	ulp      float64           // from a `*ulp<=N` line, else 0
	ulpUsed  int
	ulpLine  string
	visited  map[string]bool // signatures compared
	diverged map[string]bool
	replayed int
	skipped  map[string]int
	failures int
	name     string
}

// Start loads a package's vectors. It skips the test when they were not recorded, or fails it when
// ASTER_ORACLE=require.
func Start(t testing.TB, pkg string) *Replay {
	t.Helper()
	root, err := repoRoot()
	var data []byte
	if err == nil {
		data, err = os.ReadFile(filepath.Join(root, cacheDir, pkg+".json"))
	}
	if err != nil {
		msg := fmt.Sprintf("upstream vectors for %s not recorded (%v); run scripts/record-upstream-vectors.sh", pkg, err)
		if os.Getenv("ASTER_ORACLE") == "require" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	f := new(File)
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(f); err != nil {
		t.Fatalf("%s: %v", pkg, err)
	}
	// HasResult needs the raw presence of "result": a null result is a result.
	var raw struct {
		Calls []struct {
			Result *json.RawMessage `json:"result"`
		} `json:"calls"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("%s: %v", pkg, err)
	}
	for i := range f.Calls {
		f.Calls[i].HasResult = raw.Calls[i].Result != nil
	}
	known, err := loadKnown(root)
	if err != nil {
		t.Fatal(err)
	}
	rep := &Replay{
		t: t, File: f, known: known[pkg], name: pkg,
		visited: map[string]bool{}, diverged: map[string]bool{}, skipped: map[string]int{},
	}
	for sig := range rep.known {
		if n, ok := strings.CutPrefix(sig, "*ulp<="); ok {
			if _, err := fmt.Sscanf(n, "%g", &rep.ulp); err != nil || rep.ulp <= 0 {
				t.Fatalf("%s: bad signature %q", divergencesFile, sig)
			}
			delete(rep.known, sig)
			rep.ulpLine = sig
		}
	}
	return rep
}

func loadKnown(root string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	fh, err := os.Open(filepath.Join(root, divergencesFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(nil, 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
			return nil, fmt.Errorf("%s:%d: want <package> TAB <signature> TAB <reason>", divergencesFile, n)
		}
		if out[parts[0]] == nil {
			out[parts[0]] = map[string]string{}
		}
		out[parts[0]][parts[1]] = parts[2]
	}
	return out, sc.Err()
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("repository root not found")
		}
		dir = parent
	}
}

// Check compares the Go answer with the recorded result. got is in the recorded encoding; threw is
// true when the Go function failed where upstream throws (the messages are not compared).
func (r *Replay) Check(c *Call, got any, threw bool) {
	r.t.Helper()
	r.CheckAgainst(c, c.Result, got, threw)
}

// CheckAgainst is Check for a vector whose answer is not its result: the tuples an operator emitted
// (c.Output), say. want is the recorded answer.
func (r *Replay) CheckAgainst(c *Call, want, got any, threw bool) {
	r.t.Helper()
	sig := c.Signature()
	r.visited[sig] = true
	r.replayed++
	var diff string
	switch {
	case c.Threw != nil:
		if !threw {
			diff = fmt.Sprintf("upstream throws %q, engine returned %s", *c.Threw, show(got))
		}
	case threw:
		diff = fmt.Sprintf("upstream returns %s, engine fails", show(want))
	case !Equal(got, want):
		diff = fmt.Sprintf("upstream %s, engine %s", show(want), show(got))
	}
	if diff == "" {
		return
	}
	r.diverged[sig] = true
	if _, ok := r.known[sig]; ok {
		return
	}
	if r.ulp > 0 && c.Threw == nil && !threw && WithinUlps(got, want, r.ulp) {
		r.ulpUsed++
		return
	}
	r.failures++
	if r.failures <= 25 {
		r.t.Errorf("%s: %s (vector %d)\n\t%s", r.name, sig, r.indexOf(c), diff)
	}
}

// indexOf is the position of c in the recorded file (one vector per line, after the header).
func (r *Replay) indexOf(c *Call) int {
	for i := range r.File.Calls {
		if &r.File.Calls[i] == c {
			return i
		}
	}
	return -1
}

// Skip counts a vector the replay does not run, with the reason; Done reports them.
func (r *Replay) Skip(reason string) { r.skipped[reason]++ }

// Replayed is the number of vectors compared so far.
func (r *Replay) Replayed() int { return r.replayed }

// Done asserts the floor and the known divergences, and logs a summary. floor is the least number of
// vectors this replay must have compared.
func (r *Replay) Done(floor int) {
	r.t.Helper()
	if r.failures > 25 {
		r.t.Errorf("%s: … and %d more divergences", r.name, r.failures-25)
	}
	var stale []string
	for sig := range r.known {
		if r.visited[sig] && !r.diverged[sig] {
			stale = append(stale, sig)
		}
	}
	sort.Strings(stale)
	for _, sig := range stale {
		r.t.Errorf("%s: %s is listed as a known divergence but now agrees with upstream; remove it from %s", r.name, sig, divergencesFile)
	}
	if r.ulp > 0 && r.ulpUsed == 0 {
		r.t.Errorf("%s: the `%s` line of %s matched no vector; remove it", r.name, r.ulpLine, divergencesFile)
	}
	if r.replayed < floor {
		r.t.Errorf("%s: replayed %d vectors, fewer than the floor %d (the replay shrank, or the recording did)", r.name, r.replayed, floor)
	}
	var notes []string
	for reason, n := range r.skipped {
		notes = append(notes, fmt.Sprintf("%d %s", n, reason))
	}
	sort.Strings(notes)
	known := fmt.Sprintf("%d known divergences", len(r.diverged))
	if r.ulp > 0 {
		known += fmt.Sprintf(", %d of them within %g ulp", r.ulpUsed, r.ulp)
	}
	r.t.Logf("%s: replayed %d vectors (%s); not replayed: %s", r.name, r.replayed, known, strings.Join(notes, "; "))
}

func show(v any) string {
	b, _ := json.Marshal(v)
	s := string(b)
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

// IsMarker reports whether v is a recorder marker (an object with a "$" key) and returns its kind.
func IsMarker(v any) (kind string, ok bool) {
	m, isMap := v.(map[string]any)
	if !isMap {
		return "", false
	}
	k, has := m["$"].(string)
	return k, has
}

// Num decodes a recorded number, including the NaN, Infinity, -Infinity and -0 markers.
func Num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case map[string]any:
		switch x["$"] {
		case "NaN":
			return math.NaN(), true
		case "Infinity":
			return math.Inf(1), true
		case "-Infinity":
			return math.Inf(-1), true
		case "-0":
			return math.Copysign(0, -1), true
		}
	}
	return 0, false
}

// Enc encodes a Go float64 as the recorder encodes a JavaScript number.
func Enc(x float64) any {
	switch {
	case math.IsNaN(x):
		return map[string]any{"$": "NaN"}
	case math.IsInf(x, 1):
		return map[string]any{"$": "Infinity"}
	case math.IsInf(x, -1):
		return map[string]any{"$": "-Infinity"}
	case x == 0 && math.Signbit(x):
		return map[string]any{"$": "-0"}
	}
	return x
}

// Floats encodes a slice of numbers.
func Floats(xs []float64) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = Enc(x)
	}
	return out
}

// Date decodes a recorded Date to epoch milliseconds.
func Date(v any) (float64, bool) {
	m, ok := v.(map[string]any)
	if ok && m["$"] == "date" {
		return Num(m["epochMillis"])
	}
	return 0, false
}

// EncDate encodes epoch milliseconds as the recorder encodes a Date (an invalid date has NaN time).
func EncDate(t float64) any {
	return map[string]any{"$": "date", "epochMillis": Enc(t)}
}

// Undefined is the recorder's marker for undefined.
func Undefined() any { return map[string]any{"$": "undefined"} }

// Equal compares two recorded values exactly: numbers by bit pattern (so NaN equals NaN and the
// marker for -0 differs from 0), everything else structurally.
func Equal(a, b any) bool {
	switch x := a.(type) {
	case float64:
		y, ok := b.(float64)
		return ok && math.Float64bits(x) == math.Float64bits(y)
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !Equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, has := y[k]
			if !has || !Equal(v, w) {
				return false
			}
		}
		return true
	case nil:
		return b == nil
	}
	return reflect.DeepEqual(a, b)
}

// WithinUlps reports whether a and b are equal except for numbers that differ by at most n units in
// the last place.
func WithinUlps(a, b any, n float64) bool {
	switch x := a.(type) {
	case float64:
		y, ok := b.(float64)
		if !ok {
			return false
		}
		if x == y {
			return true
		}
		if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) || (x < 0) != (y < 0) {
			return false
		}
		d := int64(math.Float64bits(x)) - int64(math.Float64bits(y))
		if d < 0 {
			d = -d
		}
		return float64(d) <= n
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !WithinUlps(x[i], y[i], n) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, has := y[k]
			if !has || !WithinUlps(v, w, n) {
				return false
			}
		}
		return true
	}
	return Equal(a, b)
}
