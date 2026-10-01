//go:build unix

package oracle

import (
	"os/exec"
	"syscall"
)

// ownGroup starts the command in a process group of its own. The node on
// PATH may be a version manager's shim (volta) that runs the real node as its
// child; killing the shim alone would leave that child running, still busy
// with the input that made it time out.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup kills the command's whole process group.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		cmd.Process.Kill()
	}
}
