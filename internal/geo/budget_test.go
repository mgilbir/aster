package geo

import (
	"context"
	"errors"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
	"github.com/mgilbir/aster/internal/jsval"
)

func TestPathPointBudget(t *testing.T) {
	coords := make([]jsval.Value, 0, 1000)
	for i := 0; i < 1000; i++ {
		coords = append(coords, jsval.Arr([]jsval.Value{jsval.Num(float64(i%300) - 150), jsval.Num(float64(i%100) - 50)}))
	}
	line := jsval.Obj(jsval.ObjectOf("type", jsval.Str("LineString"), "coordinates", jsval.Arr(coords)))
	ctx := budget.With(context.Background(), &budget.Budget{MaxPoints: 100})
	tuples := []jsval.Value{jsval.Obj(jsval.ObjectOf("g", line))}
	err := GeoPath(ctx, tuples, GeoPathParams{Field: func(v jsval.Value) jsval.Value { return v.Get("g") }})
	if !errors.Is(err, budget.ErrLimit) {
		t.Fatalf("1000 points against a budget of 100: %v", err)
	}
	ok := budget.With(context.Background(), &budget.Budget{MaxPoints: 100000})
	if err := GeoPath(ok, tuples, GeoPathParams{Field: func(v jsval.Value) jsval.Value { return v.Get("g") }}); err != nil {
		t.Fatal(err)
	}
}
