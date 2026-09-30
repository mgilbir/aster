package format

import (
	"math"
	"unicode"
	"unicode/utf8"
)

// ParseDate is Date.parse (and new Date(string)) as V8 implements it, with z
// as the local time zone. It returns NaN for a string V8 rejects.
//
// Supported forms:
//   - the ECMAScript ISO 8601 profile in full: YYYY, YYYY-MM, YYYY-MM-DD,
//     the extended ±YYYYYY year, and any of those followed by THH:mm,
//     THH:mm:ss or THH:mm:ss.sss (any number of fraction digits) with an
//     optional Z, ±HH:mm or ±HHmm zone. A date alone is UTC; a date with a
//     time and no zone is local time; 24:00 is accepted.
//   - V8's legacy fallback, which accepts most human-written dates: month
//     names (any word beginning with the three letters, "Jan", "January"),
//     numeric M/D/Y, Y/M/D and D.M.Y orders, ignored weekday names and
//     parenthesised comments, a 12- or 24-hour time with optional seconds,
//     fraction and AM/PM, and zones UT, UTC, GMT, Z, EST/EDT/CST/CDT/MST/MDT/
//     PST/PDT and GMT+hhmm forms. Two-digit years mean 20xx for 00-49 and
//     19xx for 50-99; a missing year is 2001, a missing day 1.
//     Text without a zone is local time.
//
// The implementation follows V8's dateparser (tokeniser, day/time/zone
// composers), so odd inputs such as "1/2/3" or "2000-1-1" resolve as in
// Chrome and Node.
func ParseDate(s string, z Zone) float64 {
	p := dateParser{sc: newDateScanner(s)}
	tok, ok := p.parseES5()
	if !ok {
		return math.NaN()
	}
	hasReadNumber := !p.day.empty()
	sc := p.sc
	for ; tok.kind != tokEnd; tok = sc.next() {
		switch {
		case tok.kind == tokNumber:
			hasReadNumber = true
			n := tok.num
			switch {
			case sc.skipSymbol(':'):
				if sc.skipSymbol(':') {
					// n + "::"
					if !p.time.empty() {
						return math.NaN()
					}
					p.time.add(n)
					p.time.add(0)
				} else {
					// n + ":"
					if !p.time.add(n) {
						return math.NaN()
					}
					if sc.peek().isSymbol('.') {
						sc.next()
					}
				}
			case sc.skipSymbol('.') && p.time.expecting(n):
				p.time.add(n)
				if sc.peek().kind != tokNumber {
					return math.NaN()
				}
				ms := readMilliseconds(sc.next())
				if ms < 0 {
					return math.NaN()
				}
				p.time.addFinal(ms)
			case p.tz.expecting(n):
				p.tz.minute = n
			case p.time.expecting(n):
				p.time.addFinal(n)
				// Require end, white space, "Z", "+" or "-" immediately after
				// finalizing time.
				pk := sc.peek()
				if pk.kind != tokEnd && pk.kind != tokSpace && !pk.isKeywordZ() && !pk.isSign() {
					return math.NaN()
				}
			default:
				if !p.day.add(n) {
					return math.NaN()
				}
				sc.skipSymbol('-')
			}
		case tok.kind == tokKeyword:
			// A "word": a run of characters at or above 'A'.
			switch {
			case tok.kw == kwAMPM && !p.time.empty():
				p.time.hourOffset = tok.kwVal
			case tok.kw == kwMonth:
				p.day.named = tok.kwVal
				sc.skipSymbol('-')
			case tok.kw == kwZone && hasReadNumber:
				p.tz.set(tok.kwVal)
			default:
				// Garbage words are illegal if a number has been read.
				if hasReadNumber {
					return math.NaN()
				}
				// The first number has to be separated from garbage words by
				// whitespace or other separators.
				if sc.peek().kind == tokNumber {
					return math.NaN()
				}
			}
		case tok.isSign() && (p.tz.isUTC() || !p.time.empty()):
			// UTC offset (only after UTC or a time).
			p.tz.sign = signOf(tok.sym)
			n, length := 0, 0
			if sc.peek().kind == tokNumber {
				nt := sc.next()
				length, n = nt.length, nt.num
			}
			hasReadNumber = true
			switch {
			case sc.peek().isSymbol(':'):
				p.tz.hour, p.tz.minute = n, kNone
			case length == 2 || length == 1: // GMT-8
				p.tz.hour, p.tz.minute = n, 0
			case length == 4 || length == 3: // hhmm
				p.tz.hour, p.tz.minute = n/100, n%100
			default:
				return math.NaN() // no zones like GMT-12345
			}
		case (tok.isSign() || tok.isSymbol(')')) && hasReadNumber:
			return math.NaN() // a stray sign or ")" after a number
		}
		// Anything else is ignored.
	}

	out, ok := p.day.write()
	if !ok {
		return math.NaN()
	}
	tm, ok := p.time.write()
	if !ok {
		return math.NaN()
	}
	// MakeDay/MakeTime/MakeDate of the composed fields.
	if p.tz.sign != kNone {
		hour, minute := p.tz.hour, p.tz.minute
		if hour == kNone {
			hour = 0
		}
		if minute == kNone {
			minute = 0
		}
		// V8 does this sum in unsigned 32-bit arithmetic and refuses a result
		// above the largest small integer.
		tu := uint32(hour)*3600 + uint32(minute)*60
		if tu > math.MaxInt32 {
			return math.NaN()
		}
		total := int(tu)
		if p.tz.sign < 0 {
			total = -total
		}
		t := UTC.makeLocal(float64(out.year), float64(out.month-1), float64(out.day), float64(tm[0]), float64(tm[1]), float64(tm[2]), float64(tm[3]))
		return timeClip(t - float64(float64(total)*1000))
	}
	t := z.makeLocal(float64(out.year), float64(out.month-1), float64(out.day), float64(tm[0]), float64(tm[1]), float64(tm[2]), float64(tm[3]))
	return timeClip(t)
}

