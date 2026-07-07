package quickjs

import (
	"os"
	"regexp"
	"testing"
)

// The embedded quickjs.wasm is built from the tag in quickjs-wasm/Dockerfile.
// Version is documented to track that ARG; this test fails if they drift.
func TestVersionMatchesDockerfile(t *testing.T) {
	data, err := os.ReadFile("../../quickjs-wasm/Dockerfile")
	if err != nil {
		t.Fatalf("reading Dockerfile: %v", err)
	}
	re := regexp.MustCompile(`(?m)^ARG QUICKJS_NG_VERSION=(\S+)`)
	m := re.FindSubmatch(data)
	if m == nil {
		t.Fatal("could not find ARG QUICKJS_NG_VERSION in Dockerfile")
	}
	if got := string(m[1]); got != Version {
		t.Errorf("quickjs.Version = %q but Dockerfile pins %q; keep them in sync", Version, got)
	}
}
