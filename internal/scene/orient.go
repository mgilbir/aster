package scene

import "github.com/mgilbir/aster/internal/jsval"

// keepRawOrient keeps an orient given as an array or an object in Raw: its
// text (Orient) can spell an orient name, ["left"] reads "left", where
// upstream's layout compares the value itself with ===, which no array or
// object passes (OrientValue).
func (it *Item) keepRawOrient(val jsval.Value) {
	if it.Raw != nil {
		delete(it.Raw, "orient")
	}
	if val.IsArr() || val.IsObj() {
		if it.Raw == nil {
			it.Raw = make(map[string]jsval.Value, 1)
		}
		it.Raw["orient"] = val
	}
}

// OrientValue is item.orient as upstream's layout reads it: the array or
// object it was given, else its text, and undefined when unset or falsy.
func (it *Item) OrientValue() jsval.Value {
	if v, ok := it.Raw["orient"]; ok {
		return v
	}
	if o := it.Orient(); o != "" {
		return jsval.Str(o)
	}
	return jsval.Undefined
}
