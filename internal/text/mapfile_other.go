//go:build !unix && !windows

package text

import (
	"io"
	"os"
)

// mapFile reads size bytes of f, where the platform has no mapping: within
// the limit on a font given in memory.
func mapFile(f *os.File, size int) ([]byte, error) {
	if size > maxFontBytes {
		return nil, os.ErrInvalid
	}
	b := make([]byte, size)
	_, err := io.ReadFull(f, b)
	return b, err
}