const kNone = math.MinInt32

func signOf(sym rune) int {
	if sym == '-' {
		return -1
	}
	return 1
}

type tokenKind uint8

const (
	tokEnd tokenKind = iota
	tokNumber
	tokSymbol
	tokSpace
	tokKeyword
	tokUnknown
)

type keywordKind uint8

const (
	kwInvalid keywordKind = iota
	kwMonth
	kwAMPM
	kwZone
	kwTimeSep
)

type dateToken struct {
	kind   tokenKind
	num    int // tokNumber: the value of the first 9 significant digits
	length int // tokNumber: digit count; tokKeyword: word length; tokSpace: run length
	sym    rune
	kw     keywordKind
	kwVal  int
}

func (t dateToken) isSymbol(c rune) bool { return t.kind == tokSymbol && t.sym == c }
func (t dateToken) isSign() bool         { return t.kind == tokSymbol && (t.sym == '+' || t.sym == '-') }
func (t dateToken) isKeywordZ() bool     { return t.kind == tokKeyword && t.kw == kwZone && t.length == 1 }
func (t dateToken) isFixed(n int) bool   { return t.kind == tokNumber && t.length == n }

type dateKeyword struct {
	prefix [3]byte
	kind   keywordKind
	val    int
}

var dateKeywords = [...]dateKeyword{
	{[3]byte{'j', 'a', 'n'}, kwMonth, 1}, {[3]byte{'f', 'e', 'b'}, kwMonth, 2},
	{[3]byte{'m', 'a', 'r'}, kwMonth, 3}, {[3]byte{'a', 'p', 'r'}, kwMonth, 4},
	{[3]byte{'m', 'a', 'y'}, kwMonth, 5}, {[3]byte{'j', 'u', 'n'}, kwMonth, 6},
	{[3]byte{'j', 'u', 'l'}, kwMonth, 7}, {[3]byte{'a', 'u', 'g'}, kwMonth, 8},
	{[3]byte{'s', 'e', 'p'}, kwMonth, 9}, {[3]byte{'o', 'c', 't'}, kwMonth, 10},
	{[3]byte{'n', 'o', 'v'}, kwMonth, 11}, {[3]byte{'d', 'e', 'c'}, kwMonth, 12},
	{[3]byte{'a', 'm', 0}, kwAMPM, 0}, {[3]byte{'p', 'm', 0}, kwAMPM, 12},
	{[3]byte{'u', 't', 0}, kwZone, 0}, {[3]byte{'u', 't', 'c'}, kwZone, 0},
	{[3]byte{'z', 0, 0}, kwZone, 0}, {[3]byte{'g', 'm', 't'}, kwZone, 0},
	{[3]byte{'c', 'd', 't'}, kwZone, -5}, {[3]byte{'c', 's', 't'}, kwZone, -6},
	{[3]byte{'e', 'd', 't'}, kwZone, -4}, {[3]byte{'e', 's', 't'}, kwZone, -5},
	{[3]byte{'m', 'd', 't'}, kwZone, -6}, {[3]byte{'m', 's', 't'}, kwZone, -7},
	{[3]byte{'p', 'd', 't'}, kwZone, -7}, {[3]byte{'p', 's', 't'}, kwZone, -8},
	{[3]byte{'t', 0, 0}, kwTimeSep, 0},
}

