package aster

import (
	"flag"
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/vega"
)

// Every render of the package's tests checks that the rows the dataflow
// makes are all held by an operator, so charged to the row budget (ADR
// 0012); benchmarks measure the engine without the check.
func TestMain(m *testing.M) {
	flag.Parse()
	if b := flag.Lookup("test.bench"); b == nil || b.Value.String() == "" {
		vega.CheckHeldForTest()
	}
	os.Exit(m.Run())
}
