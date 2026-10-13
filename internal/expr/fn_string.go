package expr

import (
	"encoding/base64"
	"math"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/mgilbir/aster/internal/jsval"
)

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

func trimJSSpace(s string) string { return strings.TrimFunc(s, isJSSpace) }

// sliceIndex resolves a relative index argument of slice/substring style
// methods against length n (negative counts from the end).
func sliceIndex(f float64, n int) int {
	f = toInteger(f)
	if f < 0 {
		f += float64(n)
		if f < 0 {
			return 0
		}
		return int(f)
	}
	if f > float64(n) {
		return n
	}
	return int(f)
}

// sliceUnits is s.slice(start, end) with float arguments; end is NaN-free
// "absent" when hasEnd is false.
func (s *Scope) sliceUnits(str string, start float64, end float64, hasEnd bool) string {
	n := utf16Len(str)
	from := sliceIndex(start, n)
	to := n
	if hasEnd {
		to = sliceIndex(end, n)
	}
	return s.substr(str, from, to)
}

// padded is repeat(c, left) + str + repeat(c, right) as vega-util's pad
// builds it: repeat is a loop of +=, so the copies of c, and c and str, join
// where a high surrogate meets a low one. It is built in one buffer whose
// length is checked, and charged, before it is made.
func (s *Scope) padded(c string, left float64, str string, right float64) string {
	reps := func(r float64) int {
		if math.IsNaN(r) || r < 1 || c == "" {
			return 0
		}
		return int(min(math.Floor(r), v8MaxStringLength+1))
	}
	l, r := reps(left), reps(right)
	if n := len(c)*(l+r) + len(str); n <= smallString {
		// A label: under the free size, so under every limit.
		b := make([]byte, 0, n)
		for range l {
			b = appendJoin(b, c)
		}
		b = appendJoin(b, str)
		for range r {
			b = appendJoin(b, c)
		}
		return unsafe.String(unsafe.SliceData(b), len(b))
	}
	cu := float64(utf16Len(c))
	s.checkUnits(float64(float64(l+r)*cu) + float64(utf16Len(str)))
	b := newStrBuilder(s)
	b.reserve(len(c)*(l+r) + len(str))
	b.addRepeat(c, l)
	b.add(str)
	b.addRepeat(c, r)
	return b.String()
}

var (
	upperCaser = cases.Upper(language.Und)
	lowerCaser = cases.Lower(language.Und)
	caseMu     sync.Mutex
)

// jsUpper and jsLower are toUpperCase / toLowerCase: full Unicode case mapping
// (ß -> SS, final sigma, İ). cases.Caser is stateful, hence the lock; ASCII
// strings never reach it.
func jsUpper(s string) string {
	if isASCII(s) {
		return strings.ToUpper(s)
	}
	caseMu.Lock()
	defer caseMu.Unlock()
	upperCaser.Reset()
	return upperCaser.String(s)
}

func jsLower(s string) string {
	if isASCII(s) {
		return strings.ToLower(s)
	}
	caseMu.Lock()
	defer caseMu.Unlock()
	lowerCaser.Reset()
	return lowerCaser.String(s)
}

