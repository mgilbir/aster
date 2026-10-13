package format

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

// The longest members of d3's own locales (d3-format 3.1, d3-time-format
// 4.1): a locale built of them must be accepted.
func TestRealLocaleMembersFit(t *testing.T) {
	n := NumberLocaleDef{Decimal: ptr("٫"), Thousands: ptr("٬"), Currency: &[2]string{"\u200fFCFA ", " ف.ج.ق."},
		Percent: ptr(" %"), Minus: ptr("−"), Numerals: strings.Split("٠١٢٣٤٥٦٧٨٩", "")}
	if _, err := NewNumberLocale(n); err != nil {
		t.Error(err)
	}
	days := []string{"воскресенье", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота"}
	months := []string{"كانون الثاني", "شباط", "آذار", "نيسان", "أيار", "حزيران", "تموز", "آب", "أيلول", "تشرين الأول", "تشرين الثاني", "كانون الأول"}
	if _, err := NewTimeLocale(TimeLocaleDef{DateTime: "%A, %-d. %Bta %Y klo %X", Date: "%Y年%-m月%-d日", Time: "%-I:%M:%S %p",
		Periods: []string{"صبح", "عصر"}, Days: days, ShortDays: days, Months: months, ShortMonths: months}); err != nil {
		t.Error(err)
	}
}

// TestD3LocalesFit loads every locale d3 ships, where the oracle's modules
// are installed.
func TestD3LocalesFit(t *testing.T) {
	nm := filepath.Join("..", "..", "testdata", "oracle-node", "node_modules")
	for _, dir := range []string{"d3-format", "d3-time-format"} {
		files, _ := filepath.Glob(filepath.Join(nm, dir, "locale", "*.json"))
		if len(files) == 0 {
			t.Skip("oracle node_modules not installed")
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			v, err := jsval.ParseJSON(b)
			if err != nil {
				t.Fatal(err)
			}
			if dir == "d3-format" {
				_, err = NumberLocaleFromValue(v)
			} else {
				_, err = TimeLocaleFromValue(v)
			}
			if err != nil {
				t.Errorf("%s: %v", filepath.Base(f), err)
			}
		}
	}
}

// TestLocaleMembersBounded is C9: a member that would amplify every label
// is refused when the locale is built, with a limit error.
func TestLocaleMembersBounded(t *testing.T) {
	long := strings.Repeat("0", 1024)
	for name, def := range map[string]NumberLocaleDef{
		"thousands": {Thousands: ptr(long), Grouping: []float64{1}},
		"decimal":   {Decimal: ptr(strings.Repeat(".", MaxLocaleSymbol+1))},
		"numeral":   {Numerals: []string{"0", "1", strings.Repeat("9", 64)}},
		"currency":  {Currency: &[2]string{"", strings.Repeat("$", MaxLocaleText+1)}},
		"nan":       {NaN: ptr(long)},
	} {
		if _, err := NewNumberLocale(def); !errors.Is(err, budget.ErrLimit) {
			t.Errorf("%s: %v; want a limit error", name, err)
		}
	}
	base := func() TimeLocaleDef {
		d := defaultTimeLocale.def
		return d
	}
	for name, edit := range map[string]func(*TimeLocaleDef){
		"dateTime":  func(d *TimeLocaleDef) { d.DateTime = strings.Repeat("%x", 4095) },
		"name":      func(d *TimeLocaleDef) { d.Months = append([]string{long}, d.Months[1:]...) },
		"names":     func(d *TimeLocaleDef) { d.Days = make([]string, MaxLocaleNames+1) },
		"expansion": func(d *TimeLocaleDef) { d.DateTime = strings.Repeat("%x", 100); d.Date = strings.Repeat("a", 200) },
	} {
		d := base()
		edit(&d)
		if _, err := NewTimeLocale(d); !errors.Is(err, budget.ErrLimit) {
			t.Errorf("%s: %v; want a limit error", name, err)
		}
	}
	// A locale that only names %c in a cycle, with nothing to write, is
	// harmless and kept (d3 overflows its stack only when %c is used).
	d := base()
	d.DateTime = "%c"
	if _, err := NewTimeLocale(d); err != nil {
		t.Errorf("empty cycle: %v", err)
	}
}

// TestFormatMemosBoundedByBytes is X4: the memo tables of a Locale are keyed
// by specifiers of any length (a format string per row) and keep at most
// formatMemoBytes each, however long the keys.
func TestFormatMemosBoundedByBytes(t *testing.T) {
	l := DefaultLocale()
	pad := strings.Repeat("x", 16<<10)
	for i := 0; i < 1500; i++ {
		spec := pad + string(rune('a'+i%26)) + strings.Repeat("y", i)
		l.TimeFormat(spec)
		l.UTCParse(spec)
	}
	if b := l.timeFmt.Bytes(); b > formatMemoBytes {
		t.Errorf("time format memo holds %d bytes, more than %d", b, formatMemoBytes)
	}
	if b := l.parsers.Bytes(); b > formatMemoBytes {
		t.Errorf("time parser memo holds %d bytes, more than %d", b, formatMemoBytes)
	}
	// and a memo still memoises
	if a, b := l.TimeFormat("%Y"), l.TimeFormat("%Y"); a != b {
		t.Error("the memo returned two formats for one specifier")
	}
}

func BenchmarkLocaleFormatMemo(b *testing.B) {
	l := DefaultLocale()
	b.ReportAllocs()
	for b.Loop() {
		f, _ := l.NumberFormat(",.2f")
		_ = f
		_ = l.TimeFormat("%Y-%m-%d")
	}
}
