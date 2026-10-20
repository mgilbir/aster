package oracle

import (
	"os"
	"testing"
)

// A process the pool stops (replaced after a request that left state, or
// past its answer timeout) is killed and runs no exit handler of its own, so
// its fontconfig directory is removed by the side that stops it. Each such
// directory left behind was one more in the temporary directory for every
// replacement: tens of thousands in a day of differential runs.
func TestStoppedProcessLeavesNoFontDirectory(t *testing.T) {
	o := For(t, VL6)
	p, err := o.start()
	if err != nil {
		t.Fatal(err)
	}
	dir := p.fontDir
	if dir == "" {
		p.stop()
		t.Fatal("the process has no font directory of its own")
	}
	if _, err := os.Stat(dir); err != nil {
		p.stop()
		t.Fatalf("font directory %s: %v", dir, err)
	}
	p.stop()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("stopping the process left %s behind (%v)", dir, err)
	}
}