// jsParseFloat is the global parseFloat: the longest prefix that is a decimal
// literal (or Infinity), after leading whitespace.
func jsParseFloat(str string) float64 {
	str = strings.TrimLeftFunc(str, isJSSpace)
	i := 0
	if i < len(str) && (str[i] == '+' || str[i] == '-') {
		i++
	}
	if strings.HasPrefix(str[i:], "Infinity") {
		if str[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	digits := 0
	for i < len(str) && str[i] >= '0' && str[i] <= '9' {
		i++
		digits++
	}
	if i < len(str) && str[i] == '.' {
		j := i + 1
		fd := 0
		for j < len(str) && str[j] >= '0' && str[j] <= '9' {
			j++
			fd++
		}
		if digits > 0 || fd > 0 {
			i = j
			digits += fd
		}
	}
	if digits == 0 {
		return math.NaN()
	}
	if i < len(str) && (str[i] == 'e' || str[i] == 'E') {
		j := i + 1
		if j < len(str) && (str[j] == '+' || str[j] == '-') {
			j++
		}
		k := j
		for k < len(str) && str[k] >= '0' && str[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	f, err := strconv.ParseFloat(str[:i], 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); !ok || ne.Err != strconv.ErrRange {
			return math.NaN()
		}
	}
	return f
}

// jsParseInt is the global parseInt(string, radix).
func jsParseInt(str string, radix int32) float64 {
	str = strings.TrimLeftFunc(str, isJSSpace)
	sign := 1.0
	if str != "" && (str[0] == '+' || str[0] == '-') {
		if str[0] == '-' {
			sign = -1
		}
		str = str[1:]
	}
	r := int(radix)
	strip := true
	if r != 0 {
		if r < 2 || r > 36 {
			return math.NaN()
		}
		if r != 16 {
			strip = false
		}
	} else {
		r = 10
	}
	if strip && len(str) >= 2 && str[0] == '0' && (str[1] == 'x' || str[1] == 'X') {
		str = str[2:]
		r = 16
	}
	end := 0
	for end < len(str) {
		d := digitValue(str[end])
		if d < 0 || d >= r {
			break
		}
		end++
	}
	if end == 0 {
		return math.NaN()
	}
	digits := str[:end]
	if r == 10 {
		f, _ := strconv.ParseFloat(digits, 64)
		return sign * f
	}
	// big.Int parses in time quadratic in the digits. Past 1100 significant
	// digits the value is beyond the largest double in any radix (2^1100), so
	// it is Infinity without parsing.
	digits = strings.TrimLeft(digits, "0")
	if len(digits) > 1100 {
		return sign * math.Inf(1)
	}
	var z big.Int
	z.SetString(digits, r)
	f, _ := new(big.Float).SetInt(&z).Float64()
	return sign * f
}

func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return -1
}

// escapeSource is EscapeRegExpPattern: what RegExp.prototype.source reports for
// a pattern built from a string ("/" and line terminators escaped, empty
// pattern "(?:)"). escapedLen says first how long the result is, and whether
// making it copies src, so that a caller can pay for the copy before it is
// made; n and copied are its results.
func escapeSource(src string, n int, copied bool) string {
	if !copied {
		if src == "" {
			return "(?:)"
		}
		return src
	}
	var b strings.Builder
	b.Grow(n)
	escapeWalk(&escapeSink{b: &b}, src)
	return b.String()
}

// escapedLen is the length of escapeSource(src), and whether it is a new
// string rather than src or a constant. It reads src and allocates nothing.
func escapedLen(src string) (n int, copied bool) {
	if src == "" {
		return len("(?:)"), false
	}
	if !strings.ContainsAny(src, "/\n\r\u2028\u2029") {
		return len(src), false
	}
	var c escapeSink
	escapeWalk(&c, src)
	return c.n, true
}

// escapeSink is what escapeWalk writes to: a builder, or with no builder a
// count of the bytes it would write.
type escapeSink struct {
	n int
	b *strings.Builder
}

func (e *escapeSink) str(s string) {
	e.n += len(s)
	if e.b != nil {
		e.b.WriteString(s) //nolint:errcheck // a Builder's writes never fail
	}
}

func (e *escapeSink) rune(r rune) {
	e.n += utf8.RuneLen(r) // r is decoded from a string: valid, or U+FFFD
	if e.b != nil {
		e.b.WriteRune(r) //nolint:errcheck // a Builder's writes never fail
	}
}

// escapeWalk writes the escaped form of src (one that has something to
// escape) to b: the one rule, run to count and run to build, so the two
// cannot differ.
func escapeWalk(b *escapeSink, src string) {
	inClass := false
	for i := 0; i < len(src); {
		r, w := utf8.DecodeRuneInString(src[i:])
		switch {
		case r == '\\' && i+w < len(src):
			b.str(src[i : i+w])
			i += w
			r2, w2 := utf8.DecodeRuneInString(src[i:])
			switch r2 {
			case '\n':
				b.str("n")
			case '\r':
				b.str("r")
			case 0x2028:
				b.str("u2028")
			case 0x2029:
				b.str("u2029")
			default:
				b.str(src[i : i+w2])
			}
			i += w2
			continue
		case r == '/' && !inClass:
			b.str("\\/")
		case r == '[':
			inClass = true
			b.rune(r)
		case r == ']':
			inClass = false
			b.rune(r)
		case r == '\n':
			b.str("\\n")
		case r == '\r':
			b.str("\\r")
		case r == 0x2028:
			b.str("\\u2028")
		case r == 0x2029:
			b.str("\\u2029")
		default:
			b.rune(r)
		}
		i += w
	}
}

// canonicalFlags orders RegExp flags as RegExp.prototype.flags reports them.
// Invalid or repeated flags are left for the compiler to reject.
func canonicalFlags(flags string) string {
	if len(flags) < 2 {
		return flags
	}
	var out []byte
	for _, f := range []byte("dgimsuvy") {
		if strings.IndexByte(flags, f) >= 0 {
			out = append(out, f)
		}
	}
	if len(out) != len(flags) {
		return flags
	}
	return string(out)
}

// caseMapped is upper or lower of str: the result can be longer than str
// (ß is SS), so its length is checked and the budget charged for it once it
// is made, with what str's length predicts charged before.
func (s *Scope) caseMapped(str string, f func(string) string) string {
	if len(str) > smallString {
		s.chargeBytes(len(str))
		s.workBytes(len(str))
	}
	out := f(str)
	if len(out) > smallString && (len(out) > len(str) || len(out) > MaxStringLength) {
		s.caseGrown(len(str), out)
	}
	return out
}

// caseGrown checks and charges a case mapping that came out longer.
func (s *Scope) caseGrown(was int, out string) {
	s.checkString(out)
	if len(out) > was {
		s.chargeRaw(int64(len(out) - max(was, smallString)))
	}
}

func init() {
	def("length", &funcDef{min: 1, fn: func(s *Scope, args []jsval.Value) jsval.Value { return s.getProp(args[0], "length") },
		// length(1) generates `1.length`, which is a syntax error upstream.
		check: func(n *Node) error {
			if bareInteger(n.Elems[0]) {
				return cerr("Invalid or unexpected token")
			}
			return nil
		}})
	fn("parseFloat", func(s *Scope, args []jsval.Value) jsval.Value {
		str := s.str(arg(args, 0))
		s.workBytes(len(str))
		return jsval.Num(jsParseFloat(str))
	})
	fn("parseInt", func(s *Scope, args []jsval.Value) jsval.Value {
		str := s.str(arg(args, 0))
		s.workBytes(len(str))
		return jsval.Num(jsParseInt(str, toInt32(s.num(arg(args, 1)))))
	})
	fnMin("upper", 1, func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Str(s.caseMapped(s.str(args[0]), jsUpper))
	})
	fnMin("lower", 1, func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Str(s.caseMapped(s.str(args[0]), jsLower))
	})
	fnMin("trim", 1, func(s *Scope, args []jsval.Value) jsval.Value {
		str := s.str(args[0])
		s.workBytes(len(str))
		return jsval.Str(trimJSSpace(str))
	})
	fnMin("substring", 1, func(s *Scope, args []jsval.Value) jsval.Value {
		str := s.str(args[0])
		n := utf16Len(str)
		clampIdx := func(v jsval.Value, def int) int {
			if v.IsUndefined() {
				return def
			}
			f := toInteger(s.num(v))
			if f < 0 {
				return 0
			}
			if f > float64(n) {
				return n
			}
			return int(f)
		}
		a, b := clampIdx(arg(args, 1), 0), clampIdx(arg(args, 2), n)
		if a > b {
			a, b = b, a
		}
		return jsval.Str(s.substr(str, a, b))
	})
	fnMin("split", 1, func(s *Scope, args []jsval.Value) jsval.Value {
		str := s.str(args[0])
		sep, lim := arg(args, 1), arg(args, 2)
		limit := uint32(math.MaxUint32)
		if !lim.IsUndefined() {
			limit = toUint32(s.num(lim))
		}
		if limit == 0 {
			return jsval.Arr(nil)
		}
		switch {
		case sep.IsUndefined():
			return jsval.ArrOf(jsval.Str(str))
		case sep.IsPattern():
			return jsval.Arr(s.regexpSplit(sep.PatternOf(), str, limit))
		}
		return jsval.Arr(s.stringSplit(str, s.str(sep), limit))
	})
	fn("btoa", func(s *Scope, args []jsval.Value) jsval.Value {
		str := s.str(arg(args, 0))
		s.chargeBytes(len(str) + base64.StdEncoding.EncodedLen(len(str)))
		s.workBytes(len(str))
		b := make([]byte, 0, len(str))
		for _, r := range str {
			if r > 0xFF {
				throw("Error", "Invalid character")
			}
			b = append(b, byte(r))
		}
		return jsval.Str(base64.StdEncoding.EncodeToString(b))
	})
	fn("atob", func(s *Scope, args []jsval.Value) jsval.Value {
		in := s.str(arg(args, 0))
		// The output is at most three quarters of the input, each byte up to
		// two in UTF-8, and the input is copied once without white space.
		s.chargeBytes(3 * len(in))
		s.workBytes(len(in))
		str := strings.Map(func(r rune) rune {
			switch r {
			case ' ', '\t', '\n', '\f', '\r':
				return -1
			}
			return r
		}, in)
		if len(str)%4 == 0 {
			str = strings.TrimSuffix(strings.TrimSuffix(str, "=="), "=")
		}
		b, err := base64.RawStdEncoding.DecodeString(str)
		if err != nil {
			throw("Error", "The string to be decoded is not correctly encoded.")
		}
		out := make([]byte, 0, len(b)+len(b)/2)
		for _, c := range b {
			out = utf8.AppendRune(out, rune(c))
		}
		return jsval.Str(string(out))
	})
	fn("encodeURIComponent", func(s *Scope, args []jsval.Value) jsval.Value {
		str := s.str(arg(args, 0))
		b := newStrBuilder(s)
		const hexd = "0123456789ABCDEF"
		esc := func(x byte) {
			b.addByte('%')
			b.addByte(hexd[x>>4])
			b.addByte(hexd[x&15])
		}
		for i := 0; i < len(str); {
			c := str[i]
			if c < utf8.RuneSelf {
				if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0 {
					b.addByte(c)
				} else {
					esc(c)
				}
				i++
				continue
			}
			r, w := utf8.DecodeRuneInString(str[i:])
			if r == utf8.RuneError && w <= 1 || c == 0xED && i+1 < len(str) && str[i+1] >= 0xA0 {
				throw("URIError", "URI malformed")
			}
			for k := i; k < i+w; k++ {
				esc(str[k])
			}
			i += w
		}
		return jsval.Str(b.String())
	})

	// regexp(pattern, flags) is RegExp(pattern, flags): a RegExp argument
	// without flags comes back as the same object.
	fn("regexp", func(s *Scope, args []jsval.Value) jsval.Value {
		pat, fl := arg(args, 0), arg(args, 1)
		if pat.IsPattern() && fl.IsUndefined() {
			return pat
		}
		flags := ""
		if !fl.IsUndefined() {
			flags = s.str(fl)
		}
		if pat.IsPattern() {
			return jsval.PatternValue(s.compileEscaped(pat.PatternOf().Source, canonicalFlags(flags)))
		}
		source := ""
		if !pat.IsUndefined() {
			source = s.str(pat)
		}
		return jsval.PatternValue(s.compilePattern(source, flags))
	})
	fnMin("test", 1, func(s *Scope, args []jsval.Value) jsval.Value {
		p := s.toPattern(args[0])
		return jsval.Bool(s.patternTest(p, s.str(arg(args, 1))))
	})

	fn("replace", func(s *Scope, args []jsval.Value) jsval.Value {
		str, pat, repl := arg(args, 0), arg(args, 1), s.str(arg(args, 2))
		if !pat.IsStr() && !pat.IsPattern() {
			throw("Error", "Please pass a string or RegExp argument to replace.")
		}
		src := s.str(str)
		if pat.IsPattern() {
			return jsval.Str(s.regexpReplace(pat.PatternOf(), src, repl))
		}
		return jsval.Str(s.stringReplace(src, pat.StrValue(), repl))
	})

	fn("pad", func(s *Scope, args []jsval.Value) jsval.Value {
		str := s.str(arg(args, 0)) // `str + ''`
		s.workBytes(len(str))
		length := s.num(arg(args, 1))
		c := " "
		if pc := arg(args, 2); pc.IsTruthy() {
			c = s.str(pc)
		}
		n := length - float64(utf16Len(str))
		if n <= 0 {
			return jsval.Str(str)
		}
		align := arg(args, 3)
		switch {
		case align.IsStr() && align.StrValue() == "left":
			return jsval.Str(s.padded(c, n, str, 0))
		case align.IsStr() && align.StrValue() == "center":
			return jsval.Str(s.padded(c, float64(toInt32(n/2)), str, math.Ceil(n/2)))
		}
		return jsval.Str(s.padded(c, 0, str, n))
	})
	fn("truncate", func(s *Scope, args []jsval.Value) jsval.Value {
		str := s.str(arg(args, 0))
		s.workBytes(len(str))
		length := s.num(arg(args, 1))
		e := jsval.Str("\u2026")
		if ev := arg(args, 3); !ev.IsNullish() {
			e = ev
		}
		// `e.length` is read as a property, so a non-string ellipsis has a
		// length of undefined (NaN in the arithmetic) unless it is an array.
		elen := s.num(s.getProp(e, "length"))
		n := float64(utf16Len(str))
		l := jsMax2(0, length-elen)
		if n <= length {
			return jsval.Str(str)
		}
		es := s.str(e)
		align := arg(args, 2)
		switch {
		case align.IsStr() && align.StrValue() == "left":
			return jsval.Str(s.concat(es, s.sliceUnits(str, n-l, 0, false)))
		case align.IsStr() && align.StrValue() == "center":
			return jsval.Str(s.concat(s.concat(s.sliceUnits(str, 0, math.Ceil(l/2), true), es), s.sliceUnits(str, n-float64(toInt32(l/2)), 0, false)))
		}
		return jsval.Str(s.concat(s.sliceUnits(str, 0, l, true), es))
	})
}

