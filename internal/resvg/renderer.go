package resvg

import (
	"context"
	"fmt"
	"math"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Font holds TTF font data to register with the renderer.
type Font struct {
	Data []byte
}

// Renderer renders SVG to PNG via resvg compiled to WASM.
type Renderer struct {
	runtime wazero.Runtime
	module  api.Module

	fnAllocMem   api.Function
	fnDeallocMem api.Function
	fnFontDBInit api.Function
	fnFontDBAdd  api.Function
	fnRender     api.Function
	fnResultPtr  api.Function
	fnResultLen  api.Function
	fnErrorPtr   api.Function
	fnErrorLen   api.Function
}

// New creates a Renderer, initializes the font database, and loads the given fonts.
func New(ctx context.Context, fonts []Font) (*Renderer, error) {
	rt := wazero.NewRuntime(ctx)

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("resvg: instantiating WASI: %w", err)
	}

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("resvg: compiling WASM module: %w", err)
	}

	cfg := wazero.NewModuleConfig().
		WithName("resvg").
		WithStartFunctions("_initialize")

	mod, err := rt.InstantiateModule(ctx, compiled, cfg)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("resvg: instantiating module: %w", err)
	}

	r := &Renderer{
		runtime:      rt,
		module:       mod,
		fnAllocMem:   mod.ExportedFunction("alloc_mem"),
		fnDeallocMem: mod.ExportedFunction("dealloc_mem"),
		fnFontDBInit: mod.ExportedFunction("font_db_init"),
		fnFontDBAdd:  mod.ExportedFunction("font_db_add"),
		fnRender:     mod.ExportedFunction("render"),
		fnResultPtr:  mod.ExportedFunction("result_ptr"),
		fnResultLen:  mod.ExportedFunction("result_len"),
		fnErrorPtr:   mod.ExportedFunction("error_ptr"),
		fnErrorLen:   mod.ExportedFunction("error_len"),
	}

	// Validate all exports exist.
	exports := map[string]api.Function{
		"alloc_mem":    r.fnAllocMem,
		"dealloc_mem":  r.fnDeallocMem,
		"font_db_init": r.fnFontDBInit,
		"font_db_add":  r.fnFontDBAdd,
		"render":       r.fnRender,
		"result_ptr":   r.fnResultPtr,
		"result_len":   r.fnResultLen,
		"error_ptr":    r.fnErrorPtr,
		"error_len":    r.fnErrorLen,
	}
	for name, fn := range exports {
		if fn == nil {
			rt.Close(ctx)
			return nil, fmt.Errorf("resvg: missing WASM export: %s", name)
		}
	}

	// Initialize font database.
	if _, err := r.fnFontDBInit.Call(ctx); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("resvg: font_db_init: %w", err)
	}

	// Load fonts.
	for i, f := range fonts {
		if err := r.addFont(ctx, f.Data); err != nil {
			rt.Close(ctx)
			return nil, fmt.Errorf("resvg: loading font %d: %w", i, err)
		}
	}

	return r, nil
}

// addFont writes font data into WASM memory and registers it.
func (r *Renderer) addFont(ctx context.Context, data []byte) error {
	size := uint64(len(data))

	results, err := r.fnAllocMem.Call(ctx, size)
	if err != nil {
		return fmt.Errorf("alloc: %w", err)
	}
	ptr := results[0]

	if !r.module.Memory().Write(uint32(ptr), data) {
		r.fnDeallocMem.Call(ctx, ptr, size)
		return fmt.Errorf("write font data: out of bounds")
	}

	results, err = r.fnFontDBAdd.Call(ctx, ptr, size)
	if err != nil {
		r.fnDeallocMem.Call(ctx, ptr, size)
		return fmt.Errorf("font_db_add: %w", err)
	}

	r.fnDeallocMem.Call(ctx, ptr, size)

	if int32(results[0]) < 0 {
		return fmt.Errorf("font_db_add: %s", r.readError(ctx))
	}

	return nil
}

// Render converts SVG bytes to PNG at the given scale factor.
func (r *Renderer) Render(ctx context.Context, svg []byte, scale float64) ([]byte, error) {
	size := uint64(len(svg))

	results, err := r.fnAllocMem.Call(ctx, size)
	if err != nil {
		return nil, fmt.Errorf("resvg: alloc: %w", err)
	}
	svgPtr := results[0]

	if !r.module.Memory().Write(uint32(svgPtr), svg) {
		r.fnDeallocMem.Call(ctx, svgPtr, size)
		return nil, fmt.Errorf("resvg: write SVG data: out of bounds")
	}

	scaleBits := math.Float64bits(scale)
	results, err = r.fnRender.Call(ctx, svgPtr, size, scaleBits)
	if err != nil {
		r.fnDeallocMem.Call(ctx, svgPtr, size)
		return nil, fmt.Errorf("resvg: render: %w", err)
	}

	r.fnDeallocMem.Call(ctx, svgPtr, size)

	if int32(results[0]) < 0 {
		return nil, fmt.Errorf("resvg: %s", r.readError(ctx))
	}

	return r.readResult(ctx)
}

// readResult reads the PNG result buffer from WASM memory.
func (r *Renderer) readResult(ctx context.Context) ([]byte, error) {
	ptrResults, err := r.fnResultPtr.Call(ctx)
	if err != nil {
		return nil, fmt.Errorf("result_ptr: %w", err)
	}
	lenResults, err := r.fnResultLen.Call(ctx)
	if err != nil {
		return nil, fmt.Errorf("result_len: %w", err)
	}

	ptr := uint32(ptrResults[0])
	length := uint32(lenResults[0])

	if length == 0 {
		return nil, fmt.Errorf("empty result")
	}

	data, ok := r.module.Memory().Read(ptr, length)
	if !ok {
		return nil, fmt.Errorf("read result: out of bounds")
	}

	// Copy the data since the WASM memory view may be invalidated.
	out := make([]byte, length)
	copy(out, data)
	return out, nil
}

// readError reads the error message from WASM memory.
func (r *Renderer) readError(ctx context.Context) string {
	ptrResults, err := r.fnErrorPtr.Call(ctx)
	if err != nil {
		return "failed to read error pointer"
	}
	lenResults, err := r.fnErrorLen.Call(ctx)
	if err != nil {
		return "failed to read error length"
	}

	ptr := uint32(ptrResults[0])
	length := uint32(lenResults[0])

	if length == 0 {
		return "unknown error"
	}

	data, ok := r.module.Memory().Read(ptr, length)
	if !ok {
		return "error message out of bounds"
	}

	return string(data)
}

// Close releases all resources held by the Renderer.
func (r *Renderer) Close(ctx context.Context) error {
	if r.runtime != nil {
		return r.runtime.Close(ctx)
	}
	return nil
}
