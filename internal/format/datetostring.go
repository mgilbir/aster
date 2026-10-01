package format

import (
	"math"
	"strconv"
	"strings"
	"time"
)

var (
	dayNames   = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	monthNames = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
)

// DateToString is Date.prototype.toString, the text String(date) gives:
// "Thu Jan 01 1970 00:00:00 GMT+0000 (Coordinated Universal Time)". The time
// zone name is the zone's abbreviation except for UTC. It is exact to the
// second: two dates within one second print alike.
func DateToString(t float64, z Zone) string {
	if !Valid(t) {
		return "Invalid Date"
	}
	f := z.Fields(t)
	off := -int(z.TimezoneOffset(t)) // minutes east of UTC
	var b strings.Builder
	b.WriteString(dayNames[f.Weekday])
	b.WriteByte(' ')
	b.WriteString(monthNames[f.Month])
	b.WriteByte(' ')
	b.WriteString(padInt(f.Day, 2))
	b.WriteByte(' ')
	if f.Year < 0 {
		b.WriteByte('-')
		b.WriteString(padInt(-f.Year, 4))
	} else {
		b.WriteString(padInt(f.Year, 4))
	}
	b.WriteByte(' ')
	b.WriteString(padInt(f.Hour, 2) + ":" + padInt(f.Minute, 2) + ":" + padInt(f.Second, 2))
	b.WriteString(" GMT")
	if off < 0 {
		b.WriteByte('-')
		off = -off
	} else {
		b.WriteByte('+')
	}
	b.WriteString(padInt(off/60, 2) + padInt(off%60, 2))
	name := "Coordinated Universal Time"
	if loc := z.Location(); !z.IsUTC() && loc != time.UTC {
		name, _ = time.UnixMilli(int64(math.Floor(t))).In(loc).Zone()
	}
	b.WriteString(" (" + name + ")")
	return b.String()
}

func padInt(n, w int) string {
	s := strconv.Itoa(n)
	for len(s) < w {
		s = "0" + s
	}
	return s
}
