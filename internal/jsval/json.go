package jsval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"github.com/mgilbir/aster/internal/budget"
)

// MaxJSONDepth bounds how deeply a parsed document may nest. Real charts nest
// about a dozen levels; the bound keeps hostile input from driving unbounded
// recursion in every consumer that walks the result.
const MaxJSONDepth = 256

// MaxValueDepth bounds how deeply the functions that walk a Value recursively
// (Equal, AsString, AppendJSON) descend. A parsed document is already limited
// to MaxJSONDepth, but a value built at run time (a formula wrapping its own
// field in an array, once per transform) can nest as deep as the
// specification is long. V8 throws a RangeError at about 10000 frames; here a
// walk that reaches the bound stops (Equal reports false, AsString and
// AppendJSON write nothing for the too-deep value) instead of recursing until
// the goroutine stack overflows, which Go cannot recover from. Each frame is
// under 200 bytes, so the bound costs a few hundred KB of stack.
const MaxValueDepth = 1024

// ErrJSONDepth is returned (wrapped) when a document nests past MaxJSONDepth.
var ErrJSONDepth = fmt.Errorf("json: document nests too deeply: %w", budget.ErrLimit)

// SyntaxError reports malformed JSON and where it was found.
type SyntaxError struct {
	Offset int
	Msg    string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("json: %s at offset %d", e.Msg, e.Offset)
}

// ErrJSONSize is returned (wrapped) when a document would take more memory
// than the limit given to ParseJSONLimit.
var ErrJSONSize = fmt.Errorf("json: document is too large to hold in memory: %w", budget.ErrLimit)

// What a parsed value holds, as ParseJSONLimit counts it: a Value in an array
// and a member of an object (its key's string header and its Value), each
// twice over for the slack of a slice that grows by doubling; the Object and
// the box of an array. Strings count their bytes, keys too (a key shared
// between rows is charged each time, which keeps the count independent of
// what the table happens to hold). They are sizes of the types, so they
// follow a change of representation (they once assumed a 48-byte Value).
const (
	costElem   = 2 * int64(unsafe.Sizeof(Value{}))
	costMember = 2 * (int64(unsafe.Sizeof("")) + int64(unsafe.Sizeof(Value{})))
	costObject = int64(unsafe.Sizeof(Object{}))
	costArray  = int64(unsafe.Sizeof(arrayBox{}))
)

// ParseJSON parses text with JSON.parse semantics: strict RFC 8259 grammar,
// object keys in JavaScript's order (array-index keys first, ascending, then
// the rest in document order; see Object), a repeated key overwriting the
// value at its first position. A lone surrogate escape ("\ud83d") is kept as
// the code unit it is, in WTF-8, as JSON.parse keeps it and as the
// expression evaluator holds one; a byte of invalid UTF-8 in the text reads
// as U+FFFD, as decoding the text does.
func ParseJSON(text []byte) (Value, error) { return ParseJSONLimit(text, 0) }

// ParseJSONLimit is ParseJSON that stops with ErrJSONSize once the values built
// would take more than max bytes of memory, estimated per element, member and
// string as each is produced: a document of a few bytes per value costs tens of
// times that on the heap, so the size of the text does not bound the parse.
// max <= 0 is unlimited.
func ParseJSONLimit(text []byte, max int64) (Value, error) {
	return parseJSON(nil, 0, text, max)
}

// ParseJSONContext is ParseJSONLimit for a document an untrusted input
// supplies: it charges its work to the budget.Ledger in ctx (a step per
// jsonBytesPerStep bytes), polling ctx as it goes (ADR 0010), and returns
// ctx's error, not a syntax error, once ctx is done. A document is as large
// as the specification or a data file, tens of megabytes, and its parse in
// one piece was the longest stretch of a call without a poll. The polls fall
// between values and inside a long string, run of white space or number
// literal, so no token holds one off.
func ParseJSONContext(ctx context.Context, text []byte, max int64) (Value, error) {
	if ctx == nil {
		return parseJSON(nil, 0, text, max)
	}
	m := budget.NewMeter(ctx)
	return parseJSON(&m, jsonPollBytes, text, max)
}

