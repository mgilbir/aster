// Package aster converts Vega and Vega-Lite visualization specs to SVG and PNG.
// It embeds Vega/Vega-Lite inside QuickJS (via WASM) for a pure-Go,
// CGO-free solution.
//
// Basic usage:
//
//	c, err := aster.New()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer c.Close()
//
//	svg, err := c.VegaLiteToSVG(specJSON)
//	png, err := c.VegaLiteToPNG(specJSON)
package aster

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/mgilbir/aster/internal/resvg"
	"github.com/mgilbir/aster/internal/runtime"
	"github.com/mgilbir/aster/internal/textmeasure"
	"github.com/mgilbir/aster/internal/textmeasure/fonts/liberation"
)

// Converter renders Vega/Vega-Lite specs to SVG and PNG.
type Converter struct {
	rt       *runtime.Runtime
	measurer *textmeasure.Measurer
	fonts    []fontEntry // stashed for lazy PNG renderer init
	loader   Loader      // stashed for Close()

	pngOnce     sync.Once
	pngRenderer *resvg.Renderer
	pngErr      error
}

// New creates a new Converter with the given options.
func New(opts ...Option) (*Converter, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	var measurer *textmeasure.Measurer
	var tm runtime.TextMeasurer
	if cfg.textMeasure {
		var measurerOpts []textmeasure.MeasurerOption
		if cfg.systemFonts {
			measurerOpts = append(measurerOpts, textmeasure.WithSystemFonts())
		}
		for _, f := range cfg.fonts {
			measurerOpts = append(measurerOpts, textmeasure.WithFont(f.family, f.data))
		}
		if cfg.defaultFontFamily != "" {
			measurerOpts = append(measurerOpts, textmeasure.WithDefaultFontFamily(cfg.defaultFontFamily))
		}
		var err error
		measurer, err = textmeasure.New(measurerOpts...)
		if err != nil {
			return nil, fmt.Errorf("aster: initializing text measurer: %w", err)
		}
		tm = measurer
	}

	rtCfg := runtime.Config{
		Loader:       cfg.loader,
		TextMeasurer: tm,
		Theme:        cfg.theme,
		MemoryLimit:  int(cfg.memoryLimit),
		Timeout:      cfg.timeout,
		Version:      cfg.vegaLiteVersion,
		Timezone:     cfg.timezone,
	}

	rt, err := runtime.New(rtCfg)
	if err != nil {
		return nil, fmt.Errorf("aster: %w", err)
	}

	return &Converter{
		rt:       rt,
		measurer: measurer,
		fonts:    cfg.fonts,
		loader:   cfg.loader,
	}, nil
}

// Close releases all resources held by the Converter.
func (c *Converter) Close() error {
	var firstErr error
	if c.pngRenderer != nil {
		if err := c.pngRenderer.Close(context.Background()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c.rt != nil {
		if err := c.rt.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if closer, ok := c.loader.(io.Closer); ok {
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// VegaToSVG renders a Vega spec (JSON) to an SVG string.
func (c *Converter) VegaToSVG(spec []byte) (string, error) {
	return c.rt.VegaToSVG(string(spec))
}

// VegaLiteToSVG renders a Vega-Lite spec (JSON) to an SVG string.
func (c *Converter) VegaLiteToSVG(spec []byte) (string, error) {
	return c.rt.VegaLiteToSVG(string(spec))
}

// VegaLiteToVega compiles a Vega-Lite spec (JSON) to a full Vega spec (JSON).
func (c *Converter) VegaLiteToVega(spec []byte) ([]byte, error) {
	result, err := c.rt.VegaLiteToVega(string(spec))
	if err != nil {
		return nil, err
	}
	return []byte(result), nil
}

// VegaToPNG renders a Vega spec (JSON) to a PNG image.
func (c *Converter) VegaToPNG(spec []byte, opts ...PNGOption) ([]byte, error) {
	svg, err := c.VegaToSVG(spec)
	if err != nil {
		return nil, err
	}
	return c.SVGToPNG(svg, opts...)
}

// VegaLiteToPNG renders a Vega-Lite spec (JSON) to a PNG image.
func (c *Converter) VegaLiteToPNG(spec []byte, opts ...PNGOption) ([]byte, error) {
	svg, err := c.VegaLiteToSVG(spec)
	if err != nil {
		return nil, err
	}
	return c.SVGToPNG(svg, opts...)
}

// SVGToPNG converts an SVG string to a PNG image using resvg.
func (c *Converter) SVGToPNG(svg string, opts ...PNGOption) ([]byte, error) {
	cfg := defaultPNGConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	r, err := c.pngRendererInit()
	if err != nil {
		return nil, err
	}

	return r.Render(context.Background(), []byte(svg), cfg.scale)
}

// pngRendererInit lazily initializes the PNG renderer on first use.
func (c *Converter) pngRendererInit() (*resvg.Renderer, error) {
	c.pngOnce.Do(func() {
		// Build font list: embedded Liberation Sans + custom fonts.
		var fonts []resvg.Font
		fonts = append(fonts,
			resvg.Font{Data: liberation.SansRegular},
			resvg.Font{Data: liberation.SansBold},
			resvg.Font{Data: liberation.SansItalic},
			resvg.Font{Data: liberation.SansBoldItalic},
			resvg.Font{Data: liberation.MonoRegular},
			resvg.Font{Data: liberation.MonoBold},
			resvg.Font{Data: liberation.MonoItalic},
			resvg.Font{Data: liberation.MonoBoldItalic},
		)
		for _, f := range c.fonts {
			fonts = append(fonts, resvg.Font{Data: f.data})
		}

		families := resvg.FamilyMapping{
			SansSerif: "Liberation Sans",
			Monospace: "Liberation Mono",
		}
		c.pngRenderer, c.pngErr = resvg.New(context.Background(), fonts, families)
		if c.pngErr != nil {
			c.pngErr = fmt.Errorf("aster: initializing PNG renderer: %w", c.pngErr)
		}
	})
	return c.pngRenderer, c.pngErr
}
