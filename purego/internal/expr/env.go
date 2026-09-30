package expr

import (
	"github.com/mgilbir/aster/purego/internal/jsval"
)

// Env is what the runtime provides to evaluating expressions. Only signal
// lookup is mandatory. Everything else is an optional capability: the runtime
// implements whichever of the interfaces below it can serve, and expression
// functions that need a missing one answer what upstream answers when its
// context is empty (undefined, [], 0, false).
//
// Values that are opaque in JavaScript (a scale function, a gradient, a
// projection, a scenegraph item, the event object) are passed through as
// jsval.Value; the runtime decides how to represent them (typically an object
// or a string handle). Expressions never look inside them except through the
// member access and function calls defined here.
type Env interface {
	// Signal returns the value of the named signal. ok is false when there is
	// no such signal, which expressions read as undefined.
	Signal(name string) (v jsval.Value, ok bool)
}

// DataProvider serves data(name), and the selection functions that read a
// selection store dataset.
type DataProvider interface {
	// Data returns the current tuples of the named dataset as an array value.
	Data(name string) (tuples jsval.Value, ok bool)
}

// InDataProvider serves indata(name, field, value): the number of tuples of
// the dataset whose field equals value. ok is false when the dataset or its
// index does not exist (upstream then reads undefined).
type InDataProvider interface {
	InData(name, field string, value jsval.Value) (count int, ok bool)
}

// UnitIndexProvider exposes, for a selection store dataset, how many tuples
// each "unit" contributed. vlSelectionTest with the "intersect" operation needs
// it (upstream reads the dataset's `index:unit` index).
type UnitIndexProvider interface {
	// UnitCounts returns the tuple count per unit value and ok=false when the
	// dataset has no unit index.
	UnitCounts(name string) (counts map[string]int, ok bool)
}

// DataWriter serves the functions that change datasets.
type DataWriter interface {
	// SetData is setdata(name, tuples): replace the contents of a dataset.
	SetData(name string, tuples jsval.Value) jsval.Value
	// Modify is modify(name, insert, remove, toggle, modify, values).
	Modify(name string, insert, remove, toggle, modify, values jsval.Value) jsval.Value
}

// ScaleProvider serves the scale functions. ref is a scale name (a string) or,
// for `scale(copy('x'), v)`, whatever value copy returned; group is the
// optional trailing group-item argument (Undefined when absent). Results follow
// vega-functions: an unknown scale gives Undefined for Scale, Invert and Copy,
// an empty array for Domain and Range, and 0 for Bandwidth.
type ScaleProvider interface {
	Scale(ref, v, group jsval.Value) jsval.Value
	Invert(ref, v, group jsval.Value) jsval.Value
	Domain(ref, group jsval.Value) jsval.Value
	Range(ref, group jsval.Value) jsval.Value
	Bandwidth(ref, group jsval.Value) jsval.Value
	Copy(ref, group jsval.Value) jsval.Value
	// Gradient is gradient(scale, p0, p1, count, group).
	Gradient(ref, p0, p1, count, group jsval.Value) jsval.Value
}

// GeoProvider serves the projection functions. projection is a name or
// undefined (geoArea/geoBounds/geoCentroid then use the unprojected d3-geo
// functions).
type GeoProvider interface {
	GeoArea(projection, geojson, group jsval.Value) jsval.Value
	GeoBounds(projection, geojson, group jsval.Value) jsval.Value
	GeoCentroid(projection, geojson, group jsval.Value) jsval.Value
	GeoScale(projection, group jsval.Value) jsval.Value
	// GeoShape and PathShape return an opaque shape (a function of a path
	// context upstream) for the symbol/shape encodings.
	GeoShape(projection, geojson, group jsval.Value) jsval.Value
	PathShape(path jsval.Value) jsval.Value
}

// TreeProvider serves treePath and treeAncestors over a hierarchy dataset.
type TreeProvider interface {
	TreePath(name string, source, target jsval.Value) jsval.Value
	TreeAncestors(name string, node jsval.Value) jsval.Value
}

// ViewProvider serves the functions that reach the view, the scenegraph and
// the event that is being handled.
type ViewProvider interface {
	// EventFunction is event.vega.<name>(args...) for name in view, item,
	// group, xy, x, y.
	EventFunction(name string, args []jsval.Value) jsval.Value
	// ContainerSize is [clientWidth, clientHeight] of the view container.
	ContainerSize() jsval.Value
	// Screen is window.screen, WindowSize [innerWidth, innerHeight].
	Screen() jsval.Value
	WindowSize() jsval.Value
	// Encode is encode(item, name, retval); InScope is inScope(item);
	// Intersect is intersect(bounds, opt, group).
	Encode(item, name, retval jsval.Value) jsval.Value
	InScope(item jsval.Value) bool
	Intersect(bounds, opt, group jsval.Value) jsval.Value
	// IntersectLasso is intersectLasso(markname, pixelLasso, unit).
	IntersectLasso(markname, lasso, unit jsval.Value) jsval.Value
}

// LogLevel is the severity of a warn, info or debug call.
type LogLevel uint8

const (
	LogWarn LogLevel = iota + 1
	LogInfo
	LogDebug
)

// Logger serves warn(), info() and debug(). The expression functions return
// their last argument.
type Logger interface {
	Log(level LogLevel, args []jsval.Value)
}

// TupleChecker serves isTuple(v): whether v is a dataflow tuple.
type TupleChecker interface {
	IsTuple(v jsval.Value) bool
}
