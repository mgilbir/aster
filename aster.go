// Package aster converts Vega and Vega-Lite visualization specs to SVG.
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
package aster

import (
	"fmt"

	"github.com/mgilbir/aster/internal/runtime"
	"github.com/mgilbir/aster/internal/textmeasure"
)

// Converter renders Vega/Vega-Lite specs to SVG.
type Converter struct {
	rt       *runtime.Runtime
	measurer *textmeasure.Measurer
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
		var err error
		measurer, err = textmeasure.New()
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
	}

	rt, err := runtime.New(rtCfg)
	if err != nil {
		return nil, fmt.Errorf("aster: %w", err)
	}

	return &Converter{
		rt:       rt,
		measurer: measurer,
	}, nil
}

// Close releases all resources held by the Converter.
func (c *Converter) Close() error {
	if c.rt != nil {
		return c.rt.Close()
	}
	return nil
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