// jsonPollBytes is how many bytes of a metered parse pass between two
// charges of its work, and jsonBytesPerStep how many make one step: a byte
// costs a few nanoseconds, so a charge is a few hundred steps and the meter
// polls every few tens of microseconds of parsing.
const (
	jsonPollBytes    = 4 << 10
	jsonBytesPerStep = 16
)

// parseJSON parses text, charging m (nil: no meter) every every bytes.
func parseJSON(m *budget.Meter, every int, text []byte, max int64) (Value, error) {
	p := parser{s: text, left: math.MaxInt64, end: len(text), meter: m, every: every}
	if m != nil {
		p.end = min(len(text), every)
	}
	if max > 0 {
		p.left, p.max = max, max
	}
	if len(text) >= slabMinDoc {
		p.sl = new(slabs)
	}
	p.ws()
	v, err := p.value(0)
	if p.stop != nil {
		return Undefined, p.stop
	}
	if err != nil {
		return Undefined, err
	}
	p.ws()
	if p.stop != nil {
		return Undefined, p.stop
	}
	if p.i != len(p.s) {
		return Undefined, p.errf("unexpected trailing data")
	}
	return v, nil
}

// ParseJSONString is ParseJSON over a string.
func ParseJSONString(text string) (Value, error) { return ParseJSON(readOnlyBytes(text)) }

// ParseJSONStringContext is ParseJSONContext over a string: a JSON document a
// specification holds as a string value (a data set's values), parsed where
// it is, not copied first.
func ParseJSONStringContext(ctx context.Context, text string, max int64) (Value, error) {
	return ParseJSONContext(ctx, readOnlyBytes(text), max)
}

// readOnlyBytes is the bytes of s, shared, not copied. The parser only reads
// its text, and every string it makes of it is a copy, so the parse of a
// string needs no copy of the string, which is as long as the document.
func readOnlyBytes(s string) []byte { return unsafe.Slice(unsafe.StringData(s), len(s)) }

// slabs holds the chunks small containers are carved from, one allocation per
// chunk: a data file is millions of coordinate pairs and rows.
type slabs struct {
	pa1  slab[arrayBox1]
	pa2  slab[arrayBox2]
	pa3  slab[arrayBox3]
	pa4  slab[arrayBox4]
	po1  slab[objectN1]
	po2  slab[objectN2]
	po3  slab[objectN3]
	po4  slab[objectN4]
	po5  slab[objectN5]
	po6  slab[objectN6]
	po7  slab[objectN7]
	po8  slab[objectN8]
	po9  slab[objectN9]
	po10 slab[objectN10]
	po11 slab[objectN11]
	po12 slab[objectN12]
}

// slabMinDoc is the smallest document that uses chunks; a chart specification
// has a few hundred containers and is better served by exact allocations.
const slabMinDoc = 16 << 10

type parser struct {
	s []byte // read, never written: it may be a string's bytes (readOnlyBytes)
	i int

	// left is what the values built so far leave of the memory limit, which
	// was max.
	left, max int64

	// Elements and members of the containers being parsed are collected on
	// these stacks and copied into one exactly sized allocation when the
	// container closes, instead of growing a slice per array.
	vstk []Value
	kstk []string
	// keys interns object keys: rows of a data file repeat the same few names.
	keys map[string]string

	// sl carves small arrays and objects out of chunks; only documents big
	// enough to repay it (data files) get one.
	sl *slabs

	// meter charges a metered parse's work (ParseJSONContext), nil for
	// none, every every bytes. end is where the loops over the text stop
	// to charge it: the end of the text, or for a metered parse the next
	// charge if that is sooner. charged is the offset charged up to; stop
	// is the context's error once the meter reports it.
	meter   *budget.Meter
	every   int
	end     int
	charged int
	stop    error
}

// due reports whether the parse has reached end short of the end of the
// text: a metered parse's next poll.
func (p *parser) due() bool { return p.i >= p.end && p.end < len(p.s) }