// lookupKeyword matches a word by its first three lower-cased letters; a
// word longer than three letters can only be a month name.
func lookupKeyword(pre [3]byte, length int) (keywordKind, int) {
	for _, k := range dateKeywords {
		if k.prefix == pre && (length <= 3 || k.kind == kwMonth) {
			return k.kind, k.val
		}
	}
	return kwInvalid, 0
}

type dateScanner struct {
	s     string
	pos   int
	next_ dateToken
}

func newDateScanner(s string) *dateScanner {
	sc := &dateScanner{s: s}
	sc.next_ = sc.scan()
	return sc
}

func (sc *dateScanner) peek() dateToken { return sc.next_ }

func (sc *dateScanner) next() dateToken {
	t := sc.next_
	sc.next_ = sc.scan()
	return t
}

func (sc *dateScanner) skipSymbol(c rune) bool {
	if sc.next_.isSymbol(c) {
		sc.next()
		return true
	}
	return false
}

func isDateSpace(r rune) bool { return unicode.IsSpace(r) || r == 0xFEFF }

func (sc *dateScanner) scan() dateToken {
	s := sc.s
	if sc.pos >= len(s) {
		return dateToken{kind: tokEnd}
	}
	start := sc.pos
	c := s[sc.pos]
	if c >= '0' && c <= '9' {
		// Leading zeros are skipped, then up to nine significant digits count.
		n, sig := 0, 0
		for sc.pos < len(s) && s[sc.pos] == '0' {
			sc.pos++
		}
		for sc.pos < len(s) && s[sc.pos] >= '0' && s[sc.pos] <= '9' {
			if sig < 9 {
				n = n*10 + int(s[sc.pos]-'0')
			}
			sig++
			sc.pos++
		}
		return dateToken{kind: tokNumber, num: n, length: sc.pos - start}
	}
	switch c {
	case ':', '-', '+', '.', ')':
		sc.pos++
		return dateToken{kind: tokSymbol, sym: rune(c)}
	}
	r, w := rune(c), 1
	if c >= utf8.RuneSelf {
		r, w = utf8.DecodeRuneInString(s[sc.pos:])
	}
	if r >= 'A' && !isDateSpace(r) {
		var pre [3]byte
		length := 0
		for sc.pos < len(s) {
			r, w = rune(s[sc.pos]), 1
			if s[sc.pos] >= utf8.RuneSelf {
				r, w = utf8.DecodeRuneInString(s[sc.pos:])
			}
			if r < 'A' || isDateSpace(r) {
				break
			}
			if length < 3 {
				if r >= 'A' && r <= 'Z' {
					r += 'a' - 'A'
				}
				if r < 128 {
					pre[length] = byte(r)
				} else {
					pre[length] = 0xFF
				}
			}
			length++
			sc.pos += w
		}
		kind, val := lookupKeyword(pre, length)
		return dateToken{kind: tokKeyword, kw: kind, kwVal: val, length: length}
	}
	if isDateSpace(r) {
		for sc.pos < len(s) {
			r, w = rune(s[sc.pos]), 1
			if s[sc.pos] >= utf8.RuneSelf {
				r, w = utf8.DecodeRuneInString(s[sc.pos:])
			}
			if !isDateSpace(r) {
				break
			}
			sc.pos += w
		}
		return dateToken{kind: tokSpace, length: sc.pos - start}
	}
	if c == '(' {
		balance := 0
		for {
			switch s[sc.pos] {
			case ')':
				balance--
			case '(':
				balance++
			}
			sc.pos++
			if balance <= 0 || sc.pos >= len(s) {
				break
			}
		}
		return dateToken{kind: tokUnknown}
	}
	sc.pos += w
	return dateToken{kind: tokUnknown}
}

