package testutil

import (
	"go/build"
	"go/importer"
	"go/token"
	"go/types"
	"sync"
)

var noCgo sync.Once

// SourceImporter type-checks imported packages from their source, as
// importer.ForCompiler(fset, "source", nil), with cgo off. The source
// importer reads build.Default, whose cgo setting follows the platform the
// test binary runs as: a cross-built one (GOARCH=386 on amd64) would run the
// cgo tool on net's cgo files for a target it has no C compiler for, and fail
// to import anything that reaches net. With cgo off, net is type-checked from
// its pure-Go files, which declare the same API; the tests read Go source
// only.
func SourceImporter(fset *token.FileSet) types.Importer {
	noCgo.Do(func() { build.Default.CgoEnabled = false })
	return importer.ForCompiler(fset, "source", nil)
}