// poll charges the bytes read since the last charge to the meter and moves
// end on. Once the call is over it records the context's error in stop and
// truncates the text where the parse is, so whatever is being read ends at
// once (as at the end of the input); the result is then stop, not the error
// that ending gives.
func (p *parser) poll() error {
	if p.stop != nil {
		return p.stop
	}
	err := p.meter.Work(int64(p.i-p.charged) / jsonBytesPerStep)
	p.charged = p.i
	if err != nil {
		p.stop = err
		p.s = p.s[:p.i]
		p.end = p.i
		return err
	}
	p.end = min(len(p.s), p.i+p.every)
	return nil
}

func (p *parser) errf(format string, args ...any) error {
	return &SyntaxError{Offset: p.i, Msg: fmt.Sprintf(format, args...)}
}

// charge takes n bytes from the memory limit and reports whether they were
// there; it is small enough to inline, as the parser calls it for every value.
func (p *parser) charge(n int64) bool {
	p.left -= n
	return p.left >= 0
}

func (p *parser) tooLarge() error {
	return fmt.Errorf("%w (more than %d bytes)", ErrJSONSize, p.max)
}

// ws skips white space. Its test of the next byte is small enough to inline,
// so that compact text (data files) pays no call between tokens.
func (p *parser) ws() {
	if p.i < len(p.s) && jsonSpace[p.s[p.i]] {
		p.wsRun()
	}
}

// wsRun skips a run of white space, polling at end.
func (p *parser) wsRun() {
	for {
		for p.i < p.end {
			if !jsonSpace[p.s[p.i]] {
				return
			}
			p.i++
		}
		if !p.due() || p.poll() != nil {
			return
		}
	}
}

// jsonSpace marks the bytes JSON's grammar reads as white space.
var jsonSpace = [256]bool{' ': true, '\t': true, '\n': true, '\r': true}

func (p *parser) value(depth int) (Value, error) {
	if p.due() {
		if err := p.poll(); err != nil {
			return Undefined, err
		}
	}
	if p.i >= len(p.s) {
		return Undefined, p.errf("unexpected end of input")
	}
	switch c := p.s[p.i]; c {
	case '{':
		if depth >= MaxJSONDepth {
			return Undefined, fmt.Errorf("%w (more than %d levels)", ErrJSONDepth, MaxJSONDepth)
		}
		return p.object(depth + 1)
	case '[':
		if depth >= MaxJSONDepth {
			return Undefined, fmt.Errorf("%w (more than %d levels)", ErrJSONDepth, MaxJSONDepth)
		}
		return p.array(depth + 1)
	case '"':
		s, err := p.str()
		if err != nil {
			return Undefined, err
		}
		if !p.charge(int64(len(s))) {
			return Undefined, p.tooLarge()
		}
		return Str(s), nil
	case 't':
		return p.literal("true", True)
	case 'f':
		return p.literal("false", False)
	case 'n':
		return p.literal("null", Null)
	default:
		if c == '-' || (c >= '0' && c <= '9') {
			return p.number()
		}
		return Undefined, p.unexpected()
	}
}

// unexpected is the error of a character no value starts with: the
// character as the text holds it, or its first byte, in hex, when the text
// is not valid UTF-8 there (a byte is not a character of its own).
func (p *parser) unexpected() error {
	r, n := utf8.DecodeRune(p.s[p.i:])
	if r == utf8.RuneError && n <= 1 {
		return p.errf("unexpected byte 0x%02X", p.s[p.i])
	}
	return p.errf("unexpected character %q", r)
}

func (p *parser) literal(word string, v Value) (Value, error) {
	if len(p.s)-p.i < len(word) || string(p.s[p.i:p.i+len(word)]) != word {
		return Undefined, p.errf("invalid literal")
	}
	p.i += len(word)
	return v, nil
}

