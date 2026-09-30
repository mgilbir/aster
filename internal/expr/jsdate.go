package expr

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mgilbir/aster/internal/format"
)

// Dates are epoch-millisecond float64s (NaN for an Invalid Date) carried in a
// jsval.Value of KindTimestamp. Calendar arithmetic, Date.parse and the
// interval functions live in package format; this file has the remaining
// Date.prototype.toString.

var (
	dayNames   = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	monthNames = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
)

// dateToString is Date.prototype.toString: "Thu Jan 01 1970 00:00:00 GMT+0000
// (Coordinated Universal Time)". The time zone name is the zone's abbreviation
// except for UTC.
func dateToString(t float64, z format.Zone) string {
	if !format.Valid(t) {
		return "Invalid Date"
	}
	f := z.Fields(t)
	off := -int(z.TimezoneOffset(t)) // minutes east of UTC
	var b strings.Builder
	b.WriteString(dayNames[f.Weekday])
	b.WriteByte(' ')
	b.WriteString(monthNames[f.Month])
	b.WriteByte(' ')
	b.WriteString(pad2(f.Day))
	b.WriteByte(' ')
	if f.Year < 0 {
		b.WriteByte('-')
		b.WriteString(padN(-f.Year, 4))
	} else {
		b.WriteString(padN(f.Year, 4))
	}
	b.WriteByte(' ')
	b.WriteString(pad2(f.Hour) + ":" + pad2(f.Minute) + ":" + pad2(f.Second))
	b.WriteString(" GMT")
	if off < 0 {
		b.WriteByte('-')
		off = -off
	} else {
		b.WriteByte('+')
	}
	b.WriteString(pad2(off/60) + pad2(off%60))
	name := "Coordinated Universal Time"
	if loc := z.Location(); !z.IsUTC() && loc != time.UTC {
		name, _ = time.UnixMilli(int64(math.Floor(t))).In(loc).Zone()
	}
	b.WriteString(" (" + name + ")")
	return b.String()
}

func pad2(n int) string { return padN(n, 2) }

func padN(n, w int) string {
	s := strconv.Itoa(n)
	for len(s) < w {
		s = "0" + s
	}
	return s
}
