package aster_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/testutil"
)

// funcLoader is a Loader made of two functions.
type funcLoader struct {
	sanitize func(ctx context.Context, uri string) (string, error)
	load     func(ctx context.Context, uri string) ([]byte, error)
}

func (l funcLoader) Sanitize(ctx context.Context, uri string) (string, error) {
	return l.sanitize(ctx, uri)
}
func (l funcLoader) Load(ctx context.Context, uri string) ([]byte, error) { return l.load(ctx, uri) }

// imageSpec is a Vega chart of one image mark at url, sized or not.
func imageSpec(url string, sized bool) []byte {
	size := ""
	if sized {
		size = `,"width":{"value":16},"height":{"value":16}`
	}
	return []byte(`{"width":40,"height":40,"marks":[{"type":"image","encode":{"enter":{"url":{"value":"` + url + `"}` + size + `}}}]}`)
}

// A limit ends the call in every output (C34): an image over the per-image
// limit, read for the size of a mark that has none, failed PNG with ErrLimit
// but vanished from the SVG with no error and no report.
func TestImageOverLimitFailsEveryOutput(t *testing.T) {
	big := append(redPNG(t), make([]byte, 40<<20)...) // a PNG, then 40 MiB
	l := funcLoader{
		sanitize: func(_ context.Context, uri string) (string, error) { return uri, nil },
		load:     func(context.Context, string) ([]byte, error) { return big, nil }, // ignores the cap it is given
	}
	c, err := aster.New(aster.WithLoader(l))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	reported := 0
	h := aster.WithErrorHandler(func(error) error { reported++; return nil })
	spec := imageSpec("big.png", false)
	_, err = c.VegaToSVG(spec, h)
	if !errors.Is(err, aster.ErrLimit) {
		t.Errorf("SVG: err = %v, want ErrLimit", err)
	}
	if _, err := c.VegaToPNG(spec, h); !errors.Is(err, aster.ErrLimit) {
		t.Errorf("PNG: err = %v, want ErrLimit", err)
	}
	if _, err := c.VegaToPDF(spec, h); !errors.Is(err, aster.ErrLimit) {
		t.Errorf("PDF: err = %v, want ErrLimit", err)
	}
	if reported != 0 {
		t.Errorf("the handler was told of a limit %d times; a limit is not its to let go", reported)
	}
}

// countingLoader rewrites a logical image name to its address, as a CDN or an
// asset table does, and serves only addresses: it counts what each method was
// called with.
type countingLoader struct {
	png      []byte
	fail     bool
	mu       sync.Mutex
	sanitize map[string]int
	load     map[string]int
}

func (l *countingLoader) Sanitize(_ context.Context, uri string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sanitize[uri]++
	if strings.Contains(uri, "://") {
		return "", fmt.Errorf("only logical names, not %q", uri)
	}
	return "https://cdn.example/" + uri, nil
}

func (l *countingLoader) Load(_ context.Context, uri string) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.load[uri]++
	if l.fail || !strings.HasPrefix(uri, "https://cdn.example/") || strings.Count(uri, "://") != 1 {
		return nil, errors.New("not found")
	}
	return l.png, nil
}

func (l *countingLoader) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sanitize, l.load = map[string]int{}, map[string]int{}
}

