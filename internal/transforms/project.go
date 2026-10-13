package transforms

import (
	"context"

	"github.com/mgilbir/aster/internal/jsval"
)

// shallowCopyTuple is vega-dataflow's derive: a new tuple holding every field
// of t in order. A non-object yields an empty tuple. The copy is a tuple of
// its own (derive ingests it, with the next id), so it does not keep t's id:
// the dataflow gives it one, and counts it as a row the transform made.
func shallowCopyTuple(t jsval.Value) *jsval.Object {
	if o := t.ObjValue(); o != nil {
		c := o.Clone()
		c.SetTupleID(0)
		return c
	}
	return jsval.NewObject(0)
}

// ProjectParams configures Project.
type ProjectParams struct {
	// Fields are the accessors to keep. A nil slice (upstream: no `fields`
	// parameter) copies every field of each tuple instead; a non-nil empty
	// slice produces empty tuples.
	Fields []Field
	// As names the output fields; an empty or missing entry falls back to the
	// accessor's name.
	As []string
}

// Project creates new tuples holding only the requested fields, in the order
// requested (later duplicates overwrite earlier ones in place).
func Project(ctx context.Context, data []jsval.Value, p ProjectParams) ([]jsval.Value, error) {
	out := make([]jsval.Value, len(data))
	names := make([]string, len(p.Fields))
	for i, f := range p.Fields {
		names[i] = f.Key()
		if i < len(p.As) && p.As[i] != "" {
			names[i] = p.As[i]
		}
	}
	for i, t := range data {
		if err := poll(ctx, i); err != nil {
			return nil, err
		}
		if p.Fields == nil {
			out[i] = jsval.Obj(shallowCopyTuple(t))
			continue
		}
		o := jsval.NewObject(len(p.Fields))
		for j, f := range p.Fields {
			o.Set(names[j], f.Apply(t))
		}
		out[i] = jsval.Obj(o)
	}
	return out, nil
}
