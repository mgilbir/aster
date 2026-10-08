package text

import (
	"io/fs"
	"os"
	"sync"
)

// maxSystemFontBytes bounds a system font file. It is mapped, not read, so
// only the pages a face reads are ever loaded: Apple Color Emoji is 190 MB.
const maxSystemFontBytes = 1 << 30

// mapped holds the system font files mapped so far, by path, for the life of
// the process: a face reads its file for as long as it is in use, and a
// Measurer's faces are not tracked once it is dropped.
var mapped struct {
	sync.Mutex
	m map[string][]byte
}

// mapSystemFont returns the contents of a system font file, mapped read-only
// into memory where the platform can, once per process.
func mapSystemFont(path string) ([]byte, error) {
	mapped.Lock()
	defer mapped.Unlock()
	if b, ok := mapped.m[path]; ok {
		return b, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if !info.Mode().IsRegular() || size <= 0 || size > maxSystemFontBytes {
		return nil, fs.ErrInvalid
	}
	b, err := mapFile(f, int(size))
	if err != nil {
		return nil, err
	}
	if mapped.m == nil {
		mapped.m = map[string][]byte{}
	}
	mapped.m[path] = b
	return b, nil
}
