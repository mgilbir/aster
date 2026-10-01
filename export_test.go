package aster

import "time"

// WithClockForTest pins the clock now() and datetime() read, so a render
// that draws the current time can be compared with the node oracle, which
// pins the same instant.
func WithClockForTest(now func() time.Time) Option {
	return func(c *config) { c.now = now }
}
