package format

import (
	"math"
	"strconv"
	"time"
)

var (
	dayNames   = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	monthNames = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
)

// DateToString is Date.prototype.toString, the text String(date) gives:
// "Thu Jan 01 1970 00:00:00 GMT+0000 (Coordinated Universal Time)". The time
// zone name is V8's (zoneLongName); for a zone name V8 does not know as
// tzdata does, the zone's abbreviation. It is exact to the second: two dates
// within one second print alike.
func DateToString(t float64, z Zone) string {
	var buf [96]byte
	return string(AppendDateToString(buf[:0], t, z))
}

// AppendDateToString appends DateToString(t, z) to dst. Group keys, labels
// and the text of values print dates with it without a string per date.
func AppendDateToString(dst []byte, t float64, z Zone) []byte {
	if t = timeClip(t); !Valid(t) {
		return append(dst, "Invalid Date"...)
	}
	f := z.Fields(t)
	off := -int(z.TimezoneOffset(t)) // minutes east of UTC
	dst = append(dst, dayNames[f.Weekday]...)
	dst = append(dst, ' ')
	dst = append(dst, monthNames[f.Month]...)
	dst = append(dst, ' ')
	dst = appendPadInt(dst, f.Day, 2)
	dst = append(dst, ' ')
	if f.Year < 0 {
		dst = append(dst, '-')
		dst = appendPadInt(dst, -f.Year, 4)
	} else {
		dst = appendPadInt(dst, f.Year, 4)
	}
	dst = append(dst, ' ')
	dst = appendPadInt(dst, f.Hour, 2)
	dst = append(dst, ':')
	dst = appendPadInt(dst, f.Minute, 2)
	dst = append(dst, ':')
	dst = appendPadInt(dst, f.Second, 2)
	dst = append(dst, " GMT"...)
	if off < 0 {
		dst = append(dst, '-')
		off = -off
	} else {
		dst = append(dst, '+')
	}
	dst = appendPadInt(dst, off/60, 2)
	dst = appendPadInt(dst, off%60, 2)
	name := "Coordinated Universal Time"
	if loc := z.Location(); !z.IsUTC() && loc != time.UTC {
		name = zoneLongName(loc, int64(t))
	}
	dst = append(dst, " ("...)
	dst = append(dst, name...)
	return append(dst, ')')
}

// appendPadInt appends the decimal text of n with zeros put in front of it
// until it is w bytes long.
func appendPadInt(dst []byte, n, w int) []byte {
	start := len(dst)
	dst = strconv.AppendInt(dst, int64(n), 10)
	if pad := w - (len(dst) - start); pad > 0 {
		for range pad {
			dst = append(dst, 0)
		}
		copy(dst[start+pad:], dst[start:len(dst)-pad])
		for i := range pad {
			dst[start+i] = '0'
		}
	}
	return dst
}

// zoneName is a zone's two names in zoneLongNames, standard and daylight,
// and the instant from which ICU calls its offset daylight where node's
// tzdata release calls it standard (0: never).
type zoneName struct {
	std, dst     uint16
	daylightFrom int64
}

// zoneLongName is the name V8 prints in Date.prototype.toString for the
// instant ms (C121 of the 2026-10-18 audit): ICU's long name of the zone's
// current metazone ("Eastern Standard Time"), the daylight one when V8's
// DST flag is set. V8 decides that flag for an instant before 1970 or after
// 2038-01-19 at the "equivalent time" of ECMA-262 (the same month, day and
// time in a year from 2008 to 2037 that starts on the same weekday and has
// the same length), so a July of 1800 in New York prints "Eastern Daylight
// Time". The names (zonenames_table.go) are recorded from node, for its
// tzdata release (2026c); the DST flag is the host's zoneinfo's, so a host
// whose release models a zone otherwise names it by that release's flag
// (ADR 0029). A name missing from the table (one a later release adds)
// prints its tzdata abbreviation.
func zoneLongName(loc *time.Location, ms int64) string {
	e, ok := zoneNameTable[loc.String()]
	if !ok {
		name, _ := time.UnixMilli(ms).In(loc).Zone()
		return name
	}
	if ms < 0 || ms > maxEpochTimeMs {
		ms = equivalentTime(ms)
	}
	if time.UnixMilli(ms).In(loc).IsDST() || e.daylightFrom != 0 && ms >= e.daylightFrom {
		return zoneLongNames[e.dst]
	}
	return zoneLongNames[e.std]
}

// maxEpochTimeMs is V8's kMaxEpochTimeInMs, the end of 32-bit time.
const maxEpochTimeMs = int64(math.MaxInt32) * 1000

// equivalentTime is V8's DateCache::EquivalentTime: ms moved to the same
// UTC month, day and time of day in EquivalentYear.
func equivalentTime(ms int64) int64 {
	days := ms / msPerDay
	if ms%msPerDay < 0 {
		days--
	}
	within := ms - days*msPerDay
	y, m, d := civilFromDays(days)
	return (daysFromCivil(equivalentYear(y), m)+int64(d)-1)*msPerDay + within
}

// equivalentYear is V8's DateCache::EquivalentYear: the year from 2008 to
// 2037 whose 1 January falls on the same weekday, with the same leapness.
func equivalentYear(y int64) int64 {
	wd := (daysFromCivil(y, 1)%7 + 11) % 7 // 0 = Sunday
	recent := int64(1967)
	if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
		recent = 1956
	}
	recent += (wd * 12) % 28
	return 2008 + (recent+3*28-2008)%28
}
