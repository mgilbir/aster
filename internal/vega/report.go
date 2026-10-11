package vega

// Upstream's dataflow logs some errors and carries on: a load that fails
// (Dataflow.request), an operator that throws (Dataflow.evaluate's catch
// clause), a post-run callback that throws (asyncCallback). It also logs
// warnings (view.warn). Each is reported through Options.Report, which decides
// whether the render goes on; without one it always does, as upstream's.

// Warning is a message upstream logs with view.warn. It does not end a render
// unless Options.Report says so.
type Warning string

func (w Warning) Error() string { return string(w) }

// LoadError is a data load that failed: a url the Loader refused or could
// not load, or a body that does not parse in its format. Upstream logs it and
// goes on with an empty data set (or, for a body that does not parse, the text
// as one tuple).
type LoadError struct {
	URL   string // as the specification names it; "" when it names none
	Parse bool   // the body loaded but does not parse
	Err   error
}

// Error is upstream's log message.
func (e *LoadError) Error() string {
	switch {
	case e.Parse:
		return "Data ingestion failed " + e.URL + ": " + e.Err.Error()
	case e.URL == "":
		return "Loading failed: " + e.Err.Error()
	}
	return "Loading failed " + e.URL + ": " + e.Err.Error()
}

func (e *LoadError) Unwrap() error { return e.Err }

// stopped is the error Options.Report returned to end the render. It is
// fatal, so the dataflow does not report it again, and Render returns what
// it wraps.
type stopped struct{ err error }

func (s *stopped) Error() string { return s.err.Error() }
func (s *stopped) Unwrap() error { return s.err }

// report records err (a Warning or an error the dataflow carries on past)
// and asks Options.Report whether to go on: nil goes on, a *stopped ends the
// render.
func (g *flowGraph) report(err error) error {
	g.warnings = append(g.warnings, err.Error())
	if g.onReport == nil {
		return nil
	}
	if e := g.onReport(err); e != nil {
		return &stopped{e}
	}
	return nil
}

// warn reports a warning, ending the render from inside an operator when
// Options.Report says so.
func (g *flowGraph) warn(msg string) {
	if err := g.report(Warning(msg)); err != nil {
		panic(&opError{err})
	}
}

// unstopped is the error Options.Report returned when err is one it ended the
// render with, and err otherwise.
func unstopped(err error) error {
	if s, ok := err.(*stopped); ok {
		return s.err
	}
	return err
}
