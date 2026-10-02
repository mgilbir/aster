//go:build unix

package aster_test

import (
	"syscall"
	"time"
)

// processCPU is the CPU time this process has used (user and system), the
// GC's background workers included. On a loaded machine it moves far less
// than the wall clock does, so a benchmark reports it next to ns/op.
func processCPU() time.Duration {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