// Each image URL goes through the Loader once per call, whichever outputs
// need it (C35): PNG and PDF read the SVG back and sanitized its URL a second
// time, so a rewriting Loader saw its own answer, a Loader of logical names
// refused it, and the image the SVG drew was left out of PNG and PDF.
func TestImagesResolvedOncePerCall(t *testing.T) {
	l := &countingLoader{png: redPNG(t)}
	c, err := aster.New(aster.WithLoader(l))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	type output struct {
		name   string
		render func(spec []byte) ([]byte, error)
		drawn  func(t *testing.T, out []byte)
	}
	outputs := []output{
		{"SVG", func(s []byte) ([]byte, error) { svg, err := c.VegaToSVG(s); return []byte(svg), err }, func(t *testing.T, out []byte) {
			if !bytes.Contains(out, []byte(`href="https://cdn.example/x.png"`)) {
				t.Errorf("the SVG does not draw the resolved URL:\n%s", out)
			}
		}},
		{"PNG", func(s []byte) ([]byte, error) { return c.VegaToPNG(s) }, func(t *testing.T, out []byte) {
			img, err := png.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatal(err)
			}
			if n := countReddish(img); n < 200 {
				t.Errorf("the PNG drew %d red pixels of the image", n)
			}
		}},
		{"PDF", func(s []byte) ([]byte, error) { return c.VegaToPDF(s) }, func(t *testing.T, out []byte) {
			if !bytes.Contains(out, []byte("/Subtype /Image")) {
				t.Error("the PDF has no image")
			}
		}},
	}
	for _, o := range outputs {
		for _, sized := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/sized=%v", o.name, sized), func(t *testing.T) {
				l.reset()
				out, err := o.render(imageSpec("x.png", sized))
				if err != nil {
					t.Fatal(err)
				}
				o.drawn(t, out)
				l.mu.Lock()
				defer l.mu.Unlock()
				if len(l.sanitize) != 1 || l.sanitize["x.png"] != 1 {
					t.Errorf("Sanitize calls %v, want x.png once", l.sanitize)
				}
				wantLoads := 1
				if o.name == "SVG" && sized {
					wantLoads = 0 // nothing needs the bytes
				}
				if n := l.load["https://cdn.example/x.png"]; n != wantLoads || len(l.load) > wantLoads {
					t.Errorf("Load calls %v, want https://cdn.example/x.png %d times", l.load, wantLoads)
				}
			})
		}
	}
	// SVG given to SVGToPNG names the image itself: it is sanitized once.
	l.reset()
	out, err := c.SVGToPNG(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="40" height="40"><image xlink:href="x.png" width="16" height="16"/><image xlink:href="x.png" x="20" width="16" height="16"/></svg>`)
	if err != nil {
		t.Fatal(err)
	}
	outputs[1].drawn(t, out)
	if l.sanitize["x.png"] != 1 || len(l.sanitize) != 1 {
		t.Errorf("SVGToPNG: Sanitize calls %v, want x.png once", l.sanitize)
	}
}

// An image that fails is reported once per call, as the URL the spec names
// (C35): the fetcher reported it under its sanitized URL, and the rasterizer
// again under the SVG's with a bogus "not a data: URI".
func TestImageFailureReportedOnce(t *testing.T) {
	l := &countingLoader{png: redPNG(t), fail: true}
	c, err := aster.New(aster.WithLoader(l))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	renders := map[string]func(spec []byte, h aster.ErrorHandlerOption) error{
		"SVG": func(s []byte, h aster.ErrorHandlerOption) error { _, err := c.VegaToSVG(s, h); return err },
		"PNG": func(s []byte, h aster.ErrorHandlerOption) error { _, err := c.VegaToPNG(s, h); return err },
		"PDF": func(s []byte, h aster.ErrorHandlerOption) error { _, err := c.VegaToPDF(s, h); return err },
	}
	for name, render := range renders {
		for _, sized := range []bool{true, false} {
			if name == "SVG" && sized {
				continue // nothing is fetched
			}
			l.reset()
			var got []string
			h := aster.WithErrorHandler(func(err error) error {
				var le *aster.LoadError
				if !errors.As(err, &le) {
					t.Errorf("%s: not a LoadError: %v", name, err)
					return nil
				}
				got = append(got, le.URL+": "+le.Err.Error())
				return nil
			})
			if err := render(imageSpec("x.png", sized), h); err != nil {
				t.Errorf("%s sized=%v: %v", name, sized, err)
			}
			if len(got) != 1 || !strings.HasPrefix(got[0], "x.png: ") || strings.Contains(got[0], "data: URI") {
				t.Errorf("%s sized=%v: reported %q, want one LoadError for x.png", name, sized, got)
			}
		}
	}
}

// The data and the images of one call share one allowance of loaded bytes
// (C80): each had its own, so a call could load twice what limits.md says.
func TestOneLoadAllowancePerCall(t *testing.T) {
	const limit = 32 << 20 // loaded bytes: 16 MiB
	data := []byte(`[{"a":"` + strings.Repeat("x", 10<<20) + `"}]`)
	img := append(redPNG(t), make([]byte, 10<<20)...)
	l := funcLoader{
		sanitize: func(_ context.Context, uri string) (string, error) { return uri, nil },
		load: func(_ context.Context, uri string) ([]byte, error) {
			if strings.HasSuffix(uri, ".json") {
				return data, nil
			}
			return img, nil
		},
	}
	c, err := aster.New(aster.WithLoader(l), aster.WithMemoryLimit(limit))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	spec := []byte(`{"width":40,"height":40,"data":[{"name":"d","url":"d.json"}],
		"marks":[{"type":"image","encode":{"enter":{"url":{"value":"i.png"},"width":{"value":16},"height":{"value":16}}}}]}`)
	if _, err := c.VegaToSVG(spec); err != nil {
		t.Fatalf("the data alone is within the allowance: %v", err)
	}
	if _, err := c.SVGToPNG(`<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40"><image href="i.png" width="16" height="16"/></svg>`); err != nil {
		t.Fatalf("the image alone is within the allowance: %v", err)
	}
	if _, err := c.VegaToPNG(spec); !errors.Is(err, aster.ErrLimit) {
		t.Errorf("data and image together: err = %v, want ErrLimit", err)
	}
	if _, err := c.VegaToPDF(spec); !errors.Is(err, aster.ErrLimit) {
		t.Errorf("PDF, data and image together: err = %v, want ErrLimit", err)
	}
}

