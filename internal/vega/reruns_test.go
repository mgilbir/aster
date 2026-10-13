package vega

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// What upstream loops on is bounded where it loops: a data set whose triggers
// keep changing it, within one run, and the view's size not settling within
// one run. Independent data sets and separate signal writes each have their
// own allowance; none is spent by another.
func TestRerunsAreBoundedWhereTheyLoop(t *testing.T) {
	trigger := func(name, trig, insert string) string {
		return fmt.Sprintf(`{"name":%q,"values":[{"a":1}],"on":[{"trigger":%q,"insert":%q}]}`, name, trig, insert)
	}
	settles := map[string]struct {
		spec   string
		writes []SignalWrite
	}{
		"independent data triggers": {spec: func() string {
			var defs []string
			for i := range 40 {
				defs = append(defs, trigger(fmt.Sprintf("t%d", i), "true", "{a:2}"))
			}
			return dataSets(defs...)
		}()},
		// upstream changes the i-th data set of the chain i times
		"a chain of data triggers": {spec: func() string {
			defs := []string{`{"name":"d0","values":[{"a":1}]}`}
			for i := 1; i <= 12; i++ {
				defs = append(defs, trigger(fmt.Sprintf("d%d", i), fmt.Sprintf("length(data('d%d'))", i-1), "{a:2}"))
			}
			return dataSets(defs...)
		}()},
		// fit sizes the plot to what the axis labels leave, so each write
		// makes the layout set the width again
		"signal writes that each resize the view": {
			spec: `{"autosize":{"type":"fit","contains":"padding","resize":true},"width":200,"height":100,
				"signals":[{"name":"m","value":1}],
				"scales":[{"name":"y","type":"linear","domain":[0,{"signal":"m"}],"range":"height","nice":false,"zero":false}],
				"axes":[{"orient":"left","scale":"y"}]}`,
			writes: func() []SignalWrite {
				var w []SignalWrite
				for i := range 40 {
					w = append(w, SignalWrite{Name: "m", Value: jsval.Num(math.Pow(10, float64(i%12)))})
				}
				return w
			}(),
		},
	}
	for name, c := range settles {
		t.Run(name, func(t *testing.T) {
			if _, err := Render(context.Background(), mustJSON(t, c.spec), Options{SignalWrites: c.writes}); err != nil {
				t.Fatal(err)
			}
		})
	}
	loops := map[string]string{
		// the trigger reads a signal its own change updates: upstream never
		// returns
		"a data set whose change fires its trigger": `{"signals":[{"name":"s","update":"length(data('d'))"}],
			"data":[` + trigger("d", "s", "{a:2}") + `]}`,
		"two data sets whose changes fire each other's triggers": `{"signals":[
			{"name":"sa","update":"length(data('a'))"},{"name":"sb","update":"length(data('b'))"}],
			"data":[` + trigger("a", "sb", "{a:2}") + `,` + trigger("b", "sa", "{a:2}") + `]}`,
		"a chain of triggers nested deeper than the bound": func() string {
			// each link changes the next once, from inside the run of its
			// own change
			defs := []string{`{"name":"d0","values":[{"a":1}]}`}
			for i := 1; i <= maxRunDepth+8; i++ {
				defs = append(defs, trigger(fmt.Sprintf("d%d", i), fmt.Sprintf("length(data('d%d')) > 1", i-1), "{a:2}"))
			}
			defs[0] = trigger("d0", "true", "{a:2}")
			return dataSets(defs...)
		}(),
	}
	for name, spec := range loops {
		t.Run(name, func(t *testing.T) {
			_, err := Render(context.Background(), mustJSON(t, spec), Options{})
			if !errors.Is(err, budget.ErrLimit) {
				t.Fatalf("err = %v, want the limit", err)
			}
		})
	}
}

// The audit's reproducer: twenty data sets, each with a trigger that inserts
// once, render with the default limits.
func TestTwentyIndependentTriggers(t *testing.T) {
	b, err := os.ReadFile("testdata/trig.vg.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render(context.Background(), mustJSON(t, string(b)), Options{}); err != nil {
		t.Fatal(err)
	}
}

// A change set is applied as it was made, even when a later run starts
// another one for the same data set before it is: d2's trigger fires in the
// first run and again when d1 changes, and both inserts land. Upstream (node)
// draws "2/3".
func TestEveryTriggerChangeIsApplied(t *testing.T) {
	res := renderSpec(t, `{"width":10,"height":10,"data":[
	  {"name":"d0","values":[{"a":1}]},
	  {"name":"d1","values":[{"a":1}],"on":[{"trigger":"length(data('d0'))","insert":"{a:2}"}]},
	  {"name":"d2","values":[{"a":1}],"on":[{"trigger":"length(data('d1'))","insert":"{a:2}"}]}],
	  "marks":[{"type":"text","encode":{"update":{"text":{"signal":"length(data('d1'))+'/'+length(data('d2'))"}}}}]}`)
	if got := marks(res)[0].Items[0].Text().AsString(); got != "2/3" {
		t.Errorf("text %q, want 2/3", got)
	}
}
