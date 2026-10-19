package format

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsmath"

	"github.com/mgilbir/aster/internal/jsval"
)

// MaxWidth bounds the field width a format may request. d3 would try to
// allocate an array of that many fill characters; a specification is
// untrusted, so larger widths are rejected when the format is built.
const MaxWidth = 1 << 16

// prefixes are the SI prefixes from yocto (10^-24) to yotta (10^24).
var prefixes = [17]string{"y", "z", "a", "f", "p", "n", "µ", "m", "", "k", "M", "G", "T", "P", "E", "Z", "Y"}

// NumberLocaleDef is a d3-format locale definition (Vega's config.locale.number).
// An absent member takes d3's default for a *missing* member, which is not the
// en-US value: a definition without Thousands or Grouping does no grouping at
// all, and one without Currency has an empty currency symbol.
type NumberLocaleDef struct {
	Decimal   *string
	Thousands *string
	Grouping  []float64 // nil: absent. Non-nil but empty groups to the empty string (as d3 does). Sizes are Number(size): fractions, NaN and infinities are d3's.
	Currency  *[2]string
	Numerals  []string // nil: absent; digits missing from a short list format as "undefined"
	Percent   *string
	Minus     *string
	NaN       *string
}

// NumberLocale builds number formats for one locale definition. It is
// immutable and safe for concurrent use.
type NumberLocale struct {
	grouper                      *grouper // nil: identity
	currencyPrefix, currencySfx  string
	decimal, percent, minus, nan string
	numerals                     []string // nil: identity
	halves                       bool     // a member holds a lone surrogate (its WTF-8 bytes begin with 0xED)
}

type grouper struct {
	// sizes are the group sizes as numbers, as d3 keeps them: a fraction
	// moves the cut by a fraction, which substring truncates.
	sizes []float64
	// ints are the sizes as ints when every one is a whole number from 1
	// to 2^30 (any real locale's): grouping then runs on ints, the same
	// arithmetic exactly, without substring's conversions.
	ints      []int
	thousands string
}

var defaultNumberLocale = mustNumberLocale(NumberLocaleDef{
	Thousands: ptr(","),
	Grouping:  []float64{3},
	Currency:  &[2]string{"$", ""},
})

func mustNumberLocale(d NumberLocaleDef) *NumberLocale {
	l, err := NewNumberLocale(d)
	if err != nil {
		panic(err)
	}
	return l
}

// A locale's members are copied into every number it formats, the numerals
// once per digit and per digit of every other member, so a specification's
// config.locale could make each label megabytes long. The members are
// bounded where the locale is built, far above any real locale: in d3's
// locale files the longest decimal, thousands separator, minus sign or
// numeral is 3 bytes, and the longest currency part or percent sign 10.
const (
	// MaxLocaleSymbol bounds the decimal point, the thousands separator,
	// the minus sign and each numeral, in bytes.
	MaxLocaleSymbol = 16
	// MaxLocaleText bounds each currency part, the percent sign and the
	// text of NaN, in bytes.
	MaxLocaleText = 64
)

// check refuses a definition whose members exceed the bounds above.
func (d NumberLocaleDef) check() error {
	sym := func(name string, v *string, limit int) error {
		if v != nil && len(*v) > limit {
			return fmt.Errorf("%w: number locale %s of %d bytes exceeds %d", budget.ErrLimit, name, len(*v), limit)
		}
		return nil
	}
	err := cmp.Or(
		sym("decimal", d.Decimal, MaxLocaleSymbol), sym("thousands", d.Thousands, MaxLocaleSymbol),
		sym("minus", d.Minus, MaxLocaleSymbol), sym("percent", d.Percent, MaxLocaleText), sym("nan", d.NaN, MaxLocaleText),
	)
	if err == nil && d.Currency != nil {
		err = cmp.Or(sym("currency", &d.Currency[0], MaxLocaleText), sym("currency", &d.Currency[1], MaxLocaleText))
	}
	for i := 0; err == nil && i < len(d.Numerals); i++ {
		err = sym("numeral", &d.Numerals[i], MaxLocaleSymbol)
	}
	return err
}

