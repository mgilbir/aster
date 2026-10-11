package aster_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

// docs/limits.md's budget table is the documentation of WithMemoryLimit: every
// row's default and formula must be what the code does.
func TestLimitsDocMatchesCode(t *testing.T) {
	b, err := os.ReadFile("docs/limits.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	i, j := strings.Index(doc, "<!-- limits:begin"), strings.Index(doc, "<!-- limits:end")
	if i < 0 || j < i {
		t.Fatal("docs/limits.md has no limits:begin/end block")
	}
	defaults := aster.LimitsForTest(0)
	seen := map[string]bool{}
	formula := regexp.MustCompile(`^n(?:/(\d+))?(?:, at least (.+)|, between (.+) and (.+))?$`)
	for _, line := range strings.Split(doc[i:j], "\n") {
		cells := strings.Split(line, "|")
		if len(cells) != 5 || strings.HasPrefix(strings.TrimSpace(cells[1]), "Budget") || strings.HasPrefix(strings.TrimSpace(cells[1]), "---") {
			continue
		}
		name := strings.TrimSpace(cells[1])
		def, ok := defaults[name]
		if !ok {
			t.Errorf("row %q is not a budget the test knows", name)
			continue
		}
		seen[name] = true
		if got := quantity(t, strings.TrimSpace(cells[2])); got != def {
			t.Errorf("%s: documented default %s = %d, code %d", name, strings.TrimSpace(cells[2]), got, def)
		}
		m := formula.FindStringSubmatch(strings.Trim(strings.TrimSpace(cells[3]), "`"))
		if m == nil {
			t.Errorf("%s: formula %q not understood", name, cells[3])
			continue
		}
		// Up to 4 GiB, where no bound passes what a 32-bit int holds (past
		// it, the documented saturation takes over; see docs/limits.md).
		for _, n := range []uint64{1 << 10, 3 << 20, 64 << 20, 1 << 30, 4 << 30} {
			want := int64(n)
			if m[1] != "" {
				d, _ := strconv.ParseInt(m[1], 10, 64)
				want = int64(n) / d
			}
			if m[2] != "" {
				want = max(want, quantity(t, m[2]))
			}
			if m[3] != "" {
				want = min(max(want, quantity(t, m[3])), quantity(t, m[4]))
			}
			if got := aster.LimitsForTest(n)[name]; got != want {
				t.Errorf("%s with WithMemoryLimit(%d): documented %d, code %d", name, n, want, got)
			}
		}
	}
	for name := range defaults {
		if !seen[name] {
			t.Errorf("budget %q is missing from docs/limits.md", name)
		}
	}
}

// quantity reads "1,000,000", "64 MiB", "1 GiB", "64 Mi", "512 Ki" or "none" (0).
func quantity(t *testing.T, s string) int64 {
	t.Helper()
	if s == "none" {
		return 0
	}
	mult := int64(1)
	for suffix, m := range map[string]int64{" KiB": 1 << 10, " MiB": 1 << 20, " GiB": 1 << 30, " Ki": 1 << 10, " Mi": 1 << 20, " Gi": 1 << 30} {
		if strings.HasSuffix(s, suffix) {
			s, mult = strings.TrimSuffix(s, suffix), m
			break
		}
	}
	v, err := strconv.ParseInt(strings.ReplaceAll(s, ",", ""), 10, 64)
	if err != nil {
		t.Fatalf("quantity %q: %v", s, err)
	}
	return v * mult
}
