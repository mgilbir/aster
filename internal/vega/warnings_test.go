package vega

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// collect renders spec and returns what Options.Report was told of.
func collect(t *testing.T, spec string) []error {
	t.Helper()
	var got []error
	_, err := Render(context.Background(), mustJSON(t, spec), Options{Report: func(err error) error {
		got = append(got, err)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Warnings carry text from the specification (a property name, a field, an
// expression's arguments) and repeat in every facet cell. A render reports
// each distinct warning once, cut to a bounded length, and stops reporting
// past a bounded number of them.
func TestWarningsAreBounded(t *testing.T) {
	cells := func(n int, scale string) string {
		return fmt.Sprintf(`{"data":[{"name":"t","transform":[{"type":"sequence","start":0,"stop":%d}]}],
		  "marks":[{"type":"group","from":{"facet":{"name":"f","data":"t","groupby":"data"}},
		    "scales":[%s],"marks":[{"type":"rect","from":{"data":"f"}}]}]}`, n, scale)
	}
	t.Run("a long warning repeated in every cell", func(t *testing.T) {
		long := strings.Repeat("p", 100_000)
		got := collect(t, cells(500, `{"name":"x","type":"linear","domain":[0,1],"range":[0,1],"`+long+`":1}`))
		var w Warning
		if len(got) != 1 || !errors.As(got[0], &w) {
			t.Fatalf("reported %d, want the warning once", len(got))
		}
		if len(w) > MaxWarningText+len("…") || !strings.HasPrefix(string(w), "Unsupported scale property: ppp") || !strings.HasSuffix(string(w), "…") {
			t.Errorf("warning of %d bytes %.60q…, want it cut to %d", len(w), w, MaxWarningText)
		}
	})
	t.Run("a different warning in every cell", func(t *testing.T) {
		got := collect(t, cells(2000, `{"name":"x","type":"linear","domain":[0,1],"range":[0,{"signal":"warn('cell', parent.data)"}]}`))
		if len(got) != maxWarnings+1 {
			t.Fatalf("reported %d, want %d and the one that says the rest are dropped", len(got), maxWarnings+1)
		}
		if last := got[len(got)-1].Error(); !strings.Contains(last, "further warnings are not reported") {
			t.Errorf("last warning %q", last)
		}
	})
}

// Expression warn() is a warning, as upstream's logs it (view.warn), with
// the arguments printed after "EXPRESSION"; info() and debug() are below
// the warning level, where upstream's logger drops them too. Each returns its
// last argument.
func TestExpressionWarnIsReported(t *testing.T) {
	got := collect(t, `{"signals":[
	  {"name":"a","update":"warn('careful', 1, [2, [3, null]], {k: 1})"},
	  {"name":"b","update":"info('note', 2) + debug('detail', 3)"}],
	  "marks":[{"type":"text","encode":{"enter":{"text":{"signal":"a + '/' + b"}}}}]}`)
	var w Warning
	if len(got) != 1 || !errors.As(got[0], &w) {
		t.Fatalf("reported %v, want the one warning", got)
	}
	if want := "EXPRESSION careful 1 2,3, [object Object]"; string(w) != want {
		t.Errorf("warning %q, want %q", w, want)
	}
}

// renderReporting renders spec with the test loader and returns the messages
// Options.Report was told of, going on past each.
func renderReporting(t testing.TB, spec string) (*Result, []string, error) {
	t.Helper()
	var msgs []string
	res, err := Render(context.Background(), mustJSON(t, spec), Options{Loader: newTestLoader(), Report: func(err error) error {
		msgs = append(msgs, err.Error())
		return nil
	}})
	return res, msgs, err
}

// mustRenderReporting is renderReporting for a spec that must render.
func mustRenderReporting(t *testing.T, spec string) (*Result, []string) {
	t.Helper()
	res, msgs, err := renderReporting(t, spec)
	if err != nil {
		t.Fatal(err)
	}
	return res, msgs
}

func hasWarning(msgs []string, sub string) bool {
	for _, w := range msgs {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

// Scale's domainMid warning carries the value after the text, as upstream's
// df.warn('Scale domainMid exceeds domain min or max.', mid) logs it.
func TestDomainMidWarningCarriesTheValue(t *testing.T) {
	for _, mid := range []string{"5", "-3", "2.5", `"7"`} {
		got := collect(t, `{"scales":[{"name":"x","type":"linear","domain":[0,1],"domainMid":`+mid+`,"range":[0,1]}],
		  "marks":[{"type":"rect"}]}`)
		var w Warning
		if len(got) != 1 || !errors.As(got[0], &w) {
			t.Fatalf("mid %s: reported %v, want one warning", mid, got)
		}
		want := "Scale domainMid exceeds domain min or max. " + strings.Trim(mid, `"`)
		if string(w) != want {
			t.Errorf("mid %s: warning %q, want %q", mid, w, want)
		}
	}
}
