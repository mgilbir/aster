//go:build !unix

package aster_test

import "time"

func processCPU() time.Duration { return 0 }
