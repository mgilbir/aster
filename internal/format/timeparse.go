package format

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// parseOp is one step of a compiled parse: a literal byte to match, or a
// directive.
type parseOp struct {
	dir byte // directive letter, or 0 for a literal byte
	lit byte
}

// TimeParser is a compiled d3 time parse function (timeParse or utcParse).
type TimeParser struct {
	loc  *TimeLocale
	zone Zone
	spec string
	ops  []parseOp
	bad  bool // the specifier holds a directive d3 cannot parse: every parse fails
}

// Parse is d3.timeParse(specifier) in the given zone (UTC for utcParse).
func (l *TimeLocale) Parse(specifier string, z Zone) *TimeParser {
	p := &TimeParser{loc: l, zone: z, spec: specifier}
	p.ops, p.bad = l.compileParse(nil, specifier, 0, new(int))
	return p
}

const parseDirectives = "aAbBcdefgGHIjLmMpqQsSuUVwWxXyYZ%"

func (l *TimeLocale) compileParse(ops []parseOp, spec string, depth int, work *int) ([]parseOp, bool) {
	for i := 0; i < len(spec); {
		c := spec[i]
		i++
		if c != '%' {
			ops = append(ops, parseOp{lit: c})
			continue
		}
		if i >= len(spec) { // "%" at the end: parses[""] is missing
			return ops, true
		}
		c = spec[i]
		i++
		if c == '-' || c == '_' || c == '0' { // padding modifiers are ignored
			if i >= len(spec) {
				return ops, true
			}
			c = spec[i]
			i++
		}
		if strings.IndexByte(parseDirectives, c) < 0 {
			return ops, true
		}
		switch c {
		case 'c', 'x', 'X':
			if *work++; depth >= maxLocaleNesting || *work > maxLocaleExpansions {
				return ops, true
			}
			sub := l.def.DateTime
			if c == 'x' {
				sub = l.def.Date
			} else if c == 'X' {
				sub = l.def.Time
			}
			var bad bool
			if ops, bad = l.compileParse(ops, sub, depth+1, work); bad {
				return ops, true
			}
		default:
			ops = append(ops, parseOp{dir: c})
		}
	}
	return ops, false
}

// String is the specifier text.
func (p *TimeParser) String() string { return p.spec }

// parsed field presence flags (`"x" in d` in d3).
const (
	hasP = 1 << iota
	hasBigQ
	hasS
	hasZ
	hasV
	hasW
	hasU
	hasBigU
	hasBigW
	hasM
	hasQuarter
)

type parseState struct {
	flags               uint
	y, m, d, H, M, S, L float64
	p, q, Q, s, Z       float64
	V, w, u, U, W       float64
}

// Parse parses s. ok is false where d3 returns null (the string does not
// match). The result may still be NaN (an Invalid Date), for example for a
// month number that is out of range in an unusual combination.
func (p *TimeParser) Parse(s string) (t float64, ok bool) {
	if p.bad {
		return math.NaN(), false
	}
	st := parseState{y: 1900, d: 1}
	i := p.loc.run(&st, p.ops, s, 0)
	if i != len(s) {
		return math.NaN(), false
	}
	return p.build(&st)
}