// stringSplit is String.prototype.split with a string separator, at most
// limit pieces, in time linear in the string.
func (s *Scope) stringSplit(str, sep string, limit uint32) []jsval.Value {
	switch {
	case sep == "":
		return s.unitSplit(str, limit)
	case unitSearch(sep):
		// The separator can match half of a pair: split the code units.
		u, v := s.toUnits(str), s.toUnits(sep)
		b := itemBuilder{s: s}
		pos := 0
		for uint64(len(b.items)) < uint64(limit) {
			i := searchFirst(s, u[pos:], v)
			if i < 0 {
				break
			}
			piece := u[pos : pos+i]
			s.chargeBytes(3 * len(piece))
			b.push(jsval.Str(fromUTF16(piece)))
			pos += i + len(v)
		}
		if uint64(len(b.items)) < uint64(limit) {
			s.chargeBytes(3 * (len(u) - pos))
			b.push(jsval.Str(fromUTF16(u[pos:])))
		}
		return b.items
	}
	b := itemBuilder{s: s}
	pos := 0
	for uint64(len(b.items)) < uint64(limit) {
		i := s.indexBytes(str, sep, pos)
		if i < 0 {
			b.push(jsval.Str(str[pos:]))
			break
		}
		b.push(jsval.Str(str[pos:i]))
		pos = i + len(sep)
	}
	return b.items
}

// stringReplace is String.prototype.replace with a string pattern: its first
// occurrence replaced by the expansion of repl, where $$, $&, $` and $' are
// the only patterns (there are no groups).
func (s *Scope) stringReplace(src, needle, repl string) string {
	i := s.indexOf(src, needle, 0)
	if i < 0 {
		return src
	}
	before := s.substr(src, 0, i)
	n := utf16Len(needle)
	after := s.substr(src, i+n, utf16Len(src))
	b := newStrBuilder(s)
	b.add(before)
	expandTemplate(&b, repl, stringMatch{before, needle, after}, nil)
	b.add(after)
	return b.String()
}
