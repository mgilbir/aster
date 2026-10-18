// Package textcut cuts text to a length in bytes without splitting a
// character: the one rule every bounded message, warning and label in the
// engine follows, so that two stages that cut the same text agree on what
// it became.
package textcut

import "unicode/utf8"

// Prefix is the longest prefix of s of at most n bytes that ends at a
// character boundary: s itself when it is that short.
func Prefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	n = max(n, 0)
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Cut is s cut to at most n bytes at a character boundary, followed by an
// ellipsis (three bytes more) when anything was cut: at most n+3 bytes
// (C139). Cutting a cut text again, to the same n, changes nothing.
func Cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return Prefix(s, n) + "…"
}