func (p *TimeParser) build(d *parseState) (float64, bool) {
	// If a UNIX timestamp is specified, return it.
	if d.flags&hasBigQ != 0 {
		return timeClip(d.Q), true
	}
	if d.flags&hasS != 0 {
		return timeClip(float64(d.s*1000) + d.L), true
	}
	// If this is utcParse, never use the local timezone.
	if p.zone.IsUTC() && d.flags&hasZ == 0 {
		d.Z, d.flags = 0, d.flags|hasZ
	}
	// The am-pm flag is 0 for AM, and 1 for PM.
	if d.flags&hasP != 0 {
		d.H = math.Mod(d.H, 12) + float64(d.p*12)
	}
	// If the month was not specified, inherit from the quarter.
	if d.flags&hasM == 0 {
		if d.flags&hasQuarter != 0 {
			d.m = d.q
		} else {
			d.m = 0
		}
	}
	utc := d.flags&hasZ != 0
	zone := p.zone
	if utc {
		zone = UTC
	}
	jan1 := func() float64 { return zone.FullYearDate(d.y, 0, 1, 0, 0, 0, 0) }

	// Convert day-of-week and week-of-year to day-of-year.
	switch {
	case d.flags&hasV != 0:
		if d.V < 1 || d.V > 53 {
			return math.NaN(), false
		}
		if d.flags&hasW == 0 {
			d.w = 1
		}
		week := jan1()
		day := float64(zone.Fields(week).Weekday)
		if math.IsNaN(week) {
			day = math.NaN()
		}
		monday := zone.Week(1)
		if day > 4 || day == 0 {
			week = monday.Ceil(week)
		} else {
			week = monday.Floor(week)
		}
		week = zone.Day().Offset(week, (d.V-1)*7)
		if Valid(week) {
			f := zone.Fields(week)
			d.y, d.m = float64(f.Year), float64(f.Month)
			d.d = float64(f.Day) + math.Mod(d.w+6, 7)
		} else {
			d.y, d.m, d.d = math.NaN(), math.NaN(), math.NaN()
		}
	case d.flags&(hasBigW|hasBigU) != 0:
		if d.flags&hasW == 0 {
			switch {
			case d.flags&hasU != 0:
				d.w = math.Mod(d.u, 7)
			case d.flags&hasBigW != 0:
				d.w = 1
			default:
				d.w = 0
			}
		}
		day := math.NaN()
		if j := jan1(); Valid(j) {
			day = float64(zone.Fields(j).Weekday)
		}
		d.m = 0
		if d.flags&hasBigW != 0 {
			d.d = math.Mod(d.w+6, 7) + float64(d.W*7) - math.Mod(day+5, 7)
		} else {
			d.d = d.w + float64(d.U*7) - math.Mod(day+6, 7)
		}
	}

	// If a time zone is specified, all fields are interpreted as UTC and then
	// offset according to the specified time zone.
	if utc {
		d.H += math.Trunc(math.Trunc(d.Z / 100)) // `d.Z / 100 | 0`
		d.M += math.Mod(d.Z, 100)
		return UTC.FullYearDate(d.y, d.m, d.d, d.H, d.M, d.S, d.L), true
	}
	// Otherwise, all fields are in local time.
	return p.zone.FullYearDate(d.y, d.m, d.d, d.H, d.M, d.S, d.L), true
}

// run matches ops against s from index j and returns the new index, or -1.
func (l *TimeLocale) run(d *parseState, ops []parseOp, s string, j int) int {
	for _, op := range ops {
		if j >= len(s) {
			return -1
		}
		if op.dir == 0 {
			if s[j] != op.lit {
				return -1
			}
			j++
			continue
		}
		if j = l.parseDirective(d, op.dir, s, j); j < 0 {
			return -1
		}
	}
	return j
}

func isJSSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
}

// number is /^\s*\d+/ applied to the window s[i:i+width] (width <= 0: the
// rest of the string). It returns the digits' value and the index after them.
func number(s string, i, width int) (v float64, next int, ok bool) {
	end := len(s)
	if width > 0 && i+width < end {
		end = i + width
	}
	k := i
	for k < end && isJSSpace(s[k]) {
		k++
	}
	start := k
	for k < end && s[k] >= '0' && s[k] <= '9' {
		k++
	}
	if k == start {
		return 0, -1, false
	}
	if k-start <= 15 { // exact in an int64, as in a float64; not in a 32-bit int
		var n int64
		for _, c := range []byte(s[start:k]) {
			n = n*10 + int64(c-'0')
		}
		return float64(n), k, true
	}
	f, _ := strconv.ParseFloat(s[start:k], 64)
	return f, k, true
}

