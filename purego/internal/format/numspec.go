package format

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Specifier is a parsed d3-format specifier:
//
//	[[fill]align][sign][symbol][0][width][,][.precision][~][type]
type Specifier struct {
	Fill      string // one UTF-16 unit; " " by default
	Align     byte   // '<' '>' '=' '^'; '>' by default
	Sign      byte   // '-' '+' '(' ' '; '-' by default
	Symbol    string // "", "$" or "#"
	Zero      bool
	Width     float64 // NaN when absent
	Comma     bool
	Precision float64 // NaN when absent
	Trim      bool
	Type      string // "" when absent
}

// ErrInvalidSpecifier is wrapped by the error ParseSpecifier returns.
var ErrInvalidSpecifier = errors.New("invalid format")

// ParseSpecifier is d3's formatSpecifier. The grammar is the regular
// expression /^(?:(.)?([<>=^]))?([+\-( ])?([$#])?(0)?(\d+)?(,)?(\.\d+)?(~)?([a-z%])?$/i;
// in particular any letter is accepted as a type here, and unknown ones are
// treated as the empty type when the format is built.
func ParseSpecifier(spec string) (Specifier, error) {
	s := Specifier{Fill: " ", Align: '>', Sign: '-', Width: math.NaN(), Precision: math.NaN()}
	bad := func() (Specifier, error) {
		return Specifier{}, &specError{spec}
	}
	rest := spec
	// (.)?([<>=^]): "." does not match line terminators or half a surrogate
	// pair. When the first character is itself an alignment character and is
	// followed by another, it is the fill (the group is greedy).
	if r, n := utf8.DecodeRuneInString(rest); n > 0 && r != utf8.RuneError {
		fillOK := r <= 0xFFFF && r != '\n' && r != '\r' && r != 0x2028 && r != 0x2029
		if fillOK && n < len(rest) && isAlign(rune(rest[n])) {
			s.Fill = string(r)
			s.Align = rest[n]
			rest = rest[n+1:]
		} else if isAlign(r) {
			s.Align = byte(r)
			rest = rest[n:]
		}
	}
	if rest != "" && strings.IndexByte("+-( ", rest[0]) >= 0 {
		s.Sign = rest[0]
		rest = rest[1:]
	}
	if rest != "" && (rest[0] == '$' || rest[0] == '#') {
		s.Symbol = rest[:1]
		rest = rest[1:]
	}
	if rest != "" && rest[0] == '0' {
		s.Zero = true
		rest = rest[1:]
	}
	if n := digitRun(rest); n > 0 {
		s.Width = parseDigits(rest[:n])
		rest = rest[n:]
	}
	if rest != "" && rest[0] == ',' {
		s.Comma = true
		rest = rest[1:]
	}
	if len(rest) > 1 && rest[0] == '.' {
		if n := digitRun(rest[1:]); n > 0 {
			s.Precision = parseDigits(rest[1 : 1+n])
			rest = rest[1+n:]
		}
	}
	if rest != "" && rest[0] == '~' {
		s.Trim = true
		rest = rest[1:]
	}
	if rest != "" {
		c := rest[0]
		if c == '%' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			s.Type = rest[:1]
			rest = rest[1:]
		}
	}
	if rest != "" {
		return bad()
	}
	return s, nil
}

type specError struct{ spec string }

func (e *specError) Error() string { return "invalid format: " + e.spec }
func (e *specError) Unwrap() error { return ErrInvalidSpecifier }

func isAlign(r rune) bool { return r == '<' || r == '>' || r == '=' || r == '^' }

func digitRun(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i
}

func parseDigits(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !math.IsInf(f, 0) {
		return math.NaN()
	}
	return f
}

// toInt32 is JavaScript's ToInt32 (x | 0).
func toInt32(f float64) int32 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return int32(uint32(int64(math.Mod(math.Trunc(f), 4294967296))))
}

// String is FormatSpecifier.prototype.toString: the canonical spelling, which
// is what vega-format memoises formats by.
func (s Specifier) String() string {
	var b strings.Builder
	b.WriteString(s.Fill)
	b.WriteByte(s.Align)
	b.WriteByte(s.Sign)
	b.WriteString(s.Symbol)
	if s.Zero {
		b.WriteByte('0')
	}
	if !math.IsNaN(s.Width) {
		b.WriteString(strconv.Itoa(int(max(1, toInt32(s.Width)))))
	}
	if s.Comma {
		b.WriteByte(',')
	}
	if !math.IsNaN(s.Precision) {
		b.WriteByte('.')
		b.WriteString(strconv.Itoa(int(max(0, toInt32(s.Precision)))))
	}
	if s.Trim {
		b.WriteByte('~')
	}
	b.WriteString(s.Type)
	return b.String()
}

// HasPrecision reports whether the specifier carries an explicit precision.
func (s Specifier) HasPrecision() bool { return !math.IsNaN(s.Precision) }

// WithPrecision returns a copy with precision p.
func (s Specifier) WithPrecision(p float64) Specifier {
	s.Precision = p
	return s
}