func ptr[T any](v T) *T { return &v }

// DefaultNumberLocale is d3-format's built-in en-US locale.
func DefaultNumberLocale() *NumberLocale { return defaultNumberLocale }

// NewNumberLocale is d3.formatLocale. It fails, with an error wrapping
// budget.ErrLimit, for a member longer than MaxLocaleSymbol or
// MaxLocaleText.
func NewNumberLocale(def NumberLocaleDef) (*NumberLocale, error) {
	if err := def.check(); err != nil {
		return nil, err
	}
	l := &NumberLocale{decimal: ".", percent: "%", minus: "−", nan: "NaN"}
	if def.Grouping != nil && def.Thousands != nil {
		l.grouper = &grouper{sizes: slices.Clone(def.Grouping), thousands: *def.Thousands}
		ints := make([]int, len(def.Grouping))
		for i, g := range def.Grouping {
			if !(g >= 1 && g <= 1<<30 && g == math.Trunc(g)) {
				ints = nil
				break
			}
			ints[i] = int(g)
		}
		l.grouper.ints = ints
	}
	if def.Currency != nil {
		l.currencyPrefix, l.currencySfx = def.Currency[0], def.Currency[1]
	}
	if def.Decimal != nil {
		l.decimal = *def.Decimal
	}
	if def.Numerals != nil {
		l.numerals = def.Numerals
	}
	if def.Percent != nil {
		l.percent = *def.Percent
	}
	if def.Minus != nil {
		l.minus = *def.Minus
	}
	if def.NaN != nil {
		l.nan = *def.NaN
	}
	for _, m := range append([]string{l.currencyPrefix, l.currencySfx, l.decimal, l.percent, l.minus, l.nan, ""}, l.numerals...) {
		l.halves = l.halves || strings.IndexByte(m, 0xED) >= 0
	}
	if l.grouper != nil {
		l.halves = l.halves || strings.IndexByte(l.grouper.thousands, 0xED) >= 0
	}
	return l, nil
}

// MaxGroupSteps bounds the steps of grouping one number, each of which
// writes a piece and a separator. Whole group sizes take at most one step a
// digit (a field's width, MaxWidth, and a number's digits); d3 also takes a
// size that is a fraction of a digit, stepping back by it, so 0.5 writes a
// separator every half digit, and one too small to move the index (1e-300)
// never ends. Grouping renders what d3 renders up to this many steps and
// stops the call with budget.ErrLimit past it.
const MaxGroupSteps = 1 << 17

// maxGroupSizes is how many group sizes are read: grouping cannot use more
// than its steps, so sizes past them are never reached (and an array-like's
// length is no measure of the work: {length: 1e9} is one).
const maxGroupSizes = MaxGroupSteps

// maxArrayLikeLength is the longest array Array.prototype.map can make:
// past it ArraySpeciesCreate throws a RangeError (Invalid array length).
const maxArrayLikeLength = 1<<32 - 1

