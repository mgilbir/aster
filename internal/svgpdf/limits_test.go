package svgpdf

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/text"
)

func TestConvertContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">`)
	for i := 0; i < 5000; i++ {
		b.WriteString(`<rect width="1" height="1"/>`)
	}
	b.WriteString(`</svg>`)
	_, err := Convert(b.String(), nil, Options{Text: TextOutlines, Context: ctx})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestConvertLimits(t *testing.T) {
	head := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">`
	t.Run("elements", func(t *testing.T) {
		svg := head + strings.Repeat(`<g/>`, 50) + `</svg>`
		_, err := Convert(svg, nil, Options{Limits: Limits{MaxElements: 10}})
		if !errors.Is(err, ErrLimit) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("input", func(t *testing.T) {
		_, err := Convert(head+`</svg>`, nil, Options{Limits: Limits{MaxInputBytes: 20}})
		if !errors.Is(err, ErrLimit) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("path", func(t *testing.T) {
		svg := head + `<path d="M0,0` + strings.Repeat("L1,1", 100) + `"/></svg>`
		_, err := Convert(svg, nil, Options{Limits: Limits{MaxPathSegments: 50}})
		if !errors.Is(err, ErrLimit) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("clip-amplification", func(t *testing.T) {
		var b strings.Builder
		b.WriteString(head + `<defs><clipPath id="c">`)
		b.WriteString(strings.Repeat(`<rect width="1" height="1"/>`, 100))
		b.WriteString(`</clipPath></defs>`)
		for i := 0; i < 100; i++ {
			b.WriteString(`<rect width="1" height="1" clip-path="url(#c)"/>`)
		}
		b.WriteString(`</svg>`)
		_, err := Convert(b.String(), nil, Options{Limits: Limits{MaxPathSegments: 1000}})
		if !errors.Is(err, ErrLimit) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("ok", func(t *testing.T) {
		if _, err := Convert(head+`<path d="M0,0L5,5"/></svg>`, nil, Options{}); err != nil {
			t.Fatal(err)
		}
	})
}

type panicShaper struct{}

func (panicShaper) ShapeText(string, string) ([]text.Run, float64) { panic("boom") }
func (panicShaper) FontData(*text.Face) []byte                     { return nil }

func TestConvertRecoversPanics(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><text>hi</text></svg>`
	_, err := Convert(svg, panicShaper{}, Options{})
	if err == nil || !strings.Contains(err.Error(), "internal error") {
		t.Fatalf("got %v, want internal error", err)
	}
}

func TestConvertTextLimit(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><text>` + strings.Repeat("a", 100) + `</text></svg>`
	_, err := Convert(svg, panicShaper{}, Options{Limits: Limits{MaxTextBytes: 10}})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("got %v", err)
	}
}
