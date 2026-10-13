//go:build !unix

package aster

// cpuSeconds is 0 where getrusage does not exist: the tests' CPU bounds
// then hold trivially, and their wall-clock bounds still apply.
func cpuSeconds() float64 { return 0 }
