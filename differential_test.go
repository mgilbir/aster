package aster_test

import (
	"github.com/mgilbir/aster"
)

// Every differential gate (the corpus comparison, the sweeps, the type
// confusion differentials, the compiled-Vega comparisons, the differential
// fuzzer) classifies a case by which side failed. "Both fail" is agreement
// only when the engine refused the input as upstream does: an engine error
// that reports a bug of the engine (a recovered panic) is a failure whatever
// node answered, since node throwing on the same input says nothing about
// the panic (C50, audit 2026-10-18). The gates classify through errorOutcome
// so that no gate can define agreement more coarsely than the others.

// isEngineBug reports whether err is a recovered panic of the engine, not an
// error it returns for input it refuses (see enginebug_test.go).
var isEngineBug = aster.IsEngineBugForTest

// statusInternalError is the status of a case on which the engine panicked.
// No expectation file may list it (checkExpectations refuses it): a panic is
// never an accepted outcome.
const statusInternalError = "internal-error"

// errorOutcome classifies a comparison by its errors, the engine's gerr and
// node's message nodeErr: statusInternalError when the engine panicked
// (whatever node answered), "both-error" when both refuse the input,
// "engine-error" or "node-error" when one side does, and "" when neither
// failed and the outputs are to be compared.
func errorOutcome(gerr error, nodeErr string) string {
	switch {
	case isEngineBug(gerr):
		return statusInternalError
	case gerr != nil && nodeErr != "":
		return "both-error"
	case gerr != nil:
		return "engine-error"
	case nodeErr != "":
		return "node-error"
	}
	return ""
}