func (p *parser) number() (Value, error) {
	start := p.i
	if p.s[p.i] == '-' {
		p.i++
	}
	if p.i >= len(p.s) {
		return Undefined, p.errf("invalid number")
	}
	if p.s[p.i] == '0' {
		p.i++
	} else if p.s[p.i] >= '1' && p.s[p.i] <= '9' {
		p.i++
		p.digits()
	} else {
		return Undefined, p.errf("invalid number")
	}
	if p.i < len(p.s) && p.s[p.i] == '.' {
		p.i++
		if p.digits() == 0 {
			return Undefined, p.errf("invalid number")
		}
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		if p.digits() == 0 {
			return Undefined, p.errf("invalid number")
		}
	}
	if p.stop != nil {
		return Undefined, p.stop
	}
	lit := p.s[start:p.i]
	if f, ok := fastNumber(lit); ok {
		return Num(f), nil
	}
	if len(lit) > numberDigits {
		short, err := p.shortNumber(lit)
		if err != nil {
			return Undefined, err
		}
		lit = short
	}
	f, err := strconv.ParseFloat(string(lit), 64)
	if err != nil {
		var ne *strconv.NumError
		if !errors.As(err, &ne) || ne.Err != strconv.ErrRange {
			return Undefined, p.errf("invalid number")
		}
		// JSON.parse rounds out-of-range literals to ±Infinity or 0.
	}
	return Num(f), nil
}

// digits skips a run of decimal digits, polling at end, and reports how
// many it skipped.
func (p *parser) digits() int {
	start := p.i
	for {
		for p.i < p.end && p.s[p.i]-'0' < 10 {
			p.i++
		}
		if p.i < p.end || !p.due() || p.poll() != nil {
			return p.i - start
		}
	}
}

// numberDigits is how many significant digits of a number decide which
// float64 it rounds to, with whether any digit after them is not zero. A
// boundary between two roundings (a float64, or the midpoint of two
// neighbours, the overflow and underflow thresholds among them) has at most
// 768 significant digits (a midpoint of subnormals, an odd multiple of
// 2^-1075, has 767 and the largest normal ones fewer), so it cannot lie
// strictly between a literal's first numberDigits digits t and t plus a unit
// in their last place: a literal cut to those digits, with a digit 1 after
// them if a digit it drops is not zero, lies strictly inside the same
// interval as the literal (or is the literal, when it drops only zeros), and
// rounds to the same float64. strconv's own slow path keeps 800 digits for
// the same reason, but it misplaces the decimal point of a literal with more
// than 800 digits before it (it takes the point to follow the 800th), which
// the cut form, 0.ddd…, never has: Go 1.27's ParseFloat reads a 1,783-digit
// integer times 1e-2100, a little over 1.5 times the smallest subnormal, as
// 0, where JSON.parse reads 1e-323.
//
// ParseFloat reads every digit it is given, at once, so a literal longer
// than numberDigits bytes is cut before it is converted, as it is read and
// polled (shortNumber); a shorter one has too few digits to be misread.
const numberDigits = 800

// shortNumber is shortDecimal for the parser: its pass over a long literal
// charges the bytes it reads as the scan does, and stops with the context's
// error.
func (p *parser) shortNumber(lit []byte) ([]byte, error) {
	if p.meter == nil {
		return shortDecimal(lit, len(lit), nil)
	}
	return shortDecimal(lit, p.every, func() error {
		if err := p.meter.Work(int64(p.every) / jsonBytesPerStep); err != nil {
			p.stop = err
			return err
		}
		return nil
	})
}

// shortDecimal is the validated decimal literal lit cut to its first
// numberDigits significant digits, a sticky digit and its decimal exponent,
// in the form [-]0.ddd…e±x: the literal ParseFloat rounds to the float64 lit
// rounds to (numberDigits), and at most numberDigits+30 bytes, so that the
// conversion is bounded and the literal is not copied. It reads lit chunk
// bytes at a time, calling charge (when not nil) between two chunks.
func shortDecimal(lit []byte, chunk int, charge func() error) ([]byte, error) {
	out := make([]byte, 0, numberDigits+32)
	i := 0
	switch lit[0] {
	case '-':
		out = append(out, '-')
		i = 1
	case '+':
		i = 1
	}
	out = append(out, '0', '.')
	// The literal is 0.d₁d₂… × 10^point, d₁ its first digit that is not
	// zero; the exponent's digits saturate far past where every literal
	// rounds to 0 or Infinity.
	var point, exp int64
	sig, sticky, frac, inExp, expNeg := 0, false, false, false, false
	for i < len(lit) {
		end := min(len(lit), i+max(chunk, 1))
		for ; i < end; i++ {
			c := lit[i]
			switch {
			case inExp:
				switch c {
				case '-':
					expNeg = true
				case '+':
				default:
					exp = min(exp*10+int64(c-'0'), 1<<40)
				}
			case c == '.':
				frac = true
			case c == 'e' || c == 'E':
				inExp = true
			case sig == 0 && c == '0':
				if frac {
					point--
				}
			default:
				if !frac {
					point++
				}
				if sig < numberDigits {
					out = append(out, c)
				} else if c != '0' {
					sticky = true
				}
				sig++
			}
		}
		if charge != nil && end < len(lit) {
			if err := charge(); err != nil {
				return nil, err
			}
		}
	}
	if sig == 0 {
		return append(out[:len(out)-2], '0'), nil // ±0
	}
	if sticky {
		out = append(out, '1')
	}
	if expNeg {
		exp = -exp
	}
	// 0.1e1000 is past the largest float64 and 0.9e-1000 under half the
	// smallest: clamped there, the exponent rounds as the literal's does.
	point = min(max(point+exp, -1000), 1000)
	out = append(out, 'e')
	return strconv.AppendInt(out, point, 10), nil
}

