package svgpdf

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// TestParseTreeCharged: the element tree an SVG parses to, which its size
// chooses, is charged to the call's ledger as it is built (ADR 0010), by
// both parsers: the fast one Vega's markup takes, and encoding/xml, which a
// comment sends a document to.
func TestParseTreeCharged(t *testing.T) {
	body := strings.Repeat(`<rect width="1" height="1" fill="red"/>`, 20000)
	for name, svg := range map[string]string{
		"fast": `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">` + body + `</svg>`,
		"xml":  `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><!-- c -->` + body + `</svg>`,
	} {
		t.Run(name, func(t *testing.T) {
			// Room for the input's size, not for the tree it makes.
			l := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: int64(len(svg))})
			_, err := parseSVG(budget.WithLedger(context.Background(), l), svg, Limits{}.WithDefaults())
			if !errors.Is(err, budget.ErrLimit) {
				t.Errorf("a tree of 20,000 elements built under a %d byte ledger: %v", len(svg), err)
			}
			free := budget.NewLedger(context.Background(), budget.LedgerLimits{})
			if _, err := parseSVG(budget.WithLedger(context.Background(), free), svg, Limits{}.WithDefaults()); err != nil || free.Bytes() < 20000*64 {
				t.Errorf("tree charged %d bytes (err %v)", free.Bytes(), err)
			}
		})
	}
}
