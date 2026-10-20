package csscolor

// CSS reads its keywords, function names, property names, units and font
// family names ASCII case-insensitively (CSS Syntax §2.1, CSS Fonts §5.1):
// only A-Z fold to a-z. Go's strings.ToLower and EqualFold fold Unicode too
// (İ to "i", the Kelvin sign K to "k"), which would make "whİte" a colour
// and "stroKe" a property; these helpers are the fold CSS asks for.

// ASCIILower is s with its ASCII letters lower-cased and every other byte
// kept. It allocates only when s has an upper-case ASCII letter.
func ASCIILower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if c := b[j]; c >= 'A' && c <= 'Z' {
					b[j] = c + 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// ASCIIEqualFold reports whether a and b are equal with ASCII letters
// compared case-insensitively.
func ASCIIEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