// ParseDecimal is the float64 a decimal literal denotes, rounded once, as
// JavaScript rounds it: the conversion of JSON.parse, Number(), parseFloat,
// parseInt in radix 10 and an expression's numeric literal alike. lit is
// [+-]digits[.digits][(e|E)[+-]digits], where either side of the point may be
// empty but not both; ok is false for anything else. A literal past
// numberDigits bytes is cut to the digits that decide its rounding first
// (shortDecimal): strconv.ParseFloat alone misreads one of more than 800
// digits.
func ParseDecimal(lit string) (f float64, ok bool) {
	body := lit
	if body != "" && (body[0] == '+' || body[0] == '-') {
		body = body[1:]
	}
	if !isDecimalLiteral(body) {
		return math.NaN(), false
	}
	b := readOnlyBytes(lit)
	if lit[0] != '+' {
		if f, ok := fastNumber(b); ok {
			return f, true
		}
	}
	if len(lit) > numberDigits {
		b, _ = shortDecimal(b, len(b), nil)
	}
	f, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		// Out-of-range literals round to ±Infinity or 0, which ParseFloat
		// reports alongside ErrRange.
		var ne *strconv.NumError
		if !errors.As(err, &ne) || ne.Err != strconv.ErrRange {
			return math.NaN(), false
		}
	}
	return f, true
}

var pow10tab = [...]float64{1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11, 1e12, 1e13, 1e14, 1e15}

// fastNumber converts a validated JSON number of at most 15 significant digits
// and no exponent exactly: the digits are an integer below 2^53 and a power of
// ten up to 1e15 is exact, so one division rounds correctly (Clinger's fast
// path), the same result ParseFloat gives. ok is false for anything else.
func fastNumber(b []byte) (float64, bool) {
	i := 0
	neg := false
	if b[0] == '-' {
		neg = true
		i = 1
	}
	var m uint64
	digits, frac := 0, -1
	for ; i < len(b); i++ {
		c := b[i]
		switch {
		case c == '.':
			frac = 0
		case c >= '0' && c <= '9':
			m = m*10 + uint64(c-'0')
			if m != 0 {
				digits++
			}
			if frac >= 0 {
				frac++
			}
		default: // exponent
			return 0, false
		}
		if digits > 15 || frac > 15 {
			return 0, false
		}
	}
	f := float64(m)
	if frac > 0 {
		f /= pow10tab[frac]
	}
	if neg {
		f = -f
	}
	return f, true
}

