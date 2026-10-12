package expr

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// TestFormattedStringsCharged is X14: the strings format(), timeFormat() and
// utcFormat() write are charged to the string budget like any other the
// expressions build, and a time format stops once it would pass it: a
// specifier of n bytes can ask for thousands of times as much (each %c a
// locale's date and time).
func TestFormattedStringsCharged(t *testing.T) {
	run := func(src string, room int64) error {
		p, err := Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		s := NewScope(nil)
		s.Context = context.Background()
		s.Strings = NewStringBudget(room)
		_, err = p.Eval(s)
		return err
	}
	for _, src := range []string{
		"format(1, 'é>65536')",
		"timeFormat(0, '" + strings.Repeat("%c", 1000) + "')",
		"utcFormat(0, '" + strings.Repeat("%c", 1000) + "')",
		"[format(1, '>9000'), format(2, '>9000')]",
	} {
		if err := run(src, 16<<10); !errors.Is(err, budget.ErrLimit) {
			t.Errorf("%.40s… under a 16 KiB budget: %v; want a limit error", src, err)
		}
		if err := run(src, 64<<20); err != nil {
			t.Errorf("%.40s… under a 64 MiB budget: %v", src, err)
		}
	}
}
