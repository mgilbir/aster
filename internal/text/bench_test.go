package text

import (
	"strconv"
	"sync/atomic"
	"testing"
)

// labels are distinct chart-label-like strings; there are more of them than
// the measurement cache holds, so cycling through them never hits it.
var labels = func() []string {
	l := make([]string, 4*maxCacheItems)
	for i := range l {
		l[i] = "Category " + strconv.Itoa(i) + ", 1,234.5%"
	}
	return l
}()

const benchFont = "11px Helvetica, Arial, sans-serif"

func BenchmarkMeasureCached(b *testing.B) {
	b.Run("text", func(b *testing.B) {
		m, _ := New()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m.MeasureText("Horsepower (hp)", benchFont)
		}
	})
}

func BenchmarkMeasureUncached(b *testing.B) {
	b.Run("text", func(b *testing.B) {
		m, _ := New()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m.MeasureText(labels[i%len(labels)], benchFont)
		}
	})
	b.Run("text-exact", func(b *testing.B) {
		m, _ := New(WithExactAdvances())
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m.MeasureText(labels[i%len(labels)], benchFont)
		}
	})
}

func BenchmarkMeasureLong(b *testing.B) {
	long := corpusStrings[len(corpusStrings)-1]
	b.Run("text", func(b *testing.B) {
		m, _ := New()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m.ShapeText(long, benchFont) // ShapeText is not cached
		}
	})
}

func BenchmarkMeasureMixedFallback(b *testing.B) {
	s := "Smile \U0001F600 and 日本語 text"
	b.Run("text", func(b *testing.B) {
		m, _ := New()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			m.ShapeText(s, benchFont)
		}
	})
}

func BenchmarkNew(b *testing.B) {
	b.Run("text", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			New()
		}
	})
}

func BenchmarkGlyphOutline(b *testing.B) {
	m, _ := New()
	runs, _ := m.ShapeText("Ag", "12px sans-serif")
	f, gid := runs[0].Face, runs[0].Glyphs[0].GID
	GlyphOutline(f, gid, 12)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		GlyphOutline(f, gid, 12)
	}
}

// BenchmarkMeasureUncachedParallel is cold measurement from every core at
// once; ns/op is wall time per measurement across all goroutines.
func BenchmarkMeasureUncachedParallel(b *testing.B) {
	m, _ := New()
	var n atomic.Int64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			m.MeasureText(labels[int(n.Add(1))%len(labels)], benchFont)
		}
	})
}
