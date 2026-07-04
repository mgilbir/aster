package quickjs

import (
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Bridge holds the Go callbacks exposed to JavaScript. Nil fields are not
// registered, and the corresponding __aster_* global stays undefined.
type Bridge struct {
	// Load fetches external data for a URL (async on the JS side).
	Load func(url string) ([]byte, error)
	// Sanitize validates and rewrites a URI before loading.
	Sanitize func(uri string) (string, error)
	// MeasureText returns the pixel width of text under a CSS font spec.
	MeasureText func(text, cssFont string) float64
}

// bridgeFS is a synthetic read-only filesystem mounted into the WASI guest
// at /aster. JavaScript performs synchronous calls into Go by reading
// virtual files: std.loadFile("/aster/<kind>/<arg>/<arg>") where each arg is
// "_"+encodeURIComponent(value). Responses are "O"+payload on success or
// "E"+message for an error the JS side should throw.
//
// Arguments longer than the guest's path budget are first uploaded in chunks
// via "stash/<slot>/_<chunk>" reads and then referenced as "@<slot>".
//
// The QuickJS engine is single-threaded, so Open calls never race.
type bridgeFS struct {
	bridge Bridge
	stash  map[int][]string
}

func newBridgeFS(b Bridge) *bridgeFS {
	return &bridgeFS{bridge: b, stash: make(map[int][]string)}
}

func (f *bridgeFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	// The mount root is opened by wasi-libc when it populates preopens.
	if name == "." {
		return dirFile{}, nil
	}
	segs := strings.Split(name, "/")
	kind, rest := segs[0], segs[1:]

	if kind == "stash" {
		if len(rest) != 2 {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		slot, err := strconv.Atoi(rest[0])
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		chunk, err := decodeSegment(rest[1])
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		f.stash[slot] = append(f.stash[slot], chunk)
		return newMemFile(name, "O"), nil
	}

	args := make([]string, 0, len(rest))
	for _, seg := range rest {
		arg, err := f.decodeArg(seg)
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		args = append(args, arg)
	}
	// Stashed chunks are consumed by the call that references them.
	if len(f.stash) > 0 {
		f.stash = make(map[int][]string)
	}

	content, err := f.dispatch(kind, args)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return newMemFile(name, content), nil
}

func (f *bridgeFS) dispatch(kind string, args []string) (string, error) {
	switch kind {
	case "measure":
		if f.bridge.MeasureText == nil || len(args) != 2 {
			return "", fs.ErrNotExist
		}
		w := f.bridge.MeasureText(args[0], args[1])
		return "O" + strconv.FormatFloat(w, 'g', -1, 64), nil
	case "load":
		if f.bridge.Load == nil || len(args) != 1 {
			return "", fs.ErrNotExist
		}
		data, err := f.bridge.Load(args[0])
		if err != nil {
			return "E" + err.Error(), nil
		}
		return "O" + string(data), nil
	case "sanitize":
		if f.bridge.Sanitize == nil || len(args) != 1 {
			return "", fs.ErrNotExist
		}
		s, err := f.bridge.Sanitize(args[0])
		if err != nil {
			return "E" + err.Error(), nil
		}
		return "O" + s, nil
	default:
		return "", fs.ErrNotExist
	}
}

// decodeArg resolves one path segment to an argument value: either an
// inline "_"-prefixed escaped string or an "@<slot>" stash reference.
func (f *bridgeFS) decodeArg(seg string) (string, error) {
	if strings.HasPrefix(seg, "@") {
		slot, err := strconv.Atoi(seg[1:])
		if err != nil {
			return "", fmt.Errorf("bad stash reference %q", seg)
		}
		chunks, ok := f.stash[slot]
		if !ok {
			return "", fmt.Errorf("unknown stash slot %d", slot)
		}
		return strings.Join(chunks, ""), nil
	}
	return decodeSegment(seg)
}

func decodeSegment(seg string) (string, error) {
	if !strings.HasPrefix(seg, "_") {
		return "", fmt.Errorf("bad argument segment %q", seg)
	}
	return url.PathUnescape(seg[1:])
}

// memFile is an in-memory fs.File serving synthesized response content.
type memFile struct {
	name string
	r    *strings.Reader
	size int64
}

func newMemFile(name, content string) *memFile {
	return &memFile{name: name, r: strings.NewReader(content), size: int64(len(content))}
}

func (m *memFile) Stat() (fs.FileInfo, error) { return memFileInfo{m: m}, nil }
func (m *memFile) Read(p []byte) (int, error) { return m.r.Read(p) }
func (m *memFile) Close() error               { return nil }

// Seek supports wasi fd_seek, which some guest read paths use.
func (m *memFile) Seek(offset int64, whence int) (int64, error) {
	return m.r.Seek(offset, whence)
}

// dirFile is the empty directory served as the bridge mount root.
type dirFile struct{}

func (dirFile) Stat() (fs.FileInfo, error)               { return dirFileInfo{}, nil }
func (dirFile) Read([]byte) (int, error)                 { return 0, &fs.PathError{Op: "read", Path: ".", Err: fs.ErrInvalid} }
func (dirFile) Close() error                             { return nil }
func (dirFile) ReadDir(int) ([]fs.DirEntry, error)       { return nil, io.EOF }

type dirFileInfo struct{}

func (dirFileInfo) Name() string       { return "." }
func (dirFileInfo) Size() int64        { return 0 }
func (dirFileInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o555 }
func (dirFileInfo) ModTime() time.Time { return time.Time{} }
func (dirFileInfo) IsDir() bool        { return true }
func (dirFileInfo) Sys() any           { return nil }

var _ fs.ReadDirFile = dirFile{}

type memFileInfo struct{ m *memFile }

func (fi memFileInfo) Name() string {
	name := fi.m.name
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return name
}
func (fi memFileInfo) Size() int64        { return fi.m.size }
func (fi memFileInfo) Mode() fs.FileMode  { return 0o444 }
func (fi memFileInfo) ModTime() time.Time { return time.Time{} }
func (fi memFileInfo) IsDir() bool        { return false }
func (fi memFileInfo) Sys() any           { return nil }

var _ io.Seeker = (*memFile)(nil)
