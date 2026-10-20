package imageref

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A panic on one of Fetch's goroutines is an error of Fetch, never the end of
// the process (C15): no recover of the caller's reaches those goroutines. A
// broken contract kills the process, so the fetch runs in a child process of
// the test binary.
func TestFetchPanicIsAnError(t *testing.T) {
	const env = "ASTER_IMAGEREF_FETCH_PANIC"
	if os.Getenv(env) != "" {
		load := func(_ context.Context, href string) ([]byte, error) {
			if href == "bad" {
				panic("bug")
			}
			return []byte("ok"), nil
		}
		_, err := Fetch(context.Background(), []string{"ok", "bad", "ok2"}, load, testLimits)
		if err == nil || !strings.Contains(err.Error(), "internal error") || !strings.Contains(err.Error(), `"bad"`) {
			fmt.Printf("FETCH ERR WRONG: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("FETCH RETURNED")
		os.Exit(0)
	}
	// A hang backstop only.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFetchPanicIsAnError$", "-test.count=1")
	cmd.Env = append(os.Environ(), env+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "FETCH RETURNED") {
		t.Fatalf("%v:\n%.3000s", err, out)
	}
}
