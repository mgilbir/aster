package transforms

import (
	"context"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Sample draws a reservoir sample of at most size tuples, keeping input order
// of slots as upstream does: the first size tuples fill the reservoir, then
// tuple number cnt replaces slot ~~((cnt+1)*random()) when that lands inside
// the reservoir. A nil rand means DefaultRand. Tuples are not copied.
func Sample(ctx context.Context, data []jsval.Value, size int, rand Rand) ([]jsval.Value, error) {
	if rand == nil {
		rand = DefaultRand
	}
	res := make([]jsval.Value, 0, max(0, min(size, len(data))))
	for cnt, t := range data {
		if err := poll(ctx, cnt); err != nil {
			return nil, err
		}
		if len(res) < size {
			res = append(res, t)
			continue
		}
		if idx := int(float64(cnt+1) * rand()); idx < len(res) {
			res[idx] = t
		}
	}
	return res, nil
}
