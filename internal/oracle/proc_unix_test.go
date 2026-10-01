//go:build unix

package oracle

import (
	"syscall"
	"testing"
	"time"
)

// Killing a process kills every process of its group: with a version
// manager's shim on PATH the node answering requests is the shim's child,
// and must not be left running. stdin stays open, as for a node busy with an
// input it cannot finish, which would not notice stdin closing.
func TestKillLeavesNoProcessBehind(t *testing.T) {
	o := For(t, VL6)
	p, err := o.start()
	if err != nil {
		t.Fatal(err)
	}
	pgid := p.cmd.Process.Pid
	defer p.stop()
	killGroup(p.cmd)
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(-pgid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("process group %d still has processes after it was killed", pgid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
