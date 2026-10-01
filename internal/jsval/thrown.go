package jsval

// Thrown is the panic value of a JavaScript exception that upstream throws
// inside a function the engine models without an error result (a field
// accessor, a scale). The dataflow recovers it where an operator or a
// post-run callback runs and logs it as an error the specification caused, as
// upstream's does when an operator throws.
type Thrown struct {
	Name string // "TypeError", "RangeError", ...; empty when not modelled
	Msg  string
}

func (e *Thrown) Error() string {
	if e.Name == "" {
		return e.Msg
	}
	return e.Name + ": " + e.Msg
}