// NumberLocaleFromValue reads a locale definition object as found in a Vega
// configuration, as d3-format reads it: grouping and numerals through
// Array.prototype.map.call (any array-like: a string's UTF-16 units, an
// object's length and indexes), the currency as currency[0] and
// currency[1], the rest with String(). It fails where d3 throws (a null
// grouping, numerals or currency).
func NumberLocaleFromValue(v jsval.Value) (*NumberLocale, error) {
	if !v.IsObj() {
		return nil, errors.New("number locale must be an object")
	}
	var def NumberLocaleDef
	str := func(key string) *string {
		m := v.Get(key)
		if m.IsUndefined() {
			return nil
		}
		return ptr(m.AsString())
	}
	def.Decimal, def.Thousands, def.Percent = str("decimal"), str("thousands"), str("percent")
	def.Minus, def.NaN = str("minus"), str("nan")
	// mapped is Array.prototype.map.call(m, f) of its first n elements.
	mapped := func(key string, n int64, f func(jsval.Value)) error {
		a, ok := jsval.ToArrayLike(v.Get(key))
		if !ok {
			return fmt.Errorf("TypeError: number locale %s: Array.prototype.map called on null or undefined", key)
		}
		if a.Len() > maxArrayLikeLength {
			return fmt.Errorf("RangeError: number locale %s: Invalid array length", key)
		}
		for i := range min(a.Len(), n) {
			f(a.At(i))
		}
		return nil
	}
	// d3 maps the grouping only when there is a thousands separator.
	if !v.Get("grouping").IsUndefined() && def.Thousands != nil {
		def.Grouping = []float64{}
		if err := mapped("grouping", maxGroupSizes, func(it jsval.Value) {
			def.Grouping = append(def.Grouping, jsval.ToNumber(it))
		}); err != nil {
			return nil, err
		}
	}
	if c := v.Get("currency"); !c.IsUndefined() {
		if c.IsNull() {
			return nil, errors.New("TypeError: number locale currency: Cannot read properties of null (reading '0')")
		}
		def.Currency = &[2]string{member(c, 0).AsString(), member(c, 1).AsString()}
	}
	if !v.Get("numerals").IsUndefined() {
		def.Numerals = []string{}
		// Only the digits 0 to 9 index the numerals.
		if err := mapped("numerals", 10, func(it jsval.Value) {
			def.Numerals = append(def.Numerals, it.AsString())
		}); err != nil {
			return nil, err
		}
	}
	return NewNumberLocale(def)
}

// member is v[i] for a value that is not null or undefined: a string's i-th
// UTF-16 unit, an array's element, an object's member "i" (whatever its
// length), and undefined for the rest.
func member(v jsval.Value, i int64) jsval.Value {
	if v.IsObj() {
		return v.ObjValue().Lookup(strconv.FormatInt(i, 10))
	}
	a, _ := jsval.ToArrayLike(v)
	return a.At(i)
}

// NumberFormat is a compiled format function. Immutable; safe for concurrent
// use.
type NumberFormat struct {
	loc       *NumberLocale
	spec      Specifier
	fill      string
	fillLen   int // UTF-16 length of fill (always 1 for valid fills)
	align     byte
	sign      byte
	zero      bool
	width     int
	comma     bool
	trim      bool
	typ       byte
	precision int
	prefix    string
	suffix    string
	maybeSufx bool
	// halves: a locale symbol or the fill holds a lone surrogate, which may
	// pair with its neighbour once joined.
	halves bool
	// simpleG: the format is a trimmed "g" with no prefix, suffix, sign,
	// width, grouping or numerals, the shape of the default specifier, whose
	// output appendSimpleG writes without the general machinery.
	simpleG bool
}

// Format is d3.format(specifier) for the locale.
func (l *NumberLocale) Format(specifier string) (*NumberFormat, error) {
	s, err := ParseSpecifier(specifier)
	if err != nil {
		return nil, err
	}
	return l.newFormat(s, "", "")
}

// FormatSpecifier is d3.format for an already parsed specifier.
func (l *NumberLocale) FormatSpecifier(s Specifier) (*NumberFormat, error) {
	return l.newFormat(s, "", "")
}