// within fails the test when f has not returned after a generous, race-scaled
// time: the property is that it returns, not that it is fast.
func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	start := time.Now()
	select {
	case <-done:
		testutil.Timing(t, "within (hang guard: "+what+")", time.Since(start), testutil.Scaled(10*time.Second))
	case <-time.After(testutil.Scaled(10 * time.Second)):
		t.Fatalf("%s: still blocked (deadlock)", what)
	}
}

const failingData = `{"data":[{"name":"d","url":"missing.json"}]}`

// Close may be called from inside a call (C37): from its error handler or
// its Loader, it cancels and marks, and the call ends; it deadlocked, since
// the call held the lock Close waits for.
func TestCloseFromInsideACall(t *testing.T) {
	t.Run("handler", func(t *testing.T) {
		deny, _ := aster.New()
		var err error
		defer func() { _ = deny.Close() }()
		within(t, "Close from the error handler", func() {
			_, err = deny.VegaToSVG([]byte(failingData), aster.WithErrorHandler(func(error) error {
				_ = deny.Close()
				return nil
			}))
		})
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Errorf("the call went on after Close: err = %v", err)
		}
		if _, err := deny.VegaToSVG([]byte(`{}`)); !errors.Is(err, aster.ErrClosed) {
			t.Errorf("after Close: %v", err)
		}
	})
	t.Run("loader", func(t *testing.T) {
		var c *aster.Converter
		closed := make(chan error, 1)
		l := newClosingLoader(func() { closed <- c.Close() })
		c, _ = aster.New(aster.WithLoader(l))
		var err error
		within(t, "Close from the Loader", func() { _, err = c.VegaToSVG([]byte(failingData)) })
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
		within(t, "the Loader is closed once the call is over", func() { <-l.closedCh })
		if e := <-closed; e != nil {
			t.Errorf("Close from inside: %v", e)
		}
	})
	t.Run("loader on an image fetch goroutine", func(t *testing.T) {
		var c *aster.Converter
		l := newClosingLoader(func() { _ = c.Close() })
		c, _ = aster.New(aster.WithLoader(l))
		within(t, "Close from an image fetch", func() {
			_, _ = c.SVGToPNG(`<svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"><image href="a.png" width="4" height="4"/><image href="b.png" width="4" height="4"/></svg>`)
		})
		within(t, "the Loader is closed", func() { <-l.closedCh })
	})
}

// closingLoader calls close from Load, and records its own Close.
type closingLoader struct {
	close    func()
	once     sync.Once
	closedCh chan struct{}
}

func newClosingLoader(close func()) *closingLoader {
	return &closingLoader{close: close, closedCh: make(chan struct{})}
}

