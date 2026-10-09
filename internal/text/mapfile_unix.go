//go:build unix

package text

import (
	"os"
	"syscall"
)

// mapFile maps size bytes of f read-only. The mapping outlives f.
func mapFile(f *os.File, size int) ([]byte, error) {
	return syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ, syscall.MAP_SHARED)
}
