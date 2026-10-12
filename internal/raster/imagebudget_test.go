package raster

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// An image is decoded once per distinct href, however many elements draw
// it, and what it decodes to is charged to the call's ledger before it is
// decoded, and given back when the render ends (C7). A broken image is
// reported once.
func TestImageDecodedOncePerHref(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 512, 512))); err != nil {
		t.Fatal(err)
	}
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
	var svg strings.Builder
	svg.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="20">`)
	for i := range 8 {
		fmt.Fprintf(&svg, `<image x="%d" width="20" height="20" href=" %s"/><image x="%d" width="5" height="5" href="data:image/png;base64,AAAA"/>`, 20*i, uri, 20*i)
	}
	svg.WriteString(`</svg>`)
	l := budget.NewLedger(context.Background(), budget.LedgerLimits{})
	var reported []string
	_, err := Render([]byte(svg.String()), Options{
		Context:    budget.WithLedger(context.Background(), l),
		ImageError: func(href string, err error) { reported = append(reported, href) },
	})
	if err != nil {
		t.Fatal(err)
	}
	one := imageBytes(512, 512)
	if l.PeakBytes() < one || l.PeakBytes() >= 2*one {
		t.Errorf("ledger peak %d bytes; one image is charged %d, and it is decoded once", l.PeakBytes(), one)
	}
	if l.Bytes() != 0 {
		t.Errorf("%d bytes left charged after the render", l.Bytes())
	}
	if len(reported) != 1 {
		t.Errorf("the broken image is reported %d times, want once: %q", len(reported), reported)
	}
	// A ledger that cannot hold the image fails the render with ErrLimit,
	// before decoding it.
	small := budget.NewLedger(context.Background(), budget.LedgerLimits{MaxBytes: one / 2})
	if _, err := Render([]byte(svg.String()), Options{Context: budget.WithLedger(context.Background(), small)}); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("an image over the ledger: err = %v", err)
	}
}
