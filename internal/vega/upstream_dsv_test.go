package vega

import (
	"testing"

	"github.com/mgilbir/aster/internal/upstream"
)

// TestUpstreamD3DsvParse replays d3-dsv's own parse tests (see internal/upstream) against the engine's
// delimiter-separated reader. Its formatters and autoType are not part of the engine.
func TestUpstreamD3DsvParse(t *testing.T) {
	r := upstream.Start(t, "d3-dsv")
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			r.Skip("oversized")
			continue
		}
		var delim byte
		var text any
		switch {
		case c.Fn == "dsvFormat()" && c.Method == "parse" && len(c.Args) == 1:
			d, ok := c.ConstructedWith[0].(string)
			if !ok || len(d) != 1 {
				r.Skip("delimiters of another shape")
				continue
			}
			delim, text = d[0], c.Args[0]
		case c.Fn == "tsvParse" && len(c.Args) == 1:
			delim, text = '\t', c.Args[0]
		case c.Fn == "csvParse" && len(c.Args) == 1:
			delim, text = ',', c.Args[0]
		default:
			r.Skip("not a parse the engine has (formatters, parseRows, autoType, row functions)")
			continue
		}
		s, ok := text.(string)
		if !ok {
			r.Skip("input that is not a string")
			continue
		}
		rows, _ := parseDSV(s, delim)
		got := make([]any, len(rows))
		for j, row := range rows {
			got[j] = upstream.FromValue(row)
		}
		r.Check(c, got, false)
	}
	r.Done(50)
}
