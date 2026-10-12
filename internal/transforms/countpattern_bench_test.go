package transforms

import (
	"context"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// BenchmarkCountPattern is a countpattern over a few short texts, which
// compiles its two patterns on every call.
func BenchmarkCountPattern(b *testing.B) {
	data := []jsval.Value{
		jsval.Obj(jsval.ObjectOf("t", jsval.Str("the quick brown fox jumps over the lazy dog"))),
		jsval.Obj(jsval.ObjectOf("t", jsval.Str("a stitch in time saves nine"))),
	}
	p := CountPatternParams{Field: FieldOf("t"), Stopwords: "(the|a|in|over)"}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := CountPattern(ctx, data, p); err != nil {
			b.Fatal(err)
		}
	}
}