func (l *NumberLocale) newFormat(s Specifier, extraPrefix, extraSuffix string) (*NumberFormat, error) {
	f := &NumberFormat{loc: l, spec: s, fill: s.Fill, align: s.Align, sign: s.Sign, zero: s.Zero,
		comma: s.Comma, trim: s.Trim, fillLen: jsLen(s.Fill)}
	typ := s.Type
	precision := s.Precision
	switch {
	case typ == "n": // "n" is an alias for ",g"
		f.comma, typ = true, "g"
	case !knownType(typ): // "" and any invalid type are aliases for ".12~g"
		if math.IsNaN(precision) {
			precision = 12
		}
		f.trim, typ = true, "g"
	}
	// If zero fill is specified, padding goes after sign and before digits.
	if f.zero || (f.fill == "0" && f.align == '=') {
		f.zero, f.fill, f.align = true, "0", '='
	}
	f.typ = typ[0]

	f.prefix = extraPrefix
	switch {
	case s.Symbol == "$":
		f.prefix += l.currencyPrefix
	case s.Symbol == "#" && strings.IndexByte("boxX", f.typ) >= 0:
		f.prefix += "0" + jsval.JSLower(typ)
	}
	switch {
	case s.Symbol == "$":
		f.suffix = l.currencySfx
	case f.typ == '%' || f.typ == 'p':
		f.suffix = l.percent
	}
	f.suffix += extraSuffix
	f.maybeSufx = strings.IndexByte("defgprs%", f.typ) >= 0

	// Default precision, or clamp to the supported range: [1, 21] for
	// significant digits, [0, 20] for fixed.
	switch {
	case math.IsNaN(precision):
		f.precision = 6
	case strings.IndexByte("gprs", f.typ) >= 0:
		f.precision = int(math.Max(1, math.Min(21, precision)))
	default:
		f.precision = int(math.Max(0, math.Min(20, precision)))
	}

	w := s.Width
	switch {
	case math.IsNaN(w):
		f.width = 0
	case w > MaxWidth:
		return nil, fmt.Errorf("%w: format width %v exceeds %d", budget.ErrLimit, w, MaxWidth)
	default:
		f.width = int(w)
	}
	f.halves = l.halves || strings.IndexByte(f.fill, 0xED) >= 0
	f.simpleG = f.typ == 'g' && f.trim && f.prefix == "" && f.suffix == "" && f.sign == '-' &&
		f.width == 0 && !f.comma && f.precision <= 14 && l.numerals == nil
	return f, nil
}

func knownType(t string) bool {
	return len(t) == 1 && strings.IndexByte("%bcdeforpgsXxn", t[0]) >= 0
}

// Specifier returns the parsed specifier the format was built from.
func (f *NumberFormat) Specifier() Specifier { return f.spec }

// String is the canonical specifier text.
func (f *NumberFormat) String() string { return f.spec.String() }

// FormatPrefix is d3.formatPrefix(specifier, value): a fixed-point format
// that scales by the SI prefix appropriate for value. Formatting is then
// (*NumberFormat).Format of k*x, which the returned function does.
func (l *NumberLocale) FormatPrefix(specifier string, value float64) (func(float64) string, error) {
	s, err := ParseSpecifier(specifier)
	if err != nil {
		return nil, err
	}
	return l.FormatPrefixSpecifier(s, value)
}

// FormatPrefixSpecifier is FormatPrefix for a parsed specifier; its type is
// replaced by "f".
func (l *NumberLocale) FormatPrefixSpecifier(s Specifier, value float64) (func(float64) string, error) {
	x := decimalExponent(value)
	s.Type = "f"
	// A zero, NaN or infinite reference value makes e NaN in d3; the scale
	// factor is then NaN, no suffix is added, and everything formats as NaN.
	if math.IsNaN(x) {
		f, err := l.newFormat(s, "", "")
		if err != nil {
			return nil, err
		}
		return func(float64) string { return f.Format(math.NaN()) }, nil
	}
	e := int(math.Max(-8, math.Min(8, math.Floor(x/3)))) * 3
	f, err := l.newFormat(s, "", prefixes[8+e/3])
	if err != nil {
		return nil, err
	}
	k := jsmath.Pow(10, float64(-e)) // d3: Math.pow(10, -e)
	return func(v float64) string { return f.Format(k * v) }, nil
}

// Format formats x (the caller applies JavaScript's +value coercion).
func (f *NumberFormat) Format(x float64) string {
	var buf [64]byte
	return string(f.AppendFormat(buf[:0], x))
}

// FormatValue formats a dynamic value: the "c" type writes String(v), every
// other type formats Number(v).
func (f *NumberFormat) FormatValue(w jsval.Walker, v jsval.Value) string {
	if f.typ == 'c' {
		var buf [64]byte
		return string(f.appendFormat(buf[:0], 0, w.String(v)))
	}
	return f.Format(jsval.ToNumber(v))
}

