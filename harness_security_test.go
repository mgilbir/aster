package aster

// Shared harness for the *_security_test.go files. Every hostile input runs in
// a child process (this test binary re-executed) so that a runaway render can
// be killed on wall-clock time or on heap size without taking the test run
// down. The child reports its elapsed time and peak heap on stdout.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

const secChildEnv = "ASTER_SEC_CHILD"

type secJob struct {
	Kind      string   `json:"kind"`    // vega, vl, vl2vega, svgpng, svgpdf
	Input     string   `json:"input"`   // spec JSON or SVG text
	Timeout   string   `json:"timeout"` // WithTimeout value, "" = default
	MemLimit  uint64   `json:"memLimit"`
	HeapCapMB uint64   `json:"heapCapMB"` // child exits 3 above this
	Loader    string   `json:"loader"`    // "", "static", "record"
	FontB64   []byte   `json:"font"`
	Scale     float64  `json:"scale"`
	Extra     []string `json:"extra"`
}

type secResult struct {
	Err       string   `json:"err"`
	Panic     string   `json:"panic"`
	Seconds   float64  `json:"seconds"`
	CPU       float64  `json:"cpu"` // user+system CPU seconds of the child
	PeakMB    float64  `json:"peakMB"`
	OutLen    int      `json:"outLen"`
	Out       string   `json:"out"` // SVG output when small
	LoadCalls []string `json:"loadCalls"`
	SanCalls  []string `json:"sanCalls"`
}

// recLoader records every call and serves a tiny JSON payload.
type recLoader struct {
	load, san []string
	denyHref  bool
}

func (l *recLoader) Load(_ context.Context, uri string) ([]byte, error) {
	l.load = append(l.load, uri)
	return []byte(`[{"a":1,"b":2}]`), nil
}
func (l *recLoader) Sanitize(_ context.Context, uri string) (string, error) {
	l.san = append(l.san, uri)
	if l.denyHref && strings.Contains(uri, "deny") {
		return "", fmt.Errorf("denied")
	}
	return uri, nil
}

func init() {
	if os.Getenv(secChildEnv) == "" {
		return
	}
	var job secJob
	if err := json.NewDecoder(os.Stdin).Decode(&job); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(runSecChild(job))
}