func (p *parser) str() (string, error) {
	p.i++ // opening quote
	start := p.i
	// Fast path: no escapes, valid UTF-8, no control characters.
fast:
	for {
		for p.i < p.end {
			c := p.s[p.i]
			if c == '"' {
				raw := p.s[start:p.i]
				p.i++
				if utf8.Valid(raw) {
					return string(raw), nil
				}
				return string(AppendDecoded(nil, raw)), nil
			}
			if c == '\\' || c < 0x20 {
				break fast
			}
			p.i++
		}
		if !p.due() {
			break
		}
		if err := p.poll(); err != nil {
			return "", err
		}
	}
	buf := appendDecoded(nil, p.s[start:p.i])
	for p.i < len(p.s) {
		if p.due() {
			if err := p.poll(); err != nil {
				return "", err
			}
			continue // the text may now end here
		}
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			return string(buf), nil
		case c < 0x20:
			return "", p.errf("control character in string")
		case c == '\\':
			p.i++
			if p.i >= len(p.s) {
				return "", p.errf("unterminated escape")
			}
			e := p.s[p.i]
			p.i++
			switch e {
			case '"', '\\', '/':
				buf = append(buf, e)
			case 'b':
				buf = append(buf, '\b')
			case 'f':
				buf = append(buf, '\f')
			case 'n':
				buf = append(buf, '\n')
			case 'r':
				buf = append(buf, '\r')
			case 't':
				buf = append(buf, '\t')
			case 'u':
				r, err := p.hex4()
				if err != nil {
					return "", err
				}
				if utf16.IsSurrogate(r) {
					if r < 0xDC00 && p.i+6 <= len(p.s) && p.s[p.i] == '\\' && p.s[p.i+1] == 'u' {
						save := p.i
						p.i += 2
						r2, err := p.hex4()
						if err == nil && r2 >= 0xDC00 && r2 <= 0xDFFF {
							buf = utf8.AppendRune(buf, utf16.DecodeRune(r, r2))
							continue
						}
						p.i = save
					}
					// A surrogate with no partner is a code unit of its
					// own, written as WTF-8.
					buf = AppendSurrogate(buf, r)
					continue
				}
				buf = utf8.AppendRune(buf, r)
			default:
				return "", p.errf("invalid escape \\%c", e)
			}
		case c < utf8.RuneSelf:
			buf = append(buf, c)
			p.i++
		default:
			// A character of the text: invalid UTF-8 reads as U+FFFD, one
			// for each maximal subpart as the WHATWG decoder reads it, so
			// the bytes of a surrogate the text holds raw are not taken for
			// the code unit an escape makes.
			r, w := DecodeRune(p.s[p.i:])
			if r == utf8.RuneError {
				buf = append(buf, "\uFFFD"...)
			} else {
				buf = append(buf, p.s[p.i:p.i+w]...)
			}
			p.i += w
		}
	}
	return "", p.errf("unterminated string")
}

// appendDecoded appends text to b as the WHATWG decoder reads it
// (AppendDecoded).
func appendDecoded(b, text []byte) []byte {
	return AppendDecoded(b, text)
}

func (p *parser) hex4() (rune, error) {
	if p.i+4 > len(p.s) {
		return 0, p.errf("invalid unicode escape")
	}
	var r rune
	for _, c := range p.s[p.i : p.i+4] {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			r |= rune(c-'A') + 10
		default:
			return 0, p.errf("invalid unicode escape")
		}
	}
	p.i += 4
	return r, nil
}

func (p *parser) object(depth int) (Value, error) {
	p.i++ // {
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		if !p.charge(costObject) {
			return Undefined, p.tooLarge()
		}
		return Obj(NewObject(0)), nil
	}
	base := len(p.kstk)
	for {
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			return Undefined, p.errf("expected object key")
		}
		k, err := p.key()
		if err != nil {
			return Undefined, err
		}
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return Undefined, p.errf("expected ':'")
		}
		p.i++
		p.ws()
		v, err := p.value(depth)
		if err != nil {
			return Undefined, err
		}
		if !p.charge(costMember + int64(len(k))) {
			return Undefined, p.tooLarge()
		}
		p.kstk = append(p.kstk, k)
		p.vstk = append(p.vstk, v)
		p.ws()
		if p.i >= len(p.s) {
			return Undefined, p.errf("unterminated object")
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			n := len(p.kstk) - base
			if !p.charge(costObject) {
				return Undefined, p.tooLarge()
			}
			o := p.newObject(n)
			vbase := len(p.vstk) - n
			for i := 0; i < n; i++ {
				o.setUnordered(p.kstk[base+i], p.vstk[vbase+i])
			}
			o.order()
			clear(p.vstk[vbase:])
			p.kstk = p.kstk[:base]
			p.vstk = p.vstk[:vbase]
			return Obj(o), nil
		default:
			return Undefined, p.errf("expected ',' or '}'")
		}
	}
}

