package transforms

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

// A group key is String(value), and String(date) is exact to the second: two
// dates within one second are one group, in the zone the view is in.
func TestDatesWithinASecondShareAKey(t *testing.T) {
	rows := []jsval.Value{
		obj("d", jsval.Timestamp(100)), obj("d", jsval.Timestamp(900)), obj("d", jsval.Timestamp(1100)),
	}
	d := FieldOf("d")
	out, err := Aggregate(context.Background(), rows, AggregateParams{GroupBy: []Field{d}})
	if err != nil || len(out) != 2 || out[0].Get("count").NumValue() != 2 {
		t.Errorf("aggregate: %v, %d groups", err, len(out))
	}
	if k := KeyOf(format.Zone{}, d); k(rows[0]) != k(rows[1]) || k(rows[0]) == k(rows[2]) {
		t.Errorf("keys %q %q %q", k(rows[0]), k(rows[1]), k(rows[2]))
	}
	if got := KeyOf(format.Zone{}, d)(rows[0]); got != "Thu Jan 01 1970 00:00:00 GMT+0000 (Coordinated Universal Time)" {
		t.Errorf("key %q", got)
	}
	// The same instant is another day's text in another zone.
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skip("no tz database")
	}
	loc := format.Local(tokyo)
	if got := KeyOf(loc, d)(rows[0]); !strings.HasPrefix(got, "Thu Jan 01 1970 09:00:00 GMT+0900 (") {
		t.Errorf("Tokyo key %q", got)
	}
	out, err = Aggregate(WithZone(context.Background(), loc), rows, AggregateParams{GroupBy: []Field{d}})
	if err != nil || len(out) != 2 {
		t.Errorf("aggregate in a zone: %v, %d groups", err, len(out))
	}
}
