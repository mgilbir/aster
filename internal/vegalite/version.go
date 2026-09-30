package vegalite

import "fmt"

// Vega-Lite versions the compiler can follow (Options.Version).
const (
	// Version64 is Vega-Lite 6.4.3, the default.
	Version64 = "6.4"
	// Version58 is Vega-Lite 5.8.0.
	Version58 = "5.8"
)

// isVersion58 reports whether the version selects Vega-Lite 5.8.0, and an
// error for a version the compiler does not know. The answer lives in the
// per-compilation compileCtx (its v5 field), which the helpers that differ
// between versions take as a parameter, so compilations of different versions
// share no state and never wait for each other.
func isVersion58(version string) (bool, error) {
	switch version {
	case "", Version64:
		return false, nil
	case Version58:
		return true, nil
	}
	return false, compileError{fmt.Sprintf("unsupported Vega-Lite version %q (supported: %q, %q)", version, Version64, Version58)}
}
