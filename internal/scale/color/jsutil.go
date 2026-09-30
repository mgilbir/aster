package color

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

func decodeRune(s string) (rune, int)     { return utf8.DecodeRuneInString(s) }
func decodeLastRune(s string) (rune, int) { return utf8.DecodeLastRuneInString(s) }

func lower(s string) string { return strings.ToLower(s) }

// parseNum reads a token already validated against the number grammar. Values
// too large for a double become ±Inf, as in JavaScript.
func parseNum(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
