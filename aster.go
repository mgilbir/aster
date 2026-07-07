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
	"math"
	"sort"
	"sync"

	"github.com/mgilbir/aster/internal/resvg"
	"github.com/mgilbir/aster/internal/runtime"
	"github.com/mgilbir/aster/internal/textmeasure"
)

// Converter renders Vega/Vega-Lite specs to SVG and PNG.
type Converter struct {
	rt       *runtime.Runtime
	measurer *textmeasure.Measurer
	fonts    fontPlan // shared by text measurement and PNG rasterization
	loader   Loader   // stashed for Close()

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

	// Both pipelines are configured from a single font plan so SVG layout and
	// PNG rasterization can't disagree about fonts or generic-family mappings.
	plan := newFontPlan(cfg)

	var measurer *textmeasure.Measurer
	var tm runtime.TextMeasurer
	if cfg.textMeasure {
		var err error
		measurer, err = textmeasure.New(plan.measurerOptions()...)
		if err != nil {
			return nil, fmt.Errorf("aster: initializing text measurer: %w", err)
		}
		tm = measurer
	}

	rtCfg := runtime.Config{
		Loader:       cfg.loader,
		TextMeasurer: tm,
		Theme:        cfg.theme,
		MemoryLimit:  cfg.memoryLimit,
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
		fonts:    plan,
		loader:   cfg.loader,
	}, nil
}

// VersionInfo describes an available Vega-Lite version set.
type VersionInfo struct {
	Key             string // internal key accepted by the runtime, e.g. "vl6_4"
	VegaVersion     string // resolved Vega runtime version, e.g. "6.2.0"
	VegaLiteVersion string // Vega-Lite version, e.g. "6.4.0"
}

// AvailableVersions reports the Vega-Lite version sets bundled in this build,
// sorted by key. Pass a VegaLiteVersion (e.g. "6.4") to WithVegaLiteVersion.
func AvailableVersions() ([]VersionInfo, error) {
	m, err := runtime.AvailableVersions()
	if err != nil {
		return nil, err
	}
	out := make([]VersionInfo, 0, len(m))
	for k, v := range m {
		out = append(out, VersionInfo{Key: k, VegaVersion: v.VegaVersion, VegaLiteVersion: v.VegaLiteVersion})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
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

	if !(cfg.scale > 0) || math.IsInf(cfg.scale, 1) {
		return nil, fmt.Errorf("aster: invalid PNG scale %v (must be a positive, finite number)", cfg.scale)
	}

	r, err := c.pngRendererInit()
	if err != nil {
		return nil, err
	}

	out, err := r.Render(context.Background(), []byte(svg), cfg.scale)
	if err != nil {
		return nil, err
	}
	if cfg.recode {
		out = recodePNG(out)
	}
	return out, nil
}

// pngRendererInit lazily initializes the PNG renderer on first use. It draws
// its fonts and generic-family mapping from the same fontPlan that configured
// text measurement, so PNG glyphs are rasterized with the font the SVG layout
// was measured against.
func (c *Converter) pngRendererInit() (*resvg.Renderer, error) {
	c.pngOnce.Do(func() {
		fonts, families := c.fonts.resvgFonts()
		c.pngRenderer, c.pngErr = resvg.New(context.Background(), fonts, families)
		if c.pngErr != nil {
			c.pngErr = fmt.Errorf("aster: initializing PNG renderer: %w", c.pngErr)
		}
	})
	return c.pngRenderer, c.pngErr
}
