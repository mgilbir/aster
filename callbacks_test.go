package aster_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/aster"
)

// Every call into the caller's code (the Loader's Sanitize, Load and Close,
// the error handler, a WithSignal value's MarshalJSON, a custom projection)
// goes through one boundary: a panic in it becomes an error of the call, on
// whatever goroutine the engine ran it (C15: an image fetched for PNG or PDF
// output ran the Loader on a goroutine of its own, and its panic killed the
// process), and the Converter's lifecycle reaches Closed whatever the Loader's
// Close does (C17). A nil child of a FallbackLoader is refused by New (C16).
//
// A broken contract crashes or hangs the process, so each case runs in a
// child process of the test binary: the test sees the crash rather than
// dying of it.

const callbackChildEnv = "ASTER_CALLBACK_CASE"

// callbackCase runs one use of a misbehaving callback and returns what is
// wrong with the outcome, or "" when the contract held.
type callbackCase func() string

const remoteImageSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><image href="http://example.com/a.png" width="10" height="10"/></svg>`

// remoteImageSpec draws one image mark of a remote URL with its size set, so the
// SVG stage writes it without fetching it and PNG and PDF output fetch it
// (on imageref.Fetch's goroutines).
const remoteImageSpec = `{"width":20,"height":20,"marks":[{"type":"image","encode":{"enter":{"url":{"value":"http://example.com/a.png"},"width":{"value":10},"height":{"value":10}}}}]}`

const urlDataSpec = `{"data":{"url":"x.json"},"mark":"point"}`

const remoteLinkSpec = `{"width":20,"height":20,"marks":[{"type":"rect","encode":{"enter":{"width":{"value":5},"height":{"value":5},"href":{"value":"http://example.com/"}}}}]}`

// panickingLoader accepts every URI and panics in the methods it is told to.
type panickingLoader struct{ sanitize, load, close, deny bool }

func (l panickingLoader) Sanitize(_ context.Context, u string) (string, error) {
	if l.sanitize {
		panic("loader bug in Sanitize")
	}
	return u, nil
}

func (l panickingLoader) Load(context.Context, string) ([]byte, error) {
	if l.load {
		panic("loader bug in Load")
	}
	if l.deny {
		return nil, errors.New("denied")
	}
	return []byte(`[{"a":1}]`), nil
}

func (l panickingLoader) Close() error {
	if l.close {
		panic("loader bug in Close")
	}
	return nil
}

// wantPanicErr is "" when err reports the callback's panic as an error.
func wantPanicErr(what string, err error) string {
	if err == nil {
		return what + ": no error"
	}
	if !strings.Contains(err.Error(), "panicked") {
		return fmt.Sprintf("%s: error %q does not say the callback panicked", what, err)
	}
	return ""
}

func withLoader(l aster.Loader, f func(c *aster.Converter) error) error {
	c, err := aster.New(aster.WithLoader(l))
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return f(c)
}

// closeTwice closes c from two goroutines in turn and reports a Close that
// does not return (the hang is the child's timeout's to catch) or the errors.
func closeTwice(c *aster.Converter) (first, second error) {
	first = c.Close()
	done := make(chan error, 1)
	go func() { done <- c.Close() }()
	return first, <-done
}

type panickyJSON struct{}

func (panickyJSON) MarshalJSON() ([]byte, error) { panic("MarshalJSON bug") }

var callbackCases = map[string]callbackCase{
	"Load panics on data": func() string {
		var le *aster.LoadError
		err := withLoader(panickingLoader{load: true}, func(c *aster.Converter) error {
			_, err := c.VegaLiteToSVG([]byte(urlDataSpec))
			return err
		})
		if !errors.As(err, &le) {
			return fmt.Sprintf("data: %v is not a *LoadError", err)
		}
		return wantPanicErr("data", err)
	},
	"Load panics on an image, SVGToPNG": func() string {
		return wantPanicErr("SVGToPNG", withLoader(panickingLoader{load: true}, func(c *aster.Converter) error {
			_, err := c.SVGToPNG(remoteImageSVG)
			return err
		}))
	},
	"Load panics on an image, SVGToPDF": func() string {
		return wantPanicErr("SVGToPDF", withLoader(panickingLoader{load: true}, func(c *aster.Converter) error {
			_, err := c.SVGToPDF(remoteImageSVG)
			return err
		}))
	},
	"Load panics on an image mark, VegaToPNG": func() string {
		return wantPanicErr("VegaToPNG", withLoader(panickingLoader{load: true}, func(c *aster.Converter) error {
			_, err := c.VegaToPNG([]byte(remoteImageSpec))
			return err
		}))
	},
	"Load panics on an image mark, VegaToPDF": func() string {
		return wantPanicErr("VegaToPDF", withLoader(panickingLoader{load: true}, func(c *aster.Converter) error {
			_, err := c.VegaToPDF([]byte(remoteImageSpec))
			return err
		}))
	},
	"Load panics, handler lets it go": func() string {
		var told []error
		err := withLoader(panickingLoader{load: true}, func(c *aster.Converter) error {
			_, err := c.SVGToPNG(remoteImageSVG, aster.WithErrorHandler(func(err error) error {
				told = append(told, err)
				return nil
			}))
			return err
		})
		if err != nil {
			return fmt.Sprintf("handler let the failure go, but the call failed: %v", err)
		}
		if len(told) != 1 {
			return fmt.Sprintf("handler told of %d errors, want 1", len(told))
		}
		return wantPanicErr("told", told[0])
	},
	"Sanitize panics on data": func() string {
		return wantPanicErr("data", withLoader(panickingLoader{sanitize: true}, func(c *aster.Converter) error {
			_, err := c.VegaLiteToSVG([]byte(urlDataSpec))
			return err
		}))
	},
	"Sanitize panics on an image, SVGToPNG": func() string {
		return wantPanicErr("SVGToPNG", withLoader(panickingLoader{sanitize: true}, func(c *aster.Converter) error {
			_, err := c.SVGToPNG(remoteImageSVG)
			return err
		}))
	},
	"Sanitize panics on a link": func() string {
		var told []error
		err := withLoader(panickingLoader{sanitize: true}, func(c *aster.Converter) error {
			_, err := c.VegaToSVG([]byte(remoteLinkSpec), aster.WithErrorHandler(func(err error) error {
				told = append(told, err)
				return nil
			}))
			return err
		})
		if err != nil {
			return fmt.Sprintf("a dropped link failed the call: %v", err)
		}
		if len(told) != 1 {
			return fmt.Sprintf("told of %d warnings, want 1", len(told))
		}
		return wantPanicErr("link warning", told[0])
	},
	"error handler panics": func() string {
		return wantPanicErr("handler", withLoader(nil, func(c *aster.Converter) error {
			_, err := c.VegaLiteToSVG([]byte(urlDataSpec), aster.WithErrorHandler(func(error) error { panic("handler bug") }))
			return err
		}))
	},
	"signal value's MarshalJSON panics": func() string {
		return wantPanicErr("signal", withLoader(nil, func(c *aster.Converter) error {
			_, err := c.VegaToSVG([]byte(`{"signals":[{"name":"s","value":1}]}`), aster.WithSignal("s", panickyJSON{}))
			return err
		}))
	},
	"Close panics": func() string {
		c, err := aster.New(aster.WithLoader(panickingLoader{close: true}))
		if err != nil {
			return err.Error()
		}
		first, second := closeTwice(c)
		if msg := wantPanicErr("first Close", first); msg != "" {
			return msg
		}
		if second != nil {
			return fmt.Sprintf("second Close: %v, want nil (only the first reports the Loader's error)", second)
		}
		if _, err := c.VegaLiteToSVG([]byte(urlDataSpec)); !errors.Is(err, aster.ErrClosed) {
			return fmt.Sprintf("a call after Close: %v, want ErrClosed", err)
		}
		return ""
	},
	"Close panics, closed from inside a call": func() string {
		c, err := aster.New(aster.WithLoader(panickingLoader{close: true, deny: true}))
		if err != nil {
			return err.Error()
		}
		_, err = c.VegaLiteToSVG([]byte(urlDataSpec), aster.WithErrorHandler(func(error) error { return c.Close() }))
		if !errors.Is(err, context.Canceled) {
			return fmt.Sprintf("the call: %v, want context.Canceled", err)
		}
		// The Loader was closed as the call ended; a Close from outside
		// reports its error, and returns.
		first, second := closeTwice(c)
		if msg := wantPanicErr("Close after the call", first); msg != "" {
			return msg
		}
		if second != nil {
			return fmt.Sprintf("second Close: %v", second)
		}
		return ""
	},
	"Close calls Close": func() string {
		var c *aster.Converter
		l := &reentrantCloser{close: func() error { return c.Close() }}
		c, err := aster.New(aster.WithLoader(l))
		if err != nil {
			return err.Error()
		}
		if err := c.Close(); err != nil {
			return fmt.Sprintf("Close: %v", err)
		}
		return ""
	},
	"projection calls Close": func() string {
		var c *aster.Converter
		p := aster.Projection{Raw: func(map[string]float64) aster.RawProjection {
			return aster.RawProjection{Forward: func(lambda, phi float64) (float64, float64) {
				if c != nil {
					_ = c.Close() // must not wait for the call it runs in
				}
				return lambda, phi
			}}
		}}
		c, err := aster.New(aster.WithProjection("closing", p))
		if err != nil {
			return err.Error()
		}
		spec := `{"projections":[{"name":"p","type":"closing"}],"data":[{"name":"d","values":[{"x":1,"y":2}],"transform":[{"type":"geopoint","projection":"p","fields":["x","y"]}]}]}`
		if _, err := c.VegaToSVG([]byte(spec)); !errors.Is(err, context.Canceled) {
			return fmt.Sprintf("the call: %v, want context.Canceled", err)
		}
		return ""
	},
	"FallbackLoader with a nil *FileLoader": func() string {
		fl, _ := aster.NewFileLoader("/nonexistent-aster-callback-dir")
		c, err := aster.New(aster.WithLoader(aster.NewFallbackLoader(aster.DenyLoader{}, fl)))
		if err == nil {
			_ = c.Close()
			return "New accepted a FallbackLoader holding a nil *FileLoader"
		}
		if !strings.Contains(err.Error(), "nil") {
			return fmt.Sprintf("New: %v does not say what is nil", err)
		}
		return ""
	},
	"FallbackLoader with a nil child": func() string {
		c, err := aster.New(aster.WithLoader(aster.NewFallbackLoader(nil)))
		if err == nil {
			_ = c.Close()
			return "New accepted a FallbackLoader holding a nil Loader"
		}
		return ""
	},
	"nested FallbackLoader with a nil *HTTPLoader": func() string {
		var hl *aster.HTTPLoader
		inner := aster.NewFallbackLoader(aster.DenyLoader{}, hl)
		outer := aster.NewFallbackLoader(inner)
		outer.Loaders = append(outer.Loaders, outer) // a cycle must not hang the check
		c, err := aster.New(aster.WithLoader(outer))
		if err == nil {
			_ = c.Close()
			return "New accepted a nested FallbackLoader holding a nil *HTTPLoader"
		}
		return ""
	},
	"nil loaders used directly": func() string {
		var fl *aster.FileLoader
		var hl *aster.HTTPLoader
		var sl *aster.StaticLoader
		var fb *aster.FallbackLoader
		ctx := context.Background()
		var bad []string
		for name, l := range map[string]aster.Loader{"*FileLoader": fl, "*HTTPLoader": hl, "*StaticLoader": sl, "*FallbackLoader": fb, "FallbackLoader of nils": aster.NewFallbackLoader(nil, fl, hl)} {
			if _, err := l.Sanitize(ctx, "a.json"); err == nil {
				bad = append(bad, name+".Sanitize accepted")
			}
			if _, err := l.Load(ctx, "a.json"); err == nil {
				bad = append(bad, name+".Load loaded")
			}
		}
		_ = aster.NewFallbackLoader(nil, fl).Close()
		sort.Strings(bad)
		return strings.Join(bad, "; ")
	},
}

type reentrantCloser struct{ close func() error }

func (*reentrantCloser) Sanitize(_ context.Context, u string) (string, error) { return u, nil }
func (*reentrantCloser) Load(context.Context, string) ([]byte, error)         { return nil, errors.New("no") }
func (l *reentrantCloser) Close() error                                       { return l.close() }

func TestUserCallbacksHaveOneContract(t *testing.T) {
	if name := os.Getenv(callbackChildEnv); name != "" {
		f, ok := callbackCases[name]
		if !ok {
			t.Fatalf("no case %q", name)
		}
		if msg := f(); msg != "" {
			fmt.Println("CONTRACT BROKEN: " + msg)
			os.Exit(1)
		}
		fmt.Println("CONTRACT HELD")
		os.Exit(0)
	}
	names := make([]string, 0, len(callbackCases))
	for name := range callbackCases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// A hang backstop only: every case returns in milliseconds.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUserCallbacksHaveOneContract$", "-test.count=1")
			cmd.Env = append(os.Environ(), callbackChildEnv+"="+name)
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("hung (killed after the backstop):\n%s", head(out))
			}
			if err != nil || !strings.Contains(string(out), "CONTRACT HELD") {
				t.Fatalf("%v:\n%s", err, head(out))
			}
		})
	}
}

func head(b []byte) string {
	const n = 3000
	if len(b) > n {
		b = b[:n]
	}
	return string(b)
}
