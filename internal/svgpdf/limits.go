package svgpdf

import (
	"context"
	"errors"
	"fmt"
)

// Limits bounds the resources an SVG document may consume during conversion.
// Zero fields select the defaults.
type Limits struct {
	MaxInputBytes   int // SVG source size
	MaxElements     int // parsed elements
	MaxPathSegments int // path segments, summed over all paths (after arc conversion)
	MaxTextBytes    int // text content, summed over all <text> elements
}

const (
	defaultMaxInputBytes = 64 << 20
	defaultMaxElements   = 1_000_000
	defaultMaxSegments   = 10_000_000
	defaultMaxTextBytes  = 16 << 20
)

func (l Limits) withDefaults() Limits {
	if l.MaxInputBytes <= 0 {
		l.MaxInputBytes = defaultMaxInputBytes
	}
	if l.MaxElements <= 0 {
		l.MaxElements = defaultMaxElements
	}
	if l.MaxPathSegments <= 0 {
		l.MaxPathSegments = defaultMaxSegments
	}
	if l.MaxTextBytes <= 0 {
		l.MaxTextBytes = defaultMaxTextBytes
	}
	return l
}

// ErrLimit is wrapped by every error caused by a resource limit.
var ErrLimit = errors.New("svgpdf: resource limit exceeded")

func limitErr(format string, args ...any) error {
	return fmt.Errorf("svgpdf: "+format+": %w", append(args, ErrLimit)...)
}

// ctxErr returns the context's error, or nil for a nil or live context.
func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