// readMilliseconds keeps the first three significant digits of a fraction.
func readMilliseconds(t dateToken) int {
	number, length := t.num, t.length
	switch {
	case length < 3:
		if length == 1 {
			number *= 100
		} else if length == 2 {
			number *= 10
		}
	case length > 3:
		if length > 9 {
			length = 9
		}
		factor := 1
		for {
			factor *= 10
			length--
			if length <= 3 {
				break
			}
		}
		number /= factor
	}
	return number
}

// ---- composers ----

type dayComposer struct {
	comp  [3]int
	index int
	named int // month from a name (1..12), or kNone
	iso   bool
}

func (d *dayComposer) empty() bool { return d.index == 0 }

func (d *dayComposer) add(n int) bool {
	if d.index < len(d.comp) {
		d.comp[d.index] = n
		d.index++
		return true
	}
	return false
}

type dayResult struct{ year, month, day int }

func between(x, lo, hi int) bool { return x >= lo && x <= hi }
func isDay(x int) bool           { return between(x, 1, 31) }
func isMonth(x int) bool         { return between(x, 1, 12) }

func (d *dayComposer) write() (dayResult, bool) {
	if d.index < 1 {
		return dayResult{}, false
	}
	// Missing components default to 1: "1/2" is January 2 of year 1 (2001).
	for d.index < len(d.comp) {
		d.comp[d.index] = 1
		d.index++
	}
	c := d.comp
	year, month, day := 0, kNone, kNone // the default year 0 becomes 2000
	if d.named == kNone {
		if d.iso || !isDay(c[0]) {
			year, month, day = c[0], c[1], c[2] // YMD
		} else {
			month, day, year = c[0], c[1], c[2] // MD(Y)
		}
	} else {
		month = d.named
		if !isDay(c[0]) {
			year, day = c[0], c[1] // YMD, MYD or YDM
		} else {
			day, year = c[0], c[1] // DMY, MDY or DYM
		}
	}
	if !d.iso {
		if between(year, 0, 49) {
			year += 2000
		} else if between(year, 50, 99) {
			year += 1900
		}
	}
	if !isMonth(month) || !isDay(day) {
		return dayResult{}, false
	}
	return dayResult{year, month, day}, true
}

type timeComposer struct {
	comp       [4]int
	index      int
	hourOffset int // 0 for AM, 12 for PM, kNone when no AM/PM was seen
}

func (t *timeComposer) empty() bool { return t.index == 0 }

func (t *timeComposer) add(n int) bool {
	if t.index < len(t.comp) {
		t.comp[t.index] = n
		t.index++
		return true
	}
	return false
}

func (t *timeComposer) addFinal(n int) bool {
	if !t.add(n) {
		return false
	}
	for t.index < len(t.comp) {
		t.comp[t.index] = 0
		t.index++
	}
	return true
}

func (t *timeComposer) expecting(n int) bool {
	return (t.index == 1 && between(n, 0, 59)) || (t.index == 2 && between(n, 0, 59)) || (t.index == 3 && between(n, 0, 999))
}

func (t *timeComposer) write() ([4]int, bool) {
	for t.index < len(t.comp) {
		t.comp[t.index] = 0
		t.index++
	}
	hour, minute, second, ms := t.comp[0], t.comp[1], t.comp[2], t.comp[3]
	if t.hourOffset != kNone {
		if !between(hour, 0, 12) {
			return t.comp, false
		}
		hour = hour%12 + t.hourOffset
	}
	if !between(hour, 0, 23) || !between(minute, 0, 59) || !between(second, 0, 59) || !between(ms, 0, 999) {
		// A 24th hour is allowed if minutes, seconds and milliseconds are 0.
		if hour != 24 || minute != 0 || second != 0 || ms != 0 {
			return t.comp, false
		}
	}
	return [4]int{hour, minute, second, ms}, true
}

type tzComposer struct {
	sign, hour, minute int
}

func (z *tzComposer) set(hours int) {
	z.sign = 1
	if hours < 0 {
		z.sign = -1
		hours = -hours
	}
	z.hour, z.minute = hours, 0
}
func (z *tzComposer) empty() bool { return z.hour == kNone }
func (z *tzComposer) isUTC() bool { return z.hour == 0 && z.minute == 0 }
func (z *tzComposer) expecting(n int) bool {
	return z.hour != kNone && z.minute == kNone && between(n, 0, 59)
}

