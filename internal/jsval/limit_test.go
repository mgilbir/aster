package jsval

import (
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// A document of tiny values stops at the memory limit, long before its text is
// read to the end, and one that fits parses as ParseJSON does.
func TestParseJSONLimit(t *testing.T) {
	docs := map[string]string{
		"rows":    "[" + strings.Repeat(`{"a":1},`, 100_000) + `{"a":1}]`,
		"numbers": "[" + strings.Repeat("1,", 100_000) + "1]",
		"empties": "[" + strings.Repeat("{},", 100_000) + "{}]",
		"strings": "[" + strings.Repeat(`"abcdefghij",`, 100_000) + `"x"]`,
		"keys":    `{"` + strings.Repeat("k", 1<<20) + `":1}`,
	}
	for name, doc := range docs {
		want, err := ParseJSON([]byte(doc))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := ParseJSONLimit([]byte(doc), int64(len(doc))); !errors.Is(err, ErrJSONSize) || !errors.Is(err, budget.ErrLimit) {
			t.Errorf("%s: a limit of the text size: got %v, want ErrJSONSize", name, err)
		}
		got, err := ParseJSONLimit([]byte(doc), 1<<30)
		if err != nil {
			t.Fatalf("%s: roomy limit: %v", name, err)
		}
		if !Equal(got, want) {
			t.Errorf("%s: limited parse differs", name)
		}
	}
}
