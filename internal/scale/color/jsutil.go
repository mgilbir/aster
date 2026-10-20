package color

import (
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/jsval"
)

func decodeRune(s string) (rune, int)     { return utf8.DecodeRuneInString(s) }
func decodeLastRune(s string) (rune, int) { return utf8.DecodeLastRuneInString(s) }

func lower(s string) string { return jsval.JSLower(s) } // (format + "").trim().toLowerCase()

// parseNum reads a token already validated against the number grammar. Values
// too large for a double become ±Inf, as in JavaScript.
func parseNum(s string) float64 {
	f, _ := jsval.ParseDecimal(s)
	return f
}
