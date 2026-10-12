package aster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// An error says its cause once (X9): the stage of a call that timed out
// wrapped context.DeadlineExceeded in front of an error whose text was
// already the deadline's, and read "timed out after 1s: context deadline
// exceeded: context deadline exceeded".
func TestStageErrSaysTheCauseOnce(t *testing.T) {
	c := &Converter{cfg: &config{timeout: time.Second}}
	expired, cancel := context.WithDeadlineCause(context.Background(), time.Now().Add(-time.Second), errTimeout)
	defer cancel()
	for name, inner := range map[string]error{
		"the deadline itself":      context.DeadlineExceeded,
		"wrapped":                  fmt.Errorf("vega: %w", context.DeadlineExceeded),
		"the deadline's text":      errors.New("context deadline exceeded"),
		"an exception's text":      errors.New("RangeError: context deadline exceeded"),
		"an error of its own":      errors.New("boom"),
		"joined with the deadline": errors.Join(errors.New("boom"), context.DeadlineExceeded),
	} {
		err := c.stageErr(expired, "rendering Vega", inner)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s: %v does not wrap DeadlineExceeded", name, err)
		}
		if n := strings.Count(err.Error(), "deadline exceeded"); n > 1 {
			t.Errorf("%s: %q says the cause %d times", name, err, n)
		}
		if !strings.Contains(err.Error(), "timed out after 1s") {
			t.Errorf("%s: %q does not name the timeout", name, err)
		}
	}
}
