package aster

import "regexp"

// engineBugRE matches the error of a recovered panic: the "internal error"
// every layer's recover writes (aster, vega, vegalite, raster, svgpdf), and
// the runtime's own words for one, which a panic value carries. Every test
// that asserts "no panic" and every differential gate reads errors through
// isEngineBug, so they agree on what a panic is (C50, audit 2026-10-18).
var engineBugRE = regexp.MustCompile(`internal error|runtime error|goroutine \d+|nil pointer|index out of range|slice bounds`)

// isEngineBug reports whether err is a recovered panic of the engine, not an
// error it returns for input it refuses.
func isEngineBug(err error) bool { return err != nil && engineBugRE.MatchString(err.Error()) }