// AppendFormatValue appends what FormatValue returns to dst.
func (f *NumberFormat) AppendFormatValue(w jsval.Walker, dst []byte, v jsval.Value) []byte {
	if f.typ == 'c' {
		return f.appendFormat(dst, 0, w.String(v))
	}
	return f.AppendFormat(dst, jsval.ToNumber(v))
}

// AppendFormat appends the formatted x to dst.
func (f *NumberFormat) AppendFormat(dst []byte, x float64) []byte {
	if f.typ == 'c' {
		return f.appendFormat(dst, x, jsval.JSNumberString(x))
	}
	return f.appendFormat(dst, x, "")
}

func (f *NumberFormat) appendFormat(dst []byte, x float64, cText string) []byte {
	start := len(dst)
	out := f.appendParts(dst, x, cText)
	if f.halves || (f.typ == 'c' && strings.IndexByte(cText, 0xED) >= 0) {
		// Pieces that end and begin with half a character (a locale's
		// symbols, a string) join into one, as JavaScript's + joins them.
		out = jsval.PairSurrogates(out, start)
	}
	return out
}

func (f *NumberFormat) appendParts(dst []byte, x float64, cText string) []byte {
	if f.simpleG {
		if out, ok := f.appendSimpleG(dst, x); ok {
			return out
		}
	}
	loc := f.loc
	valuePrefix, valueSuffix := f.prefix, f.suffix
	var body string

	if f.typ == 'c' {
		valueSuffix = cText + valueSuffix
	} else {
		// -0 is not less than 0, but its sign bit is set.
		negative := x < 0 || (x == 0 && math.Signbit(x))
		var pexp int
		var hasPexp bool
		if math.IsNaN(x) {
			body = loc.nan
		} else {
			body, pexp, hasPexp = formatBody(f.typ, math.Abs(x), f.precision)
		}
		if f.trim {
			body = trimZeros(body)
		}
		// A negative value that rounds to zero after formatting loses its
		// sign unless an explicit "+" was asked for.
		if negative && jsval.StringToNumber(body) == 0 && f.sign != '+' {
			negative = false
		}
		switch {
		case negative && f.sign == '(':
			valuePrefix = "(" + valuePrefix
		case negative:
			valuePrefix = loc.minus + valuePrefix
		case f.sign != '-' && f.sign != '(':
			valuePrefix = string(f.sign) + valuePrefix
		}
		if f.typ == 's' && hasPexp && !math.IsNaN(jsval.StringToNumber(body)) {
			valueSuffix = prefixes[8+pexp/3] + valueSuffix
		}
		if negative && f.sign == '(' {
			valueSuffix += ")"
		}
		// Break the formatted value into the integer part that can be
		// grouped and the fractional or exponential part that cannot.
		if f.maybeSufx {
			for i := 0; i < len(body); i++ {
				if c := body[i]; c < '0' || c > '9' {
					if c == '.' {
						valueSuffix = loc.decimal + body[i+1:] + valueSuffix
					} else {
						valueSuffix = body[i:] + valueSuffix
					}
					body = body[:i]
					break
				}
			}
		}
	}

	// With a fill other than "0", grouping is applied before padding.
	if f.comma && !f.zero {
		body = loc.group(body, math.Inf(1))
	}
	length := jsLen(valuePrefix) + jsLen(body) + jsLen(valueSuffix)
	padN := 0
	if length < f.width {
		padN = f.width - length
	}
	if f.comma && f.zero {
		w := math.Inf(1)
		if padN > 0 {
			w = float64(f.width - jsLen(valueSuffix))
		}
		body = loc.group(strings.Repeat("0", padN)+body, w)
		padN = 0
	}

	fillOut := func(n int) {
		for i := 0; i < n; i++ {
			dst = append(dst, f.fill...)
		}
	}
	start := len(dst)
	switch f.align {
	case '<':
		dst = append(append(append(dst, valuePrefix...), body...), valueSuffix...)
		fillOut(padN)
	case '=':
		dst = append(dst, valuePrefix...)
		fillOut(padN)
		dst = append(append(dst, body...), valueSuffix...)
	case '^':
		fillOut(padN >> 1)
		dst = append(append(append(dst, valuePrefix...), body...), valueSuffix...)
		fillOut(padN - padN>>1)
	default:
		fillOut(padN)
		dst = append(append(append(dst, valuePrefix...), body...), valueSuffix...)
	}
	if loc.numerals != nil {
		return loc.applyNumerals(dst, start)
	}
	return dst
}