func (l *closingLoader) Sanitize(_ context.Context, uri string) (string, error) { return uri, nil }
func (l *closingLoader) Load(ctx context.Context, _ string) ([]byte, error) {
	l.close()
	return nil, ctx.Err()
}
func (l *closingLoader) Close() error { l.once.Do(func() { close(l.closedCh) }); return nil }

// A render started from an error handler while a Close waits must not
// deadlock (C37: a writer-preferring lock blocked the nested call, which
// blocked the outer one, which blocked Close); it fails with ErrClosed.
func TestNestedRenderWithPendingClose(t *testing.T) {
	c, err := aster.New()
	if err != nil {
		t.Fatal(err)
	}
	closeDone := make(chan error, 1)
	var nestedErr error
	within(t, "a nested render with a pending Close", func() {
		_, _ = c.VegaToSVG([]byte(failingData), aster.WithErrorHandler(func(error) error {
			go func() { closeDone <- c.Close() }()
			time.Sleep(testutil.Scaled(50 * time.Millisecond)) // Close is waiting for this call now
			_, nestedErr = c.VegaToSVG([]byte(`{}`))
			return nil
		}))
	})
	within(t, "Close", func() { <-closeDone })
	if !errors.Is(nestedErr, aster.ErrClosed) {
		t.Errorf("nested render: %v, want ErrClosed", nestedErr)
	}
}

