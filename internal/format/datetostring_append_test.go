package format

import (
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
	"time"
)

// referenceDateToString is DateToString as it was written before
// AppendDateToString, a string built from padded pieces, kept to hold the
// appending form to the same text.
func referenceDateToString(t float64, z Zone) string {
	pad := func(n, w int) string {
		s := strconv.Itoa(n)
		for len(s) < w {
			s = "0" + s
		}
		return s
	}
	if t = timeClip(t); !Valid(t) {
		return "Invalid Date"
	}
	f := z.Fields(t)
	off := -int(z.TimezoneOffset(t))
	var b strings.Builder
	b.WriteString(dayNames[f.Weekday] + " " + monthNames[f.Month] + " " + pad(f.Day, 2) + " ")
	if f.Year < 0 {
		b.WriteString("-" + pad(-f.Year, 4))
	} else {
		b.WriteString(pad(f.Year, 4))
	}
	b.WriteString(" " + pad(f.Hour, 2) + ":" + pad(f.Minute, 2) + ":" + pad(f.Second, 2) + " GMT")
	if off < 0 {
		b.WriteByte('-')
		off = -off
	} else {
		b.WriteByte('+')
	}
	b.WriteString(pad(off/60, 2) + pad(off%60, 2))
	name := "Coordinated Universal Time"
	if loc := z.Location(); !z.IsUTC() && loc != time.UTC {
		name = zoneLongName(loc, int64(t))
	}
	b.WriteString(" (" + name + ")")
	return b.String()
}

// AppendDateToString writes what the reference writes, after what dst
// holds, for instants across the whole range a Date holds (years -271821 to
// 275760, before and after 1970, on and off the second) in zones east and
// west of UTC, with half-hour and quarter-hour offsets, and for the invalid
// ones.
func TestAppendDateToStringIsDateToString(t *testing.T) {
	zones := []Zone{UTC}
	for _, name := range []string{"America/New_York", "Asia/Kolkata", "Asia/Kathmandu", "Australia/Lord_Howe", "Pacific/Chatham", "America/St_Johns", "Europe/Dublin", "Africa/Monrovia"} {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatal(err)
		}
		zones = append(zones, Local(loc))
	}
	const maxTime = 8.64e15
	instants := []float64{0, -1, 1, 999, -999, 1000, maxTime, -maxTime, maxTime + 1, -maxTime - 1, math.NaN(), math.Inf(1), math.Inf(-1),
		-62198755200000, -62198755200001, -62135596800000, 253402300799999, 253402300800000, 1262304000000, -2208988800000}
	r := rand.New(rand.NewPCG(3, 4))
	for range 20_000 {
		instants = append(instants, (r.Float64()*2-1)*maxTime, math.Trunc((r.Float64()*2-1)*4e12), (r.Float64()*2-1)*1e11)
	}
	for _, z := range zones {
		for _, ms := range instants {
			want := referenceDateToString(ms, z)
			if got := DateToString(ms, z); got != want {
				t.Fatalf("DateToString(%v) in %v = %q, want %q", ms, z.Location(), got, want)
			}
			if got := string(AppendDateToString([]byte("key|"), ms, z)); got != "key|"+want {
				t.Fatalf("AppendDateToString(%v) in %v = %q, want %q", ms, z.Location(), got, "key|"+want)
			}
		}
	}
}

func BenchmarkDateToString(b *testing.B) {
	b.Run("string", func(b *testing.B) {
		for b.Loop() {
			_ = DateToString(1262304000000, UTC)
		}
	})
	b.Run("append", func(b *testing.B) {
		buf := make([]byte, 0, 96)
		for b.Loop() {
			buf = AppendDateToString(buf[:0], 1262304000000, UTC)
		}
	})
}
