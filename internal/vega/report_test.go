package vega

import (
	"context"
	"errors"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// Options.Report sees what upstream logs and carries on past, typed, and
// decides whether the render goes on.
func TestReportDecidesWhetherTheRenderGoesOn(t *testing.T) {
	const failedLoad = `{"data":[{"name":"d","url":"https://example.com/x.json"}],"marks":[{"type":"rect","from":{"data":"d"}}]}`
	const exception = `{"signals":[{"name":"s","update":"datum"}]}`
	const warning = `{"scales":[{"name":"x","type":"log","domain":[0,10],"range":[0,100]}]}`
	render := func(spec string, report func(error) error) (*Result, error) {
		t.Helper()
		v, err := jsval.ParseJSONString(spec)
		if err != nil {
			t.Fatal(err)
		}
		return Render(context.Background(), v, Options{Report: report})
	}

	t.Run("a failed load goes on as upstream when Report says so", func(t *testing.T) {
		var got []error
		res, err := render(failedLoad, func(err error) error { got = append(got, err); return nil })
		if err != nil {
			t.Fatal(err)
		}
		var le *LoadError
		if len(got) != 1 || !errors.As(got[0], &le) || le.URL != "https://example.com/x.json" || le.Parse {
			t.Fatalf("reported %v, want one *LoadError for the url", got)
		}
		if len(res.Warnings) != 1 || res.Warnings[0] != got[0].Error() {
			t.Errorf("Warnings %q, want the reported message", res.Warnings)
		}
	})

	t.Run("a failed load ends the render with Report's error", func(t *testing.T) {
		stop := errors.New("stop")
		calls := 0
		_, err := render(failedLoad, func(error) error { calls++; return stop })
		if err != stop {
			t.Fatalf("err = %v, want Report's error itself", err)
		}
		if calls != 1 {
			t.Errorf("Report called %d times, want once (the stop is not reported again)", calls)
		}
	})

	// Each stops on what it is told of first, and must be told of it once.
	stopOnFirst := func(spec string) (seen []error, err error) {
		_, err = render(spec, func(err error) error { seen = append(seen, err); return err })
		return seen, err
	}

	t.Run("an exception ends the render with Report's error", func(t *testing.T) {
		seen, err := stopOnFirst(exception)
		if len(seen) != 1 || err != seen[0] {
			t.Fatalf("err = %v, reported %v: want the one reported exception", err, seen)
		}
	})

	t.Run("a warning raised inside an operator is a Warning and can end the render", func(t *testing.T) {
		seen, err := stopOnFirst(warning)
		var w Warning
		if len(seen) != 1 || !errors.As(seen[0], &w) {
			t.Fatalf("reported %v, want one Warning", seen)
		}
		if err != seen[0] {
			t.Fatalf("err = %v, want the warning", err)
		}
	})

	t.Run("without Report the render goes on", func(t *testing.T) {
		res, err := render(failedLoad, nil)
		if err != nil || len(res.Warnings) != 1 {
			t.Fatalf("err %v, warnings %q", err, res.Warnings)
		}
	})
}