func (l *TimeLocale) parseDirective(d *parseState, c byte, s string, i int) int {
	var v float64
	var next int
	var ok bool
	switch c {
	case 'a':
		return matchName(d, l.def.ShortDays, l.shortWeekdayLookup, s, i, &d.w, hasW)
	case 'A':
		return matchName(d, l.def.Days, l.weekdayLookup, s, i, &d.w, hasW)
	case 'b':
		return matchName(d, l.def.ShortMonths, l.shortMonthLookup, s, i, &d.m, hasM)
	case 'B':
		return matchName(d, l.def.Months, l.monthLookup, s, i, &d.m, hasM)
	case 'p':
		return matchName(d, l.def.Periods, l.periodLookup, s, i, &d.p, hasP)
	case 'd', 'e':
		if v, next, ok = number(s, i, 2); ok {
			d.d = v
		}
	case 'f':
		if v, next, ok = number(s, i, 6); ok {
			d.L = math.Floor(v / 1000)
		}
	case 'g', 'y':
		if v, next, ok = number(s, i, 2); ok {
			if v > 68 {
				d.y = v + 1900
			} else {
				d.y = v + 2000
			}
		}
	case 'G', 'Y':
		if v, next, ok = number(s, i, 4); ok {
			d.y = v
		}
	case 'H', 'I':
		if v, next, ok = number(s, i, 2); ok {
			d.H = v
		}
	case 'j':
		if v, next, ok = number(s, i, 3); ok {
			d.m, d.d = 0, v
			d.flags |= hasM
		}
	case 'L':
		if v, next, ok = number(s, i, 3); ok {
			d.L = v
		}
	case 'm':
		if v, next, ok = number(s, i, 2); ok {
			d.m = v - 1
			d.flags |= hasM
		}
	case 'M':
		if v, next, ok = number(s, i, 2); ok {
			d.M = v
		}
	case 'q':
		if v, next, ok = number(s, i, 1); ok {
			d.q = float64(v*3) - 3
			d.flags |= hasQuarter
		}
	case 'Q':
		if v, next, ok = number(s, i, 0); ok {
			d.Q = v
			d.flags |= hasBigQ
		}
	case 's':
		if v, next, ok = number(s, i, 0); ok {
			d.s = v
			d.flags |= hasS
		}
	case 'S':
		if v, next, ok = number(s, i, 2); ok {
			d.S = v
		}
	case 'u':
		if v, next, ok = number(s, i, 1); ok {
			d.u = v
			d.flags |= hasU
		}
	case 'U':
		if v, next, ok = number(s, i, 2); ok {
			d.U = v
			d.flags |= hasBigU
		}
	case 'V':
		if v, next, ok = number(s, i, 2); ok {
			d.V = v
			d.flags |= hasV
		}
	case 'w':
		if v, next, ok = number(s, i, 1); ok {
			d.w = v
			d.flags |= hasW
		}
	case 'W':
		if v, next, ok = number(s, i, 2); ok {
			d.W = v
			d.flags |= hasBigW
		}
	case 'Z':
		return parseZone(d, s, i)
	case '%':
		if s[i] == '%' {
			return i + 1
		}
		return -1
	default:
		return -1
	}
	if !ok {
		return -1
	}
	return next
}

// matchName is the regular expression /^(?:name1|name2|...)/i: the first
// name, in list order, that is a case-insensitive prefix of the input wins
// (not the longest). The value stored is the index that d3's lower-cased
// lookup map holds for the matched text, NaN when the lookup misses.
func matchName(d *parseState, names []string, lookup map[string]int, s string, i int, dst *float64, flag uint) int {
	rest := s[i:]
	for _, name := range names {
		n := utf8.RuneCountInString(name)
		end, cnt := 0, 0
		for end < len(rest) && cnt < n {
			_, w := utf8.DecodeRuneInString(rest[end:])
			end += w
			cnt++
		}
		if cnt < n {
			continue
		}
		if strings.EqualFold(rest[:end], name) {
			if idx, ok := lookup[strings.ToLower(rest[:end])]; ok {
				*dst = float64(idx)
			} else {
				*dst = math.NaN()
			}
			d.flags |= flag
			return i + end
		}
	}
	return -1
}

// parseZone reproduces /^(Z)|([+-]\d\d)(?::?(\d\d))?/ on the six characters at
// i. Only the first alternative is anchored, so an offset may be found later
// in the window; the match length is then added to i without accounting for
// the skipped characters, exactly as d3 does.
func parseZone(d *parseState, s string, i int) int {
	end := min(len(s), i+6)
	w := s[i:end]
	for p := 0; p < len(w); p++ {
		if p == 0 && w[0] == 'Z' {
			d.Z, d.flags = 0, d.flags|hasZ
			return i + 1
		}
		if (w[p] == '+' || w[p] == '-') && p+2 < len(w)+0 && isDigit(w[p+1]) && isDigit(w[p+2]) {
			n := 3
			hh, mm := w[p:p+3], "00"
			q := p + 3
			if q < len(w) && w[q] == ':' {
				q++
			}
			if q+1 < len(w)+0 && isDigit(w[q]) && isDigit(w[q+1]) {
				mm = w[q : q+2]
				n = q + 2 - p
			}
			v, _ := strconv.Atoi(hh[1:] + mm)
			if hh[0] == '+' {
				v = -v
			} else {
				// -( "-0530" ) is +530
				_ = v
			}
			d.Z, d.flags = float64(v), d.flags|hasZ
			return i + n
		}
	}
	return -1
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
