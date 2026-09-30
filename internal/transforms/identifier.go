package transforms

import (
	"context"

	"github.com/mgilbir/aster/internal/jsval"
)

// Identifier adds a unique id to every tuple under field as, in place. counter
// is the id source shared by all identifier transforms of a view (upstream keeps
// it in the ":vega_identifier:" signal); it holds the last id issued and is
// advanced. A tuple that already has a truthy value in the field keeps it and
// consumes no id, as `t[as] = t[as] || ++id` does.
func Identifier(ctx context.Context, data []jsval.Value, as string, counter *float64) ([]jsval.Value, error) {
	id := *counter
	defer func() { *counter = id }()
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		o := t.ObjValue()
		if o == nil {
			continue
		}
		if cur := o.Lookup(as); cur.IsTruthy() {
			o.Set(as, cur)
			continue
		}
		id++
		o.Set(as, jsval.Num(id))
	}
	return data, nil
}