func (l *NumberLocale) applyNumerals(dst []byte, start int) []byte {
	out := make([]byte, 0, len(dst)+8)
	out = append(out, dst[:start]...)
	for _, c := range dst[start:] {
		if c >= '0' && c <= '9' {
			d := int(c - '0')
			if d < len(l.numerals) {
				out = append(out, l.numerals[d]...)
			} else {
				out = append(out, "undefined"...)
			}
			continue
		}
		out = append(out, c)
	}
	return out
}

// group is d3's formatGroup: insert the thousands separator into the digits
// of value, right to left, in the locale's group sizes (the last size
// repeating). width limits the grouped text to that many UTF-16 units, which
// is how zero padding is made to fill a field (+Inf: no limit).
//
// The arithmetic is d3's, in numbers: `t.push(value.substring(i -= g, i +
// g))` with a fractional size moves the cut by a fraction that substring
// truncates, a huge one loses the digits to rounding, an infinite one cuts
// nothing, and NaN or a size of 0 ends the loop. A size below 1 takes more
// steps than digits, up to MaxGroupSteps, past which the call stops with
// budget.ErrLimit (a *budget.Stop panic, as a walk's bound stops a call).
func (l *NumberLocale) group(value string, width float64) string {
	if l.grouper == nil {
		return value
	}
	sizes := l.grouper.sizes
	// The digits are ASCII except when a locale's NaN text is grouped by a
	// type that never splits off a suffix; index by UTF-16 units then.
	var units []uint16
	n := len(value)
	if !isASCIIString(value) {
		units = jsval.UTF16Units(value)
		n = len(units)
	}
	var pieceBuf [16][2]int
	pieces := pieceBuf[:0] // [start, end) of each piece, collected right to left
	if ints := l.grouper.ints; len(ints) > 0 {
		w := math.MaxInt
		if width < float64(math.MaxInt>>1) {
			w = int(width) // a field width: a whole number
		}
		i, length, j, g := n, 0, 0, ints[0]
		for i > 0 && g > 0 {
			if length+g+1 > w {
				g = max(1, w-length)
			}
			pieces = append(pieces, [2]int{max(0, i-g), i})
			i -= g
			length += g + 1
			if length > w {
				break
			}
			j = (j + 1) % len(ints)
			g = ints[j]
		}
		return l.joinPieces(value, units, pieces)
	}
	i, length := float64(n), 0.0
	j := 0
	g := math.NaN() // grouping[0] of an empty grouping is undefined
	if len(sizes) > 0 {
		g = sizes[0]
	}
	for i > 0 && g > 0 {
		if len(pieces) == MaxGroupSteps {
			panic(&budget.Stop{Err: errGroupSteps})
		}
		if length+g+1 > width {
			g = math.Max(1, width-length)
		}
		i -= g
		pieces = append(pieces, substringBounds(i, i+g, n))
		length += g + 1
		if length > width {
			break
		}
		j = (j + 1) % len(sizes)
		g = sizes[j]
	}
	return l.joinPieces(value, units, pieces)
}

var errGroupSteps = fmt.Errorf("%w: grouping a number took more than %d steps (a group size of a fraction of a digit)", budget.ErrLimit, MaxGroupSteps)

// joinPieces is t.reverse().join(thousands) of the pieces of value (of its
// UTF-16 units when it is not ASCII).
func (l *NumberLocale) joinPieces(value string, units []uint16, pieces [][2]int) string {
	if len(pieces) == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value) + len(pieces)*len(l.grouper.thousands))
	for k := len(pieces) - 1; k >= 0; k-- {
		p := pieces[k]
		if units != nil {
			b.WriteString(jsval.FromUTF16(units[p[0]:p[1]]))
		} else {
			b.WriteString(value[p[0]:p[1]])
		}
		if k > 0 {
			b.WriteString(l.grouper.thousands)
		}
	}
	return b.String()
}

