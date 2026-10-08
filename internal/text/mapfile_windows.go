//go:build windows

package text

import (
	"os"
	"syscall"
	"unsafe"
)

// mapFile maps size bytes of f read-only. The mapping outlives f.
func mapFile(f *os.File, size int) ([]byte, error) {
	h, err := syscall.CreateFileMapping(syscall.Handle(f.Fd()), nil, syscall.PAGE_READONLY, 0, 0, nil)
	if err != nil {
		return nil, os.NewSyscallError("CreateFileMapping", err)
	}
	// The view keeps the mapping alive once its handle is closed.
	defer func() { _ = syscall.CloseHandle(h) }()
	addr, err := syscall.MapViewOfFile(h, syscall.FILE_MAP_READ, 0, 0, uintptr(size))
	if err != nil {
		return nil, os.NewSyscallError("MapViewOfFile", err)
	}
	// The view is memory the process did not allocate, so its address is
	// made a pointer by arithmetic from nil rather than by conversion.
	return unsafe.Slice((*byte)(unsafe.Add(nil, addr)), size), nil
}