type dateParser struct {
	sc   *dateScanner
	day  dayComposer
	time timeComposer
	tz   tzComposer
}

// parseES5 reads the ECMAScript ISO format as far as it goes. It returns the
// first token it could not use (tokEnd when the whole string was ISO), which
// the legacy parser continues from, or ok=false when the string is
// definitively invalid.
func (p *dateParser) parseES5() (dateToken, bool) {
	sc := p.sc
	p.day.named, p.time.hourOffset = kNone, kNone
	p.tz = tzComposer{sign: kNone, hour: kNone, minute: kNone}
	invalid := func() (dateToken, bool) { return dateToken{}, false }

	// Mandatory date: [('-'|'+')yy]yyyy[-MM[-DD]]
	if sc.peek().isSign() {
		signTok := sc.next()
		if !sc.peek().isFixed(6) {
			return signTok, true
		}
		sign := signOf(signTok.sym)
		year := sc.next().num
		if sign < 0 && year == 0 {
			return signTok, true
		}
		p.day.add(sign * year)
	} else if sc.peek().isFixed(4) {
		p.day.add(sc.next().num)
	} else {
		return sc.next(), true
	}
	if sc.skipSymbol('-') {
		if !sc.peek().isFixed(2) || !isMonth(sc.peek().num) {
			return sc.next(), true
		}
		p.day.add(sc.next().num)
		if sc.skipSymbol('-') {
			if !sc.peek().isFixed(2) || !isDay(sc.peek().num) {
				return sc.next(), true
			}
			p.day.add(sc.next().num)
		}
	}
	// Optional time.
	if !(sc.peek().kind == tokKeyword && sc.peek().kw == kwTimeSep) {
		if sc.peek().kind != tokEnd {
			return sc.next(), true
		}
	} else {
		sc.next()
		if !sc.peek().isFixed(2) || !between(sc.peek().num, 0, 24) {
			return invalid()
		}
		hourIs24 := sc.peek().num == 24
		p.time.add(sc.next().num)
		if !sc.skipSymbol(':') {
			return invalid()
		}
		if !sc.peek().isFixed(2) || !between(sc.peek().num, 0, 59) || (hourIs24 && sc.peek().num > 0) {
			return invalid()
		}
		p.time.add(sc.next().num)
		if sc.skipSymbol(':') {
			if !sc.peek().isFixed(2) || !between(sc.peek().num, 0, 59) || (hourIs24 && sc.peek().num > 0) {
				return invalid()
			}
			p.time.add(sc.next().num)
			if sc.skipSymbol('.') {
				if sc.peek().kind != tokNumber || (hourIs24 && sc.peek().num > 0) {
					return invalid()
				}
				// Allow more or less than the mandated three digits.
				p.time.add(readMilliseconds(sc.next()))
			}
		}
		// Optional zone: 'Z' | ('+'|'-')hh':'mm
		switch pk := sc.peek(); {
		case pk.isKeywordZ():
			sc.next()
			p.tz.set(0)
		case pk.isSymbol('+') || pk.isSymbol('-'):
			p.tz.sign = signOf(sc.next().sym)
			if sc.peek().isFixed(4) {
				// hhmm extension syntax.
				hm := sc.next().num
				hour, minute := hm/100, hm%100
				if !between(hour, 0, 23) || !between(minute, 0, 59) {
					return invalid()
				}
				p.tz.hour, p.tz.minute = hour, minute
			} else {
				// hh:mm standard syntax.
				if !sc.peek().isFixed(2) || !between(sc.peek().num, 0, 23) {
					return invalid()
				}
				p.tz.hour = sc.next().num
				if !sc.skipSymbol(':') {
					return invalid()
				}
				if !sc.peek().isFixed(2) || !between(sc.peek().num, 0, 59) {
					return invalid()
				}
				p.tz.minute = sc.next().num
			}
		}
		if sc.peek().kind != tokEnd {
			return invalid()
		}
	}
	// "When the time zone offset is absent, date-only forms are interpreted as
	// a UTC time and date-time forms are interpreted as a local time."
	if p.tz.empty() && p.time.empty() {
		p.tz.set(0)
	}
	p.day.iso = true
	return dateToken{kind: tokEnd}, true
}