// maxInternKey is the longest key the parser shares between occurrences.
const maxInternKey = 1 << 10

// key reads an object key, sharing the string between occurrences.
func (p *parser) key() (string, error) {
	if p.sl == nil { // a small document repeats few keys; skip the table
		return p.str()
	}
	// A key past maxInternKey bytes is read (and polled within) as any
	// string is, and not shared.
	start := p.i + 1
	for j, end := start, min(len(p.s), start+maxInternKey); j < end; j++ {
		c := p.s[j]
		if c == '"' {
			raw := p.s[start:j]
			if !utf8.Valid(raw) {
				break
			}
			p.i = j + 1
			if k, ok := p.keys[string(raw)]; ok {
				return k, nil
			}
			k := string(raw)
			if p.keys == nil {
				p.keys = make(map[string]string)
			}
			if len(p.keys) < 256 {
				p.keys[k] = k
			}
			return k, nil
		}
		if c == '\\' || c < 0x20 {
			break
		}
	}
	return p.str()
}

func (p *parser) array(depth int) (Value, error) {
	p.i++ // [
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return Arr(nil), nil
	}
	base := len(p.vstk)
	for {
		p.ws()
		v, err := p.value(depth)
		if err != nil {
			return Undefined, err
		}
		if !p.charge(costElem) {
			return Undefined, p.tooLarge()
		}
		p.vstk = append(p.vstk, v)
		p.ws()
		if p.i >= len(p.s) {
			return Undefined, p.errf("unterminated array")
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			if !p.charge(costArray) {
				return Undefined, p.tooLarge()
			}
			arr, items := p.makeArr(len(p.vstk) - base)
			copy(items, p.vstk[base:])
			clear(p.vstk[base:])
			p.vstk = p.vstk[:base]
			return arr, nil
		default:
			return Undefined, p.errf("expected ',' or ']'")
		}
	}
}

// AppendJSON appends JSON.stringify(v) (compact) to dst. Undefined object
// members are omitted and undefined array elements written as null; non-finite
// numbers are null; a Timestamp is written as its toJSON, the ISO string
// (null for an Invalid Date), and a Pattern as {} (it has no enumerable
// properties); a lone surrogate is escaped, as
// JSON.stringify escapes it. It is a detached walk (see Walker): a value an
// expression or a transform built is written with the call's Walker.
func AppendJSON(dst []byte, v Value) []byte { return Walker{}.AppendJSON(dst, v) }

// AppendJSONIndent is JSON.stringify(v, null, indent); see AppendJSON.
func AppendJSONIndent(dst []byte, v Value, indent string) []byte {
	return Walker{}.AppendJSONIndent(dst, v, indent)
}

// MarshalJSON writes compact JSON.stringify output.
func (v Value) MarshalJSON() ([]byte, error) {
	if v.Kind() == KindUndefined {
		return []byte("null"), nil
	}
	return AppendJSON(nil, v), nil
}

func quoteJSON(s string) string { return string(appendQuoted(nil, s)) }

const hexDigits = "0123456789abcdef"

// appendQuoted writes s as JSON.stringify does: only ", \ and control
// characters are escaped (never <, > or &, unlike encoding/json), and a lone
// surrogate, which a string an expression made holds as three WTF-8 bytes
// (ED A0..BF xx), as its \uXXXX escape (JSON.stringify is well formed since
// ES2019). A high surrogate's bytes followed by a low one's are the
// character they pair into.
func appendQuoted(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' && c != 0xED {
			continue
		}
		if c == 0xED {
			u, ok := surrogateAt(s, i)
			if !ok {
				continue // U+D000..U+D7FF
			}
			dst = append(dst, s[start:i]...)
			if lo, ok := surrogateAt(s, i+3); ok && u < 0xDC00 && lo >= 0xDC00 {
				dst = utf8.AppendRune(dst, utf16.DecodeRune(u, lo))
				i += 5
			} else {
				dst = append(dst, '\\', 'u', hexDigits[u>>12], hexDigits[u>>8&0xF], hexDigits[u>>4&0xF], hexDigits[u&0xF])
				i += 2
			}
			start = i + 1
			continue
		}
		dst = append(dst, s[start:i]...)
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		default:
			dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
		}
		start = i + 1
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

