package vega

import (
	"github.com/mgilbir/aster/internal/jsval"
)

// Upstream's crossfilter hands on a pulse whose added "tuples" are the
// indices of its tuples (numbers), with the tuples themselves as the pulse's
// source; a resolvefilter maps the indices it is given back to tuples and
// filters the source lazily. Operators read one or the other: what a pulse
// adds (an aggregate, a collect, a filter, a data join), or its source,
// materialised through the lazy filter (most transforms on their first run,
// whose parameters are all modified, and an extent of changed data), or the
// raw source (a sieve: what data() returns). Everywhere else the two are the
// same list, and the engine only keeps one (flowPulse.tuples).

// pulseSource is a pulse's source where it is not what the pulse adds, and
// the filter upstream applies to it lazily (nil for none).
type pulseSource struct {
	tuples []jsval.Value
	filter func(jsval.Value) bool
}

// sourceView is the pulse's source, materialised: filtered, a filter's
// test charged to g a row at a time.
func (p *flowPulse) sourceView(g *flowGraph) []jsval.Value {
	if p.src == nil {
		return p.tuples
	}
	if p.src.filter == nil {
		return p.src.tuples
	}
	out := make([]jsval.Value, 0, len(p.src.tuples))
	for i, t := range p.src.tuples {
		g.rows(i, len(p.src.tuples), 2)
		if p.src.filter(t) {
			out = append(out, t)
		}
	}
	return out
}

// rawSource is the pulse's source as a sieve keeps it, unfiltered.
func (p *flowPulse) rawSource() []jsval.Value {
	if p.src == nil {
		return p.tuples
	}
	return p.src.tuples
}

// readsSource are the transforms that read their pulse's source on a first
// run (when every parameter counts as modified): materialised, through the
// lazy filter, or raw (stack, pie, impute, voronoi and force read
// pulse.source itself). The others read what the pulse adds. Upstream's
// transform functions, one by one.
var readsSource = map[string]sourceRead{
	"lookup": materialised, "flatten": materialised,
	"fold": materialised, "cross": materialised, "density": materialised,
	"dotbin": materialised, "kde": materialised, "kde2d": materialised,
	"contour": materialised, "isocontour": materialised, "quantile": materialised,
	"loess": materialised, "regression": materialised, "label": materialised,
	"wordcloud": materialised, "linkpath": materialised, "geopoint": materialised,
	"geopath": materialised, "heatmap": materialised, "countpattern": materialised,
	"stack": raw, "pie": raw, "impute": raw, "voronoi": raw, "force": raw,
}

type sourceRead uint8

const (
	addsOnly sourceRead = iota
	materialised
	raw
)

// input is the list a transform of type typ reads from pulse.
func (p *flowPulse) input(g *flowGraph, typ string) []jsval.Value {
	switch readsSource[typ] {
	case materialised:
		return p.sourceView(g)
	case raw:
		return p.rawSource()
	}
	return p.tuples
}

// replacesSource are the transforms whose output is a new data set, its own
// source (Pulse.fork(NO_SOURCE), out.source = ...); keepsSource those whose
// output adds a list of their own but keeps the source (a filter adds the
// tuples that pass, an impute its new tuples too). Every other transform
// annotates what it read and hands the pulse on as it came.
var (
	replacesSource = map[string]bool{
		"countpattern": true, "cross": true, "density": true, "flatten": true,
		"fold": true, "sequence": true, "kde": true, "project": true, "quantile": true,
		"sample": true, "contour": true, "isocontour": true, "kde2d": true,
		"loess": true, "regression": true, "pivot": true,
	}
	keepsSource = map[string]bool{"filter": true, "impute": true}
	// keepsTree are the transforms whose output pulse still has the
	// source it came with (pulse.reflow, pulse.modifies, or a fork that
	// keeps the source: filter, impute), so `pulse.source.root` is still
	// the tree whatever tuples they add, and however few: an empty
	// source is the same array with its root.
	keepsTree = map[string]bool{
		"filter": true, "formula": true, "identifier": true, "bin": true, "timeunit": true,
		"window": true, "joinaggregate": true, "lookup": true, "impute": true, "dotbin": true,
		"geopoint": true, "geopath": true, "geoshape": true, "linkpath": true, "pie": true,
		"stack": true, "voronoi": true, "force": true, "heatmap": true, "wordcloud": true,
	}
)

// writesAdds are the transforms that write a field into each tuple the pulse
// adds (formula, bin, timeunit, identifier, window on a first run), by the
// name of the first field each writes: on a crossfilter's index, a number,
// that is upstream's TypeError.
var writesAdds = map[string]func(g *flowGraph, p *opParams) string{
	"formula":    func(_ *flowGraph, p *opParams) string { return p.keyOf("as") },
	"identifier": func(_ *flowGraph, p *opParams) string { return p.keyOf("as") },
	"bin":        func(_ *flowGraph, p *opParams) string { return p.asPair("bin0", "bin1")[0] },
	"timeunit":   func(_ *flowGraph, p *opParams) string { return p.asPair("unit0", "unit1")[0] },
	"window": func(g *flowGraph, p *opParams) string {
		if ops := windowParams(g, p).Ops; len(ops) > 0 {
			o := ops[0]
			return o.Name()
		}
		return ""
	},
}

// checkWritesAdds raises the TypeError a transform of type typ meets writing
// its first field into a primitive the pulse adds.
func checkWritesAdds(g *flowGraph, typ string, p *opParams, in []jsval.Value) {
	name := writesAdds[typ]
	if name == nil {
		return
	}
	for i, t := range in {
		g.rows(i, len(in), 1)
		var kind string
		switch t.Kind() {
		case jsval.KindNum:
			kind = "number"
		case jsval.KindStr:
			kind = "string"
		case jsval.KindBool:
			kind = "boolean"
		default:
			continue
		}
		fail("TypeError: Cannot create property '%s' on %s '%s'", name(g, p), kind, jsval.StringPrefix(t, jsval.MessageBytes))
	}
}

// sourcePulse makes res, the output of a transform of type typ that read
// from pulse, carry pulse's source as upstream's would. Only a pulse with a
// distinct source is touched.
func sourcePulse(typ string, pulse, res *flowPulse) {
	switch {
	case pulse.src == nil || replacesSource[typ]:
	case keepsSource[typ]:
		res.src = pulse.src
		if typ == "impute" {
			// out.add = add.concat(imputed): the new tuples follow what the
			// pulse adds (impute read the source, and appended to it)
			src := pulse.rawSource()
			if len(res.tuples) >= len(src) {
				res.tuples = append(append([]jsval.Value(nil), pulse.tuples...), res.tuples[len(src):]...)
			}
		}
	default:
		res.tuples = pulse.tuples
		res.src = pulse.src
	}
}