// Close called from outside still waits for the calls in flight, Loader
// methods included, before it closes the Loader and returns.
func TestCloseWaitsForCallsInFlight(t *testing.T) {
	inLoad := make(chan struct{})
	var loadReturned atomic.Bool
	l := &trackingCloser{}
	l.load = func(ctx context.Context) error {
		close(inLoad)
		<-ctx.Done()
		time.Sleep(testutil.Scaled(50 * time.Millisecond))
		loadReturned.Store(true)
		return ctx.Err()
	}
	c, err := aster.New(aster.WithLoader(l))
	if err != nil {
		t.Fatal(err)
	}
	callErr := make(chan error, 1)
	go func() { _, err := c.VegaToSVG([]byte(failingData)); callErr <- err }()
	<-inLoad
	within(t, "Close", func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	if !loadReturned.Load() {
		t.Error("Close returned while the Loader was still loading")
	}
	if !l.closed.Load() {
		t.Error("Close returned before closing the Loader")
	}
	if err := <-callErr; !errors.Is(err, context.Canceled) {
		t.Errorf("the call: %v, want context.Canceled", err)
	}
}

type trackingCloser struct {
	load   func(ctx context.Context) error
	closed atomic.Bool
}

func (l *trackingCloser) Sanitize(_ context.Context, uri string) (string, error) { return uri, nil }
func (l *trackingCloser) Load(ctx context.Context, _ string) ([]byte, error) {
	return nil, l.load(ctx)
}
func (l *trackingCloser) Close() error { l.closed.Store(true); return nil }

// heavySpec takes a good while to render, in the dataflow and its writers.
const heavySpec = `{"width":400,"height":400,"data":[{"name":"t","transform":[{"type":"sequence","start":0,"stop":400000},{"type":"formula","as":"y","expr":"sin(datum.data)*cos(datum.data)"},{"type":"window","ops":["sum"],"fields":["y"],"as":["s"]}]}]}`

// heavySVG takes a good while to rasterize.
var heavySVG = `<svg xmlns="http://www.w3.org/2000/svg" width="1000" height="1000">` +
	strings.Repeat(`<path d="M0,0L1000,0L1000,1000Z" fill="red" fill-opacity="0.5"/>`, 300) +
	strings.Repeat(`<path d="M1,1L2,1L2,2Z" fill="blue"/>`, 150000) + `</svg>`

type ctxKey struct{}

// WithContext cancels one call without closing the Converter (C70): each
// method takes it, the call stops and fails with the context's error, frees
// what it held, and the Converter goes on serving other calls.
func TestWithContextCancelsOneCall(t *testing.T) {
	blocking := funcLoader{
		sanitize: func(_ context.Context, uri string) (string, error) { return uri, nil },
		load: func(ctx context.Context, _ string) ([]byte, error) {
			if ctx.Value(ctxKey{}) != "request" {
				return nil, errors.New("the call's context does not carry the caller's values")
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	c, err := aster.New(aster.WithLoader(blocking))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	vl := []byte(`{"data":{"url":"d.json"},"mark":"point"}`)
	vg := []byte(failingData)
	svgIn := `<svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"><image href="a.png" width="4" height="4"/></svg>`
	calls := map[string]func(o aster.ContextOption) error{
		"VegaToSVG":          func(o aster.ContextOption) error { _, err := c.VegaToSVG(vg, o); return err },
		"VegaLiteToSVG":      func(o aster.ContextOption) error { _, err := c.VegaLiteToSVG(vl, o); return err },
		"VegaToPNG":          func(o aster.ContextOption) error { _, err := c.VegaToPNG(vg, o); return err },
		"VegaLiteToPNG":      func(o aster.ContextOption) error { _, err := c.VegaLiteToPNG(vl, o); return err },
		"SVGToPNG":           func(o aster.ContextOption) error { _, err := c.SVGToPNG(svgIn, o); return err },
		"VegaToPDF":          func(o aster.ContextOption) error { _, err := c.VegaToPDF(vg, o); return err },
		"VegaLiteToPDF":      func(o aster.ContextOption) error { _, err := c.VegaLiteToPDF(vl, o); return err },
		"SVGToPDF":           func(o aster.ContextOption) error { _, err := c.SVGToPDF(svgIn, o); return err },
		"SVGToPDFUsage":      func(o aster.ContextOption) error { _, _, err := c.SVGToPDFUsage(svgIn, o); return err },
		"VegaToPDFUsage":     func(o aster.ContextOption) error { _, _, err := c.VegaToPDFUsage(vg, o); return err },
		"VegaLiteToPDFUsage": func(o aster.ContextOption) error { _, _, err := c.VegaLiteToPDFUsage(vl, o); return err },
	}
	before := runtime.NumGoroutine()
	for name, call := range calls {
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "request"))
		time.AfterFunc(testutil.Scaled(20*time.Millisecond), cancel)
		var err error
		within(t, name, func() { err = call(aster.WithContext(ctx)) })
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v, want context.Canceled", name, err)
		}
		cancel()
	}
	// A heavy render is stopped in the middle of its work, not only in a
	// Loader.
	for name, call := range map[string]func(o aster.ContextOption) error{
		"VegaToSVG": func(o aster.ContextOption) error { _, err := c.VegaToSVG([]byte(heavySpec), o); return err },
		"VegaToPNG": func(o aster.ContextOption) error { _, err := c.VegaToPNG([]byte(heavySpec), o); return err },
		"SVGToPNG":  func(o aster.ContextOption) error { _, err := c.SVGToPNG(heavySVG, o); return err },
		"SVGToPDF":  func(o aster.ContextOption) error { _, err := c.SVGToPDF(heavySVG, o); return err },
	} {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(testutil.Scaled(20*time.Millisecond), cancel)
		var err error
		within(t, name+" heavy", func() { err = call(aster.WithContext(ctx)) })
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s heavy: err = %v, want context.Canceled", name, err)
		}
		cancel()
	}
	// The Converter is unharmed.
	if _, err := c.VegaToSVG([]byte(`{"width":10,"height":10}`)); err != nil {
		t.Errorf("after cancelled calls: %v", err)
	}
	// Nothing a cancelled call started is left running.
	goroutineWait := time.Now()
	deadline := goroutineWait.Add(testutil.Scaled(5 * time.Second))
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	testutil.Timing(t, "cancelled calls leave no goroutines (wait for them to exit)", time.Since(goroutineWait), testutil.Scaled(5*time.Second))
	if n := runtime.NumGoroutine(); n > before {
		t.Errorf("%d goroutines left behind by cancelled calls", n-before)
	}
}

