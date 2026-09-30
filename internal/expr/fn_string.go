package expr

import (
	"encoding/base64"
	"math"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/mgilbir/aster/internal/jsval"
)

// MaxStringLength bounds strings that pad, truncate and friends build, in
// UTF-16 code units. Upstream would loop (or run out of memory) on
// pad(x, 1e12).
const MaxStringLength = 1 << 24

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
func sliceUnits(str string, start float64, end float64, hasEnd bool) string {
	n := utf16Len(str)
	from := sliceIndex(start, n)
	to := n
	if hasEnd {
		to = sliceIndex(end, n)
	}
	if to <= from {
		return ""
	}
	return substrUnits(str, from, to)
}

func repeatString(s *Scope, c string, reps float64) string {
	if math.IsNaN(reps) || reps < 1 || c == "" {
		return ""
	}
	count := math.Floor(reps)
	if count*float64(utf16Len(c)) > MaxStringLength {
		throw("RangeError", "Invalid string length")
	}
	s.checkLen(len(c) * int(count))
	s.tick()
	return strings.Repeat(c, int(count))
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

// patternCache memoises patterns compiled from strings (test('a+', x) in a
// filter would otherwise compile per datum). Patterns with g or y flags carry
// state and are never shared.
var patternCache = struct {
	sync.Mutex
	m map[string]*jsval.Pattern
}{m: map[string]*jsval.Pattern{}}

func compilePattern(source, flags string) *jsval.Pattern {
	shareable := !strings.ContainsAny(flags, "gy")
	key := flags + "/" + source
	if shareable {
		patternCache.Lock()
		p := patternCache.m[key]
		patternCache.Unlock()
		if p != nil {
			return p
		}
	}
	p, err := jsval.NewPattern(escapeSource(source), canonicalFlags(flags))
	if err != nil {
		throw("SyntaxError", "%v", err)
	}
	if shareable {
		patternCache.Lock()
		if len(patternCache.m) >= 512 {
			clear(patternCache.m)
		}
		patternCache.m[key] = p
		patternCache.Unlock()
	}
	return p
}

// escapeSource is EscapeRegExpPattern: what RegExp.prototype.source reports for
// a pattern built from a string ("/" and line terminators escaped, empty
// pattern "(?:)").
func escapeSource(src string) string {
	if src == "" {
		return "(?:)"
	}
	if !strings.ContainsAny(src, "/\n\r\u2028\u2029") {
		return src
	}
	var b strings.Builder
	inClass := false
	for i := 0; i < len(src); {
		r, w := utf8.DecodeRuneInString(src[i:])
		switch {
		case r == '\\' && i+w < len(src):
			b.WriteString(src[i : i+w])
			i += w
			r2, w2 := utf8.DecodeRuneInString(src[i:])
			switch r2 {
			case '\n':
				b.WriteString("n")
			case '\r':
				b.WriteString("r")
			case 0x2028:
				b.WriteString("u2028")
			case 0x2029:
				b.WriteString("u2029")
			default:
				b.WriteString(src[i : i+w2])
			}
			i += w2
			continue
		case r == '/' && !inClass:
			b.WriteString("\\/")
		case r == '[':
			inClass = true
			b.WriteRune(r)
		case r == ']':
			inClass = false
			b.WriteRune(r)
		case r == '\n':
			b.WriteString("\\n")
		case r == '\r':
			b.WriteString("\\r")
		case r == 0x2028:
			b.WriteString("\\u2028")
		case r == 0x2029:
			b.WriteString("\\u2029")
		default:
			b.WriteRune(r)
		}
		i += w
	}
	return b.String()
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

// toPattern is RegExp(v) with no flags.
func (s *Scope) toPattern(v jsval.Value) *jsval.Pattern {
	if v.IsPattern() {
		return v.PatternOf()
	}
	if v.IsUndefined() {
		return compilePattern("", "")
	}
	return compilePattern(s.str(v), "")
}

func (s *Scope) patternTest(p *jsval.Pattern, str string) bool {
	if strings.ContainsAny(p.Flags, "gy") {
		return p.Re.MatchString(str) // stateful lastIndex, as RegExp.prototype.test
	}
	ok, err := p.Re.MatchStringErr(str)
	if err != nil {
		throw("RangeError", "regular expression too complex: %v", err)
	}
	return ok
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
		return jsval.Num(jsParseFloat(s.str(arg(args, 0))))
	})
	fn("parseInt", func(s *Scope, args []jsval.Value) jsval.Value {
		return jsval.Num(jsParseInt(s.str(arg(args, 0)), toInt32(s.num(arg(args, 1)))))
	})
	fnMin("upper", 1, func(s *Scope, args []jsval.Value) jsval.Value { return jsval.Str(jsUpper(s.str(args[0]))) })
	fnMin("lower", 1, func(s *Scope, args []jsval.Value) jsval.Value { return jsval.Str(jsLower(s.str(args[0]))) })
	fnMin("trim", 1, func(s *Scope, args []jsval.Value) jsval.Value { return jsval.Str(trimJSSpace(s.str(args[0]))) })
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
		return jsval.Str(substrUnits(str, a, b))
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
		var parts []string
		switch {
		case sep.IsUndefined():
			parts = []string{str}
		case sep.IsPattern():
			items := s.regexpSplit(sep.PatternOf(), str)
			if uint64(len(items)) > uint64(limit) {
				items = items[:limit]
			}
			return jsval.Arr(items)
		default:
			sp := s.str(sep)
			switch {
			case sp == "":
				if isASCII(str) {
					parts = make([]string, len(str))
					for i := range str {
						parts[i] = str[i : i+1]
					}
				} else {
					u := toUTF16(str)
					parts = make([]string, len(u))
					for i := range u {
						parts[i] = fromUTF16(u[i : i+1])
					}
				}
			default:
				parts = strings.Split(str, sp)
			}
		}
		if uint64(len(parts)) > uint64(limit) {
			parts = parts[:limit]
		}
		items := make([]jsval.Value, len(parts))
		for i, p := range parts {
			items[i] = jsval.Str(p)
		}
		return jsval.Arr(items)
	})
	fn("btoa", func(s *Scope, args []jsval.Value) jsval.Value {
		str := s.str(arg(args, 0))
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
		str := strings.Map(func(r rune) rune {
			switch r {
			case ' ', '\t', '\n', '\f', '\r':
				return -1
			}
			return r
		}, s.str(arg(args, 0)))
		if len(str)%4 == 0 {
			str = strings.TrimSuffix(strings.TrimSuffix(str, "=="), "=")
		}
		b, err := base64.RawStdEncoding.DecodeString(str)
		if err != nil {
			throw("Error", "The string to be decoded is not correctly encoded.")
		}
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		return jsval.Str(string(r))
	})
	fn("encodeURIComponent", func(s *Scope, args []jsval.Value) jsval.Value {
		str := s.str(arg(args, 0))
		var b strings.Builder
		const hexd = "0123456789ABCDEF"
		for i := 0; i < len(str); {
			c := str[i]
			if c < utf8.RuneSelf {
				if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0 {
					b.WriteByte(c)
				} else {
					b.WriteByte('%')
					b.WriteByte(hexd[c>>4])
					b.WriteByte(hexd[c&15])
				}
				i++
				continue
			}
			r, w := utf8.DecodeRuneInString(str[i:])
			if r == utf8.RuneError && w <= 1 || c == 0xED && i+1 < len(str) && str[i+1] >= 0xA0 {
				throw("URIError", "URI malformed")
			}
			for _, x := range []byte(str[i : i+w]) {
				b.WriteByte('%')
				b.WriteByte(hexd[x>>4])
				b.WriteByte(hexd[x&15])
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
		source := ""
		switch {
		case pat.IsPattern():
			source = pat.PatternOf().Source
		case !pat.IsUndefined():
			source = s.str(pat)
		}
		return jsval.PatternValue(compilePattern(source, flags))
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
			// A regexp can match at every position, so the result can be
			// (len(src)+1)*len(repl) long; refuse before it is built.
			if len(repl) > 1024 && (len(src)+1)*len(repl) > 4*MaxStringLength {
				throw("RangeError", "Invalid string length")
			}
			out, err := pat.PatternOf().Re.ReplaceAllStringErr(src, repl)
			if err != nil {
				throw("RangeError", "%v", err)
			}
			s.checkLen(len(out))
			return jsval.Str(out)
		}
		needle := pat.StrValue()
		i := strings.Index(src, needle)
		if i < 0 {
			return jsval.Str(src)
		}
		rep := expandReplacement(repl, src, i, i+len(needle))
		s.checkLen(len(src) + len(rep))
		return jsval.Str(src[:i] + rep + src[i+len(needle):])
	})

	fn("pad", func(s *Scope, args []jsval.Value) jsval.Value {
		str := s.str(arg(args, 0)) // `str + ''`
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
			return jsval.Str(repeatString(s, c, n) + str)
		case align.IsStr() && align.StrValue() == "center":
			return jsval.Str(repeatString(s, c, float64(toInt32(n/2))) + str + repeatString(s, c, math.Ceil(n/2)))
		}
		return jsval.Str(str + repeatString(s, c, n))
	})
	fn("truncate", func(s *Scope, args []jsval.Value) jsval.Value {
		str := s.str(arg(args, 0))
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
			return jsval.Str(es + sliceUnits(str, n-l, 0, false))
		case align.IsStr() && align.StrValue() == "center":
			return jsval.Str(sliceUnits(str, 0, math.Ceil(l/2), true) + es + sliceUnits(str, n-float64(toInt32(l/2)), 0, false))
		}
		return jsval.Str(sliceUnits(str, 0, l, true) + es)
	})
}

