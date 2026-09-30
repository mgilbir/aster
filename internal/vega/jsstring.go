package vega

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

var (
	dayNames   = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	monthNames = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
)

// jsString is JavaScript's String(x). A date prints as Date.prototype.toString
// does, down to the second and in the view's local zone, which is how two dates
// within one second can share a key.
func (v *runView) jsString(x jsval.Value) string {
	if x.IsTimestamp() {
		return dateToString(x.NumValue(), v.zone)
	}
	return x.AsString()
}

// dateToString is Date.prototype.toString: "Thu Jan 01 1970 00:00:00 GMT+0000
// (Coordinated Universal Time)".
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
	b.WriteString(padN(f.Day, 2))
	b.WriteByte(' ')
	if f.Year < 0 {
		b.WriteByte('-')
		b.WriteString(padN(-f.Year, 4))
	} else {
		b.WriteString(padN(f.Year, 4))
	}
	b.WriteByte(' ')
	b.WriteString(padN(f.Hour, 2) + ":" + padN(f.Minute, 2) + ":" + padN(f.Second, 2))
	b.WriteString(" GMT")
	if off < 0 {
		b.WriteByte('-')
		off = -off
	} else {
		b.WriteByte('+')
	}
	b.WriteString(padN(off/60, 2) + padN(off%60, 2))
	name := "Coordinated Universal Time"
	if loc := z.Location(); !z.IsUTC() && loc != time.UTC {
		name, _ = time.UnixMilli(int64(math.Floor(t))).In(loc).Zone()
	}
	b.WriteString(" (" + name + ")")
	return b.String()
}

func padN(n, w int) string {
	s := strconv.Itoa(n)
	for len(s) < w {
		s = "0" + s
	}
	return s
}