// A caller's deadline is the call's: it fails with DeadlineExceeded, and the
// message does not claim the Converter's WithTimeout ran out, nor repeat the
// cause (X9).
func TestWithContextDeadline(t *testing.T) {
	c, err := aster.New(aster.WithTimeout(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Scaled(20*time.Millisecond))
	defer cancel()
	_, err = c.VegaToSVG([]byte(heavySpec), aster.WithContext(ctx))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if strings.Contains(err.Error(), "1h0m0s") || strings.Count(err.Error(), "deadline exceeded") != 1 {
		t.Errorf("message %q: names the Converter's timeout, or repeats the cause", err)
	}
	// The Converter's own timeout names itself, once.
	d, err := aster.New(aster.WithTimeout(testutil.Scaled(20 * time.Millisecond)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	_, err = d.VegaToSVG([]byte(heavySpec))
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out after") || strings.Count(err.Error(), "deadline exceeded") != 1 {
		t.Errorf("converter timeout: %v", err)
	}
	// Close still cancels a call that has a context of its own.
	e, _ := aster.New()
	go func() { time.Sleep(testutil.Scaled(20 * time.Millisecond)); _ = e.Close() }()
	_, err = e.VegaToSVG([]byte(heavySpec), aster.WithContext(context.Background()))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Close during a call with a context: %v, want context.Canceled", err)
	}
}

// Options are checked by New, so a mistake fails there rather than being
// silently replaced by a default (C74 to C79).
func TestNewRejectsInvalidOptions(t *testing.T) {
	font, err := os.ReadFile("internal/fonts/dejavu/DejaVuSans-Bold.ttf")
	if err != nil {
		t.Fatal(err)
	}
	for name, opt := range map[string]aster.Option{
		"WithDefaultFontFamily typo":      aster.WithDefaultFontFamily("Liberaton Sans"),
		"WithDefaultSerifFamily typo":     aster.WithDefaultSerifFamily("Typo"),
		"WithDefaultMonospaceFamily typo": aster.WithDefaultMonospaceFamily("Typo"),
		"WithFontFace negative":           aster.WithFontFace("D", font, -1),
		"typed nil Loader":                aster.WithLoader((*aster.FileLoader)(nil)),
		"typed nil HTTPLoader":            aster.WithLoader((*aster.HTTPLoader)(nil)),
		"Local timezone":                  aster.WithTimezone("Local"),
		"Vega-Lite 6.4.0":                 aster.WithVegaLiteVersion("6.4.0"),
	} {
		if _, err := aster.New(opt); err == nil {
			t.Errorf("%s: New succeeded", name) // not closed: a typed nil Loader panics there
		}
	}
	vs, _ := aster.AvailableVersions()
	for _, v := range vs {
		for _, s := range []string{v.VegaLiteVersion, "v" + v.VegaLiteVersion} {
			c, err := aster.New(aster.WithVegaLiteVersion(s))
			if err != nil {
				t.Errorf("WithVegaLiteVersion(%q), as AvailableVersions reports it: %v", s, err)
				continue
			}
			_ = c.Close()
		}
	}
	for name, opts := range map[string][]aster.Option{
		"registered default family": {aster.WithFont("D", font), aster.WithDefaultFontFamily("D")},
		"embedded default families": {aster.WithDefaultFontFamily("Liberation Serif"), aster.WithDefaultSerifFamily("Liberation Sans"), aster.WithDefaultMonospaceFamily("Liberation Mono")},
		"untyped nil Loader":        {aster.WithLoader(nil)},
		"IANA zone":                 {aster.WithTimezone("Europe/Madrid")},
	} {
		c, err := aster.New(opts...)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		_ = c.Close()
	}
}

// The fonts given to New are the Converter's from then on (C71): the bytes
// are copied, so a caller that reuses its buffer changes nothing, and every
// stage draws from one font set built once.
func TestFontBytesAreCopied(t *testing.T) {
	font, err := os.ReadFile("internal/fonts/dejavu/DejaVuSans-Bold.ttf")
	if err != nil {
		t.Fatal(err)
	}
	spec := []byte(`{"width":120,"height":30,"marks":[{"type":"text","encode":{"enter":{"text":{"value":"Hamburgefonstiv"},"font":{"value":"Mine"},"fontSize":{"value":14},"x":{"value":4},"y":{"value":20}}}}]}`)
	render := func(c *aster.Converter) (string, []byte, []byte) {
		svg, err := c.VegaToSVG(spec)
		if err != nil {
			t.Fatal(err)
		}
		p, err := c.VegaToPNG(spec)
		if err != nil {
			t.Fatal(err)
		}
		d, err := c.VegaToPDF(spec)
		if err != nil {
			t.Fatal(err)
		}
		return svg, p, d
	}
	for _, measure := range []bool{true, false} {
		want, err := aster.New(aster.WithFont("Mine", font), aster.WithTextMeasurement(measure))
		if err != nil {
			t.Fatal(err)
		}
		wSVG, wPNG, wPDF := render(want)
		_ = want.Close()

		buf := bytes.Clone(font)
		c, err := aster.New(aster.WithFont("Mine", buf), aster.WithTextMeasurement(measure))
		if err != nil {
			t.Fatal(err)
		}
		clear(buf) // the caller reuses its buffer
		svg, p, d := render(c)
		_ = c.Close()
		if svg != wSVG || !bytes.Equal(p, wPNG) || !bytes.Equal(d, wPDF) {
			t.Errorf("measure=%v: output changed with the caller's buffer (SVG %v, PNG %v, PDF %v)", measure, svg == wSVG, bytes.Equal(p, wPNG), bytes.Equal(d, wPDF))
		}
	}
}

// One font set serves layout, PNG and PDF: the first PNG and PDF of a
// Converter retain next to nothing more than its fonts do, where each built a
// font set of its own (C71: every registered font parsed again, the system
// fonts scanned again). Measured as bytes retained after a collection, against
// what the font set itself retains.
func TestOneFontSetPerConverter(t *testing.T) {
	font, err := os.ReadFile("internal/fonts/dejavu/DejaVuSans-Bold.ttf")
	if err != nil {
		t.Fatal(err)
	}
	retained := func() int64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return int64(m.HeapAlloc)
	}
	spec := []byte(`{"width":60,"height":30,"marks":[{"type":"text","encode":{"enter":{"text":{"value":"Hi"},"font":{"value":"F0"},"x":{"value":4},"y":{"value":20}}}}]}`)
	var fontSet int64
	for _, measure := range []bool{true, false} {
		opts := []aster.Option{aster.WithTextMeasurement(measure)}
		for i := range 8 {
			opts = append(opts, aster.WithFont(fmt.Sprint("F", i), font))
		}
		b0 := retained()
		c, err := aster.New(opts...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.VegaToSVG(spec); err != nil {
			t.Fatal(err)
		}
		b1 := retained()
		if _, err := c.VegaToPNG(spec); err != nil {
			t.Fatal(err)
		}
		if _, err := c.VegaToPDF(spec); err != nil {
			t.Fatal(err)
		}
		b2 := retained()
		if measure {
			fontSet = b1 - b0
		}
		t.Logf("measure=%v: New and the first SVG +%d KiB, then the first PNG and PDF +%d KiB", measure, (b1-b0)>>10, (b2-b1)>>10)
		if b2-b1 > fontSet/4 {
			t.Errorf("measure=%v: the first PNG and PDF retained %d KiB more, against %d KiB for the font set: fonts of their own", measure, (b2-b1)>>10, fontSet>>10)
		}
		_ = c.Close()
	}
}

// The text shaper's error reaches the caller (C77): the label transform drew
// no glyphs and the render succeeded.
func TestShaperErrorReachesTheCaller(t *testing.T) {
	c, err := aster.New(aster.WithTextMeasurement(false), aster.WithFontSetFailureForTest())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	spec := []byte(`{"width":100,"height":100,"data":[{"name":"d","values":[{"x":10,"y":10}]}],
		"marks":[{"type":"symbol","name":"pts","from":{"data":"d"},"encode":{"enter":{"x":{"field":"x"},"y":{"field":"y"}}}},
		{"type":"text","from":{"data":"pts"},"encode":{"enter":{"text":{"value":"label"}}},"transform":[{"type":"label","size":[100,100]}]}]}`)
	if _, err := c.VegaToSVG(spec); err == nil || !strings.Contains(err.Error(), "font set failure for test") {
		t.Errorf("err = %v, want the shaper's", err)
	}
}
