package quickjs

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func newRuntime(t *testing.T, cfg Config) *Runtime {
	t.Helper()
	r, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestEvalModuleDefaultExport(t *testing.T) {
	r := newRuntime(t, Config{})
	got, err := r.EvalModule(`export default "hello";`)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestEvalModuleTopLevelAwait(t *testing.T) {
	r := newRuntime(t, Config{})
	got, err := r.EvalModule(`export default await (async () => "async-ok")();`)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if got != "async-ok" {
		t.Fatalf("got %q, want %q", got, "async-ok")
	}
}

func TestLoadModuleAndImport(t *testing.T) {
	r := newRuntime(t, Config{})
	if err := r.LoadModule("dep", `export const x = 41;`); err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	got, err := r.EvalModule(`import { x } from "dep"; export default String(x + 1);`)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if got != "42" {
		t.Fatalf("got %q, want %q", got, "42")
	}
}

func TestEvalGlobalThenModule(t *testing.T) {
	r := newRuntime(t, Config{})
	if err := r.EvalGlobal("setup.js", `globalThis.__test_value = 7;`); err != nil {
		t.Fatalf("EvalGlobal: %v", err)
	}
	got, err := r.EvalModule(`export default String(globalThis.__test_value * 6);`)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if got != "42" {
		t.Fatalf("got %q, want %q", got, "42")
	}
}

func TestEvalModuleError(t *testing.T) {
	r := newRuntime(t, Config{})
	_, err := r.EvalModule(`throw new Error("boom-42"); export default "x";`)
	if err == nil || !strings.Contains(err.Error(), "boom-42") {
		t.Fatalf("expected boom-42 error, got: %v", err)
	}
}

func TestEvalModuleRejectedPromise(t *testing.T) {
	r := newRuntime(t, Config{})
	_, err := r.EvalModule(`export default await Promise.reject(new Error("rejected-43"));`)
	if err == nil || !strings.Contains(err.Error(), "rejected-43") {
		t.Fatalf("expected rejected-43 error, got: %v", err)
	}
}

func TestBridgeMeasureText(t *testing.T) {
	r := newRuntime(t, Config{
		Bridge: Bridge{
			MeasureText: func(text, font string) float64 {
				return float64(len(text)) + 0.5
			},
		},
	})
	got, err := r.EvalModule(`export default String(__aster_measure_text("hello", "10px sans-serif"));`)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if got != "5.5" {
		t.Fatalf("got %q, want %q", got, "5.5")
	}
}

func TestBridgeLoad(t *testing.T) {
	r := newRuntime(t, Config{
		Bridge: Bridge{
			Load: func(url string) ([]byte, error) {
				if url == "bad" {
					return nil, errors.New("load-denied-44")
				}
				return []byte("data-for:" + url), nil
			},
		},
	})

	got, err := r.EvalModule(`export default await __aster_load("https://example.com/x.json?a=1&b=%20");`)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if got != "data-for:https://example.com/x.json?a=1&b=%20" {
		t.Fatalf("got %q", got)
	}

	_, err = r.EvalModule(`export default await __aster_load("bad");`)
	if err == nil || !strings.Contains(err.Error(), "load-denied-44") {
		t.Fatalf("expected load-denied-44 error, got: %v", err)
	}
}

func TestBridgeLoadLongURL(t *testing.T) {
	var gotURL string
	r := newRuntime(t, Config{
		Bridge: Bridge{
			Load: func(url string) ([]byte, error) {
				gotURL = url
				return []byte(fmt.Sprintf("len:%d", len(url))), nil
			},
		},
	})

	// Longer than the single-segment budget: exercises the stash path.
	longURL := "data:text/plain," + strings.Repeat("abcdefghij", 500) // ~5KB
	got, err := r.EvalModule(fmt.Sprintf(`export default await __aster_load(%q);`, longURL))
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if got != fmt.Sprintf("len:%d", len(longURL)) {
		t.Fatalf("got %q", got)
	}
	if gotURL != longURL {
		t.Fatalf("URL mangled in transit: got len %d, want len %d", len(gotURL), len(longURL))
	}
}

func TestBridgeSanitize(t *testing.T) {
	r := newRuntime(t, Config{
		Bridge: Bridge{
			Sanitize: func(uri string) (string, error) {
				return "sanitized:" + uri, nil
			},
		},
	})
	got, err := r.EvalModule(`export default __aster_sanitize("rel/path.csv");`)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if got != "sanitized:rel/path.csv" {
		t.Fatalf("got %q", got)
	}
}

func TestUnregisteredBridgeGlobalsUndefined(t *testing.T) {
	r := newRuntime(t, Config{})
	got, err := r.EvalModule(`export default String(typeof __aster_load) + "," + String(typeof __aster_measure_text);`)
	if err != nil {
		t.Fatalf("EvalModule: %v", err)
	}
	if got != "undefined,undefined" {
		t.Fatalf("got %q", got)
	}
}

func TestTimeout(t *testing.T) {
	r := newRuntime(t, Config{Timeout: 200 * time.Millisecond})
	// The watchdog interrupts at host-call boundaries; Date.now crosses one.
	_, err := r.EvalModule(`for(;;){ Date.now(); } export default "never";`)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	// After the watchdog closes the module the runtime must report itself
	// unusable, so callers can surface a clear error instead of retrying.
	if !r.Closed() {
		t.Fatal("expected Closed() to be true after a timeout")
	}
	if _, err := r.EvalModule(`export default "again";`); err == nil {
		t.Fatal("expected a subsequent eval on a timed-out runtime to fail")
	}
}

func TestMemoryLimit(t *testing.T) {
	r := newRuntime(t, Config{MemoryLimit: 8 << 20})
	_, err := r.EvalModule(`
		const chunks = [];
		for (let i = 0; i < 4096; i++) chunks.push(new Uint8Array(1 << 20));
		export default String(chunks.length);
	`)
	if err == nil {
		t.Fatal("expected out-of-memory error")
	}
}