// quotedLen is the length appendQuoted gives s.
func quotedLen(s string) int {
	n := len(s) + 2
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == 0xED:
			u, ok := surrogateAt(s, i)
			if !ok {
				continue
			}
			if lo, ok := surrogateAt(s, i+3); ok && u < 0xDC00 && lo >= 0xDC00 {
				n -= 2 // six bytes become four
				i += 5
			} else {
				n += 3 // three bytes become six
				i += 2
			}
		case c == '"' || c == '\\' || c == '\n' || c == '\r' || c == '\t' || c == '\b' || c == '\f':
			n++
		case c < 0x20:
			n += 5
		}
	}
	return n
}

// surrogateAt is the surrogate whose WTF-8 bytes start s[i:], if they do.
func surrogateAt(s string, i int) (rune, bool) {
	if i+2 < len(s) && s[i] == 0xED && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2]&0xC0 == 0x80 {
		return 0xD000 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F), true
	}
	return 0, false
}

// slab hands out elements from chunks, one allocation per chunk.
type slab[T any] struct {
	rest []T
	next int
}

// slabNext takes the next element; chunks start small and double up to limit, so
// a small document does not pay for a full chunk of every size.
func slabNext[T any](s *slab[T], limit int) *T {
	if len(s.rest) == 0 {
		s.next = min(limit, max(1, s.next*2))
		s.rest = make([]T, s.next)
	}
	x := &s.rest[0]
	s.rest = s.rest[1:]
	return x
}

// makeArr is MakeArr drawing small arrays from the parser's chunks.
func (p *parser) makeArr(n int) (Value, []Value) {
	if p.sl == nil {
		return MakeArr(n)
	}
	var b *arrayBox
	switch n {
	case 1:
		x := slabNext(&p.sl.pa1, 64)
		x.set(x.buf[:])
		b = &x.arrayBox
	case 2:
		x := slabNext(&p.sl.pa2, 64)
		x.set(x.buf[:])
		b = &x.arrayBox
	case 3:
		x := slabNext(&p.sl.pa3, 32)
		x.set(x.buf[:])
		b = &x.arrayBox
	case 4:
		x := slabNext(&p.sl.pa4, 32)
		x.set(x.buf[:])
		b = &x.arrayBox
	default:
		return MakeArr(n)
	}
	return ref(KindArr, unsafe.Pointer(b)), b.list()
}

// newObject is NewObject drawing small objects from the parser's chunks.
func (p *parser) newObject(n int) *Object {
	if p.sl == nil {
		return NewObject(n)
	}
	switch n {
	case 1:
		x := slabNext(&p.sl.po1, 32)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 2:
		x := slabNext(&p.sl.po2, 16)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 3:
		x := slabNext(&p.sl.po3, 10)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 4:
		x := slabNext(&p.sl.po4, 8)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 5:
		x := slabNext(&p.sl.po5, 6)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 6:
		x := slabNext(&p.sl.po6, 5)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 7:
		x := slabNext(&p.sl.po7, 4)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 8:
		x := slabNext(&p.sl.po8, 4)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 9:
		x := slabNext(&p.sl.po9, 8)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 10:
		x := slabNext(&p.sl.po10, 8)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 11:
		x := slabNext(&p.sl.po11, 8)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	case 12:
		x := slabNext(&p.sl.po12, 8)
		x.keys, x.vals = x.kbuf[:0], x.vbuf[:0]
		return &x.Object
	}
	return NewObject(n)
}

// AppendQuoted appends s as JSON.stringify writes a string (see
// appendQuoted). It is no walk: the text is s's, escaped.
func AppendQuoted(dst []byte, s string) []byte { return appendQuoted(dst, s) }

// plainJSON reports whether s is its own JSON text inside the quotes: no
// byte appendQuoted escapes.
func plainJSON(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == '"' || c == '\\' || c == 0xED {
			return false
		}
	}
	return true
}
