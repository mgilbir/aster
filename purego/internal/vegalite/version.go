package vegalite

import (
	"fmt"
	"sync"
)

// Vega-Lite versions the compiler can follow (Options.Version).
const (
	// Version64 is Vega-Lite 6.4.3, the default.
	Version64 = "6.4"
	// Version58 is Vega-Lite 5.8.0.
	Version58 = "5.8"
)

// v5 selects Vega-Lite 5.8.0 behaviour wherever it differs from 6.4.3. It is
// package state rather than a parameter because the differences sit in leaf
// helpers (channel tables, time unit parsing, config defaults) that have no
// model or context to carry a version; threading one through every helper
// would touch most of the package.
//
// versionMu makes that safe: a 6.4 compilation holds it for reading, so 6.4
// compilations still run in parallel, and a 5.8 compilation holds it for
// writing while it flips v5, so it never overlaps any other compilation. v5 is
// only assigned with the write lock held.
var (
	v5        bool
	versionMu sync.RWMutex
)

// acquireVersion takes versionMu for a compilation with the given version and
// returns the function releasing it.
func acquireVersion(version string) (release func(), err error) {
	switch version {
	case "", Version64:
		versionMu.RLock()
		return versionMu.RUnlock, nil
	case Version58:
		versionMu.Lock()
		v5 = true
		return func() {
			v5 = false
			versionMu.Unlock()
		}, nil
	}
	return nil, compileError{fmt.Sprintf("unsupported Vega-Lite version %q (supported: %q, %q)", version, Version64, Version58)}
}