func runSecChild(job secJob) int {
	var peak uint64
	stop := make(chan struct{})
	go func() {
		var ms runtime.MemStats
		for {
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > peak {
				peak = ms.HeapAlloc
			}
			if job.HeapCapMB > 0 && ms.HeapAlloc > job.HeapCapMB<<20 {
				fmt.Printf("%s\n", `{"err":"HEAPCAP exceeded","peakMB":`+fmt.Sprint(ms.HeapAlloc>>20)+`}`)
				os.Exit(3)
			}
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()
	rl := &recLoader{denyHref: true}
	opts := []Option{}
	switch {
	case job.Loader == "record":
		opts = append(opts, WithLoader(rl))
	case strings.HasPrefix(job.Loader, "bigcsv:"), strings.HasPrefix(job.Loader, "bigjson:"):
		var mb int
		kind, arg, _ := strings.Cut(job.Loader, ":")
		fmt.Sscan(arg, &mb)
		opts = append(opts, WithLoader(&bigLoader{kind: kind, mb: mb, rec: rl}))
	}
	if job.Timeout != "" {
		d, _ := time.ParseDuration(job.Timeout)
		opts = append(opts, WithTimeout(d))
	}
	if job.MemLimit > 0 {
		opts = append(opts, WithMemoryLimit(job.MemLimit))
	}
	for _, f := range job.Extra {
		switch f {
		case "nomeasure":
			opts = append(opts, WithTextMeasurement(false))
		}
	}
	if len(job.FontB64) > 0 {
		opts = append(opts, WithFont("Evil", job.FontB64))
	}
	res := secResult{}
	t0 := time.Now()
	cpu0 := cpuSeconds()
	func() {
		defer func() {
			if r := recover(); r != nil {
				res.Panic = fmt.Sprint(r)
			}
		}()
		c, err := New(opts...)
		if err != nil {
			res.Err = err.Error()
			return
		}
		defer c.Close()
		t0 = time.Now()
		var out []byte
		var s string
		switch job.Kind {
		case "vega":
			s, err = c.VegaToSVG([]byte(job.Input))
			out = []byte(s)
		case "vl":
			s, err = c.VegaLiteToSVG([]byte(job.Input))
			out = []byte(s)
		case "vl2vega":
			out, err = c.VegaLiteToVega([]byte(job.Input))
		case "vegapng":
			scale := job.Scale
			if scale == 0 {
				scale = 1
			}
			out, err = c.VegaToPNG([]byte(job.Input), WithScale(scale))
		case "svgpng":
			scale := job.Scale
			if scale == 0 {
				scale = 1
			}
			out, err = c.SVGToPNG(job.Input, WithScale(scale))
		case "svgpdf":
			out, err = c.SVGToPDF(job.Input)
		case "vegapdf":
			out, err = c.VegaToPDF([]byte(job.Input))
		}
		if err != nil {
			res.Err = err.Error()
		}
		res.OutLen = len(out)
		if len(out) < 1<<20 && job.Kind != "svgpng" && job.Kind != "vegapng" && job.Kind != "svgpdf" {
			res.Out = string(out)
		}
	}()
	res.Seconds = time.Since(t0).Seconds()
	res.CPU = cpuSeconds() - cpu0
	close(stop)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.HeapAlloc > peak {
		peak = ms.HeapAlloc
	}
	res.PeakMB = float64(peak) / (1 << 20)
	res.LoadCalls, res.SanCalls = rl.load, rl.san
	b, _ := json.Marshal(res)
	fmt.Println(string(b))
	return 0
}

// runSec executes job in a child, killing it after wall (default 60s).
// killed reports a wall-clock kill or a heap-cap exit.
func runSec(t testing.TB, job secJob, wall time.Duration) (res secResult, killed bool) {
	t.Helper()
	if wall == 0 {
		wall = 60 * time.Second
	}
	if job.HeapCapMB == 0 {
		job.HeapCapMB = 3072
	}
	in, _ := json.Marshal(job)
	ctx, cancel := context.WithTimeout(context.Background(), wall)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), secChildEnv+"=1")
	cmd.Stdin = bytes.NewReader(in)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() != nil {
		return secResult{Err: "KILLED after " + wall.String()}, true
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	last := lines[len(lines)-1]
	if jerr := json.Unmarshal([]byte(last), &res); jerr != nil {
		return secResult{Err: fmt.Sprintf("child failed: %v: %s %s", err, out.String(), errb.String())}, true
	}
	if strings.Contains(res.Err, "HEAPCAP") {
		return res, true
	}
	return res, false
}

// TestSecProbe runs one ad-hoc input: SEC_PROBE=<file> SEC_KIND=<kind>
// [SEC_TIMEOUT=5s] [SEC_MEM=bytes] go test -run TestSecProbe -v
func TestSecProbe(t *testing.T) {
	p := os.Getenv("SEC_PROBE")
	if p == "" {
		t.Skip("SEC_PROBE not set")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	job := secJob{Kind: os.Getenv("SEC_KIND"), Input: string(b), Timeout: os.Getenv("SEC_TIMEOUT"), Loader: os.Getenv("SEC_LOADER")}
	if job.Kind == "" {
		job.Kind = "vega"
	}
	if m := os.Getenv("SEC_CAP"); m != "" {
		fmt.Sscan(m, &job.HeapCapMB)
	}
	if m := os.Getenv("SEC_MEM"); m != "" {
		fmt.Sscan(m, &job.MemLimit)
	}
	res, killed := runSec(t, job, 90*time.Second)
	if o := os.Getenv("SEC_OUT"); o != "" {
		os.WriteFile(o, []byte(res.Out), 0o644)
	}
	if len(res.Out) > 300 {
		res.Out = res.Out[:300] + "..."
	}
	t.Logf("killed=%v %+v", killed, res)
}

// bigLoader serves a generated payload of mb megabytes for every Load: many
// tiny rows, the worst case for a row-count limit that is checked after
// parsing.
type bigLoader struct {
	kind string
	mb   int
	rec  *recLoader
}

func (l *bigLoader) Sanitize(ctx context.Context, uri string) (string, error) { return uri, nil }
func (l *bigLoader) Load(_ context.Context, uri string) ([]byte, error) {
	n := l.mb << 20
	switch l.kind {
	case "bigcsv":
		b := make([]byte, 0, n+8)
		b = append(b, "a\n"...)
		for len(b) < n {
			b = append(b, "1\n"...)
		}
		return b, nil
	default:
		b := make([]byte, 0, n+8)
		b = append(b, '[')
		for len(b) < n {
			b = append(b, "{},"...)
		}
		return append(b, "{}]"...), nil
	}
}

func cpuSeconds() float64 {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	return float64(ru.Utime.Sec) + float64(ru.Utime.Usec)/1e6 + float64(ru.Stime.Sec) + float64(ru.Stime.Usec)/1e6
}
