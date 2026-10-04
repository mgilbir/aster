package raster

import "github.com/mgilbir/aster/internal/csscolor"

// rgba is a non-premultiplied colour with float alpha.
type rgba struct {
	r, g, b uint8
	a       float32
}

var black = rgba{0, 0, 0, 1}

// parseColor parses a CSS colour. ok is false for anything unrecognised
// (including currentColor, which the caller resolves).
func parseColor(s string) (rgba, bool) {
	c, ok := csscolor.Parse(s)
	return rgba{c.R, c.G, c.B, c.A}, ok
}

func clampByte(v float64) uint8 { return csscolor.ClampByte(v) }
