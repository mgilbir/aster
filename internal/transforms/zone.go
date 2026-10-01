package transforms

import (
	"context"

	"github.com/mgilbir/aster/internal/format"
)

type zoneKey struct{}

// WithZone returns ctx carrying the calendar z. Transforms that turn a value
// into text to key on it (a group-by, a pivot column, a lookup) write a date
// the way String(date) does, in the local zone of the view, and so need to
// know it; without one, dates are in UTC.
func WithZone(ctx context.Context, z format.Zone) context.Context {
	return context.WithValue(ctx, zoneKey{}, z)
}

func zoneOf(ctx context.Context) format.Zone {
	z, _ := ctx.Value(zoneKey{}).(format.Zone)
	return z
}