// substringBounds is the range String.prototype.substring(a, b) takes of a
// string of n units: each end ToIntegerOrInfinity (NaN is 0) clamped to
// [0, n], in order.
func substringBounds(a, b float64, n int) [2]int {
	clamp := func(x float64) int {
		switch {
		case math.IsNaN(x) || x <= 0:
			return 0
		case x >= float64(n):
			return n
		}
		return int(x) // truncation toward zero; x is positive here
	}
	s, e := clamp(a), clamp(b)
	if s > e {
		s, e = e, s
	}
	return [2]int{s, e}
}

func isASCIIString(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// jsLen is String.prototype.length: UTF-16 code units.
func jsLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			i++
			n++
			continue
		}
		r, w := utf8.DecodeRuneInString(s[i:])
		i += w
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// formatBody is d3's formatTypes[type](abs(x), precision). For type "s" it
// also returns the SI exponent (the module-level prefixExponent in d3).
func formatBody(typ byte, x float64, p int) (s string, pexp int, hasPexp bool) {
	switch typ {
	case '%':
		return toFixed(x*100, p), 0, false
	case 'b':
		return roundedInt(x, 2, false), 0, false
	case 'd':
		return formatDecimalInt(x), 0, false
	case 'e':
		return toExponential(x, p), 0, false
	case 'f':
		return toFixed(x, p), 0, false
	case 'g':
		return toPrecision(x, p), 0, false
	case 'o':
		return roundedInt(x, 8, false), 0, false
	case 'p':
		return formatRounded(x*100, p), 0, false
	case 'r':
		return formatRounded(x, p), 0, false
	case 's':
		return formatPrefixAuto(x, p)
	case 'X':
		return roundedInt(x, 16, true), 0, false
	case 'x':
		return roundedInt(x, 16, false), 0, false
	}
	return toPrecision(x, p), 0, false // unreachable: aliased to "g"
}

// roundedInt is Math.round(x).toString(radix) for a power-of-two radix.
func roundedInt(x float64, radix int, upper bool) string {
	var s string
	r := jsmath.Round(x)
	if math.IsInf(x, 0) {
		s = "Infinity"
	} else if r < 1<<63 {
		s = strconv.FormatUint(uint64(r), radix)
	} else {
		bi, _ := new(big.Float).SetFloat64(r).Int(nil)
		s = bi.Text(radix)
	}
	if upper {
		s = jsval.JSUpper(s)
	}
	return s
}

// formatDecimalInt is d3's "d": Math.round(x) as a plain integer string.
// Beyond 1e21 JavaScript switches to exponent notation, so d3 asks ICU
// (toLocaleString) for the digits, which writes the shortest round-trip
// digits followed by zeros.
func formatDecimalInt(x float64) string {
	if math.IsInf(x, 0) {
		return "∞"
	}
	r := jsmath.Round(x)
	if r < 1e21 {
		return jsval.JSNumberString(r)
	}
	digits, e := sigDigitsShortest(r)
	if len(digits) <= e+1 {
		return digits + strings.Repeat("0", e+1-len(digits))
	}
	return digits[:e+1] + digits[e+1:]
}

func sigDigitsShortest(x float64) (string, int) {
	d, e, _ := decimalParts(x, 0)
	return d, e
}

// formatRounded is d3's formatRounded ("r" and "p"): p significant digits in
// plain (never exponential) notation.
func formatRounded(x float64, p int) string {
	coef, exp, ok := decimalParts(x, p)
	if !ok {
		return jsval.JSNumberString(x)
	}
	switch {
	case exp < 0:
		return "0." + strings.Repeat("0", -exp-1) + coef
	case len(coef) > exp+1:
		return coef[:exp+1] + "." + coef[exp+1:]
	default:
		return coef + strings.Repeat("0", exp-len(coef)+1)
	}
}

