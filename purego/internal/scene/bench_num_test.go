package scene

import "testing"

// BenchmarkStringPathNumbers writes the coordinates of a long polyline with
// the three-digit rounding d3-shape's generators use.
func BenchmarkStringPathNumbers(b *testing.B) {
	p := &StringPath{}
	p.SetDigits(3)
	b.ReportAllocs()
	for b.Loop() {
		p.Reset()
		for i := 0; i < 1000; i++ {
			x := float64(i) * 1.37281
			y := 400 - float64(i)*0.3917
			if i == 0 {
				p.MoveTo(x, y)
			} else {
				p.LineTo(x, y)
			}
		}
	}
}