// regexpSplit is String.prototype.split with a RegExp separator: capture
// groups are spliced into the result (undefined for a group that did not
// participate), as ECMA-262 specifies.
func (s *Scope) regexpSplit(p *jsval.Pattern, str string) []jsval.Value {
	if str == "" {
		if ok, _ := p.Re.MatchStringErr(str); ok {
			return []jsval.Value{}
		}
		return []jsval.Value{jsval.Str("")}
	}
	matches, err := p.Re.FindAllStringSubmatchIndexErr(str, -1)
	if err != nil {
		if p.Source == "(?:)" {
			// Empty separator: split into UTF-16 code units.
			u := toUTF16(str)
			out := make([]jsval.Value, len(u))
			for i := range u {
				out[i] = jsval.Str(fromUTF16(u[i : i+1]))
			}
			return out
		}
		throw("RangeError", "regular expression cannot split this string: %v", err)
	}
	var out []jsval.Value
	pos := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		if start >= len(str) {
			break
		}
		if end == pos {
			continue // an empty match where the current piece began
		}
		out = append(out, jsval.Str(str[pos:start]))
		for g := 2; g+1 < len(m); g += 2 {
			if m[g] < 0 {
				out = append(out, jsval.Undefined)
			} else {
				out = append(out, jsval.Str(str[m[g]:m[g+1]]))
			}
		}
		pos = end
	}
	return append(out, jsval.Str(str[pos:]))
}

// expandReplacement handles the $ patterns of String.prototype.replace for a
// string pattern (no capture groups): $$, $&, $` and $'.
func expandReplacement(repl, src string, start, end int) string {
	if !strings.Contains(repl, "$") {
		return repl
	}
	var b strings.Builder
	for i := 0; i < len(repl); i++ {
		c := repl[i]
		if c != '$' || i+1 >= len(repl) {
			b.WriteByte(c)
			continue
		}
		switch repl[i+1] {
		case '$':
			b.WriteByte('$')
			i++
		case '&':
			b.WriteString(src[start:end])
			i++
		case '`':
			b.WriteString(src[:start])
			i++
		case '\'':
			b.WriteString(src[end:])
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