// formatPrefixAuto is d3's "s" body: the digits scaled to the SI prefix of
// x's magnitude; the prefix exponent is reported for the suffix.
func formatPrefixAuto(x float64, p int) (string, int, bool) {
	coef, exp, ok := decimalParts(x, p)
	if !ok {
		return toPrecisionAny(x, p), 0, false
	}
	pe := int(math.Max(-8, math.Min(8, math.Floor(float64(exp)/3)))) * 3
	i := exp - pe + 1
	n := len(coef)
	switch {
	case i == n:
		return coef, pe, true
	case i > n:
		return coef + strings.Repeat("0", i-n), pe, true
	case i > 0:
		return coef[:i] + "." + coef[i:], pe, true
	}
	// Less than 1y: the extra leading zeros use up significant digits.
	c2, _, _ := decimalParts(x, max(0, p+i-1))
	return "0." + strings.Repeat("0", -i) + c2, pe, true
}

// toPrecisionAny is toPrecision for zero and non-finite x.
func toPrecisionAny(x float64, p int) string {
	if x == 0 || math.IsInf(x, 0) || math.IsNaN(x) {
		return jsval.ToPrecision(x, p)
	}
	return toPrecision(x, p)
}

// trimZeros is d3's formatTrim: drop insignificant zeros of a formatted
// number ("1.2000k" becomes "1.2k"). It stops at the first character that is
// not a digit.
func trimZeros(s string) string {
	i0, i1 := -1, 0
out:
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '.':
			i0, i1 = i, i
		case c == '0':
			if i0 == 0 {
				i0 = i
			}
			i1 = i
		case c >= '1' && c <= '9':
			if i0 > 0 {
				i0 = 0
			}
		default:
			break out
		}
	}
	if i0 > 0 {
		return s[:i0] + s[i1+1:]
	}
	return s
}

// PrecisionFixed is d3.precisionFixed(step).
func PrecisionFixed(step float64) float64 {
	return math.Max(0, -decimalExponent(math.Abs(step)))
}

// PrecisionPrefix is d3.precisionPrefix(step, value).
func PrecisionPrefix(step, value float64) float64 {
	return math.Max(0, float64(math.Max(-8, math.Min(8, math.Floor(decimalExponent(value)/3)))*3)-decimalExponent(math.Abs(step)))
}

// PrecisionRound is d3.precisionRound(step, max).
func PrecisionRound(step, max float64) float64 {
	step = math.Abs(step)
	max = math.Abs(max) - step
	return math.Max(0, decimalExponent(max)-decimalExponent(step)) + 1
}

// appendSimpleG formats x as a trimmed "g" does when the result is positional
// and exact: x finite and not zero, its shortest digits no more than the
// precision, and its exponent from -6 up to the precision (toPrecision's
// positional range). The shortest digits then are the digits toPrecision
// rounds to, and the trimmed body is them placed around the decimal point. It
// reports false for everything else, and the general path formats it.
func (f *NumberFormat) appendSimpleG(dst []byte, x float64) ([]byte, bool) {
	if x == 0 || math.IsNaN(x) || math.IsInf(x, 0) {
		return dst, false
	}
	var tmp [40]byte
	var dbuf [24]byte
	digits, e := parseExp(dbuf[:0], strconv.AppendFloat(tmp[:0], math.Abs(x), 'e', -1, 64))
	if len(digits) > f.precision || e < -6 || e >= f.precision {
		return dst, false
	}
	loc := f.loc
	if x < 0 {
		dst = append(dst, loc.minus...)
	}
	switch {
	case e < 0:
		dst = append(dst, '0')
		dst = append(dst, loc.decimal...)
		for i := 0; i < -(e + 1); i++ {
			dst = append(dst, '0')
		}
		dst = append(dst, digits...)
	case len(digits) <= e+1:
		dst = append(dst, digits...)
		for i := len(digits); i < e+1; i++ {
			dst = append(dst, '0')
		}
	default:
		dst = append(dst, digits[:e+1]...)
		dst = append(dst, loc.decimal...)
		dst = append(dst, digits[e+1:]...)
	}
	return dst, true
}
