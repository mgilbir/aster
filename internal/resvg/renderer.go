package resvg

import (
	"context"
	"fmt"
	"math"

	"github.com/mgilbir/andsifr"
	"github.com/mgilbir/andsifr/api"
	"github.com/mgilbir/andsifr/imports/wasi_snapshot_preview1"
)

// Font holds TTF font data to register with the renderer.
type Font struct {
	Data []byte
}

// FamilyMapping maps a generic CSS font family (e.g. "sans-serif") to a
// specific loaded font family name (e.g. "Liberation Sans"). All five CSS
// generic families are covered so PNG rasterization agrees with the text
// measurement pipeline on how each resolves.
type FamilyMapping struct {
	SansSerif string
	Serif     string
	Monospace string
	Cursive   string
	Fantasy   string
}

// Renderer renders SVG to PNG via resvg compiled to WASM.
type Renderer struct {
	runtime wazero.Runtime
	module  api.Module
	dead    bool // set after a WASM trap; the guest state can no longer be trusted

	fnAllocMem           api.Function
	fnDeallocMem         api.Function
	fnFontDBInit         api.Function
	fnFontDBAdd          api.Function
	fnFontDBSetSansSerif api.Function
	fnFontDBSetSerif     api.Function
	fnFontDBSetMonospace api.Function
	fnFontDBSetCursive   api.Function
	fnFontDBSetFantasy   api.Function
	fnRender             api.Function
	fnResultPtr          api.Function
	fnResultLen          api.Function
	fnErrorPtr           api.Function
	fnErrorLen           api.Function
}

// New creates a Renderer, initializes the font database, loads the given fonts,
// and configures generic font family mappings.
func New(ctx context.Context, fonts []Font, families FamilyMapping) (*Renderer, error) {
	rt := wazero.NewRuntime(ctx)

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("resvg: instantiating WASI: %w", err)
	}

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("resvg: compiling WASM module: %w", err)
	}

	cfg := wazero.NewModuleConfig().
		WithName("resvg").
		WithStartFunctions("_initialize")

	mod, err := rt.InstantiateModule(ctx, compiled, cfg)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("resvg: instantiating module: %w", err)
	}

	r := &Renderer{
		runtime:              rt,
		module:               mod,
		fnAllocMem:           mod.ExportedFunction("alloc_mem"),
		fnDeallocMem:         mod.ExportedFunction("dealloc_mem"),
		fnFontDBInit:         mod.ExportedFunction("font_db_init"),
		fnFontDBAdd:          mod.ExportedFunction("font_db_add"),
		fnFontDBSetSansSerif: mod.ExportedFunction("font_db_set_sans_serif"),
		fnFontDBSetSerif:     mod.ExportedFunction("font_db_set_serif"),
		fnFontDBSetMonospace: mod.ExportedFunction("font_db_set_monospace"),
		fnFontDBSetCursive:   mod.ExportedFunction("font_db_set_cursive"),
		fnFontDBSetFantasy:   mod.ExportedFunction("font_db_set_fantasy"),
		fnRender:             mod.ExportedFunction("render"),
		fnResultPtr:          mod.ExportedFunction("result_ptr"),
		fnResultLen:          mod.ExportedFunction("result_len"),
		fnErrorPtr:           mod.ExportedFunction("error_ptr"),
		fnErrorLen:           mod.ExportedFunction("error_len"),
	}

	// Validate all exports exist.
	exports := map[string]api.Function{
		"alloc_mem":              r.fnAllocMem,
		"dealloc_mem":            r.fnDeallocMem,
		"font_db_init":           r.fnFontDBInit,
		"font_db_add":            r.fnFontDBAdd,
		"font_db_set_sans_serif": r.fnFontDBSetSansSerif,
		"font_db_set_serif":      r.fnFontDBSetSerif,
		"font_db_set_monospace":  r.fnFontDBSetMonospace,
		"font_db_set_cursive":    r.fnFontDBSetCursive,
		"font_db_set_fantasy":    r.fnFontDBSetFantasy,
		"render":                 r.fnRender,
		"result_ptr":             r.fnResultPtr,
		"result_len":             r.fnResultLen,
		"error_ptr":              r.fnErrorPtr,
		"error_len":              r.fnErrorLen,
	}
	for name, fn := range exports {
		if fn == nil {
			_ = rt.Close(ctx)
			return nil, fmt.Errorf("resvg: missing WASM export: %s", name)
		}
	}

	// Initialize font database.
	if _, err := r.fnFontDBInit.Call(ctx); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("resvg: font_db_init: %w", err)
	}

	// Load fonts.
	for i, f := range fonts {
		if err := r.addFont(ctx, f.Data); err != nil {
			_ = rt.Close(ctx)
			return nil, fmt.Errorf("resvg: loading font %d: %w", i, err)
		}
	}

	// Configure generic font family mappings.
	if families.SansSerif != "" {
		if err := r.setFamily(ctx, r.fnFontDBSetSansSerif, families.SansSerif); err != nil {
			_ = rt.Close(ctx)
			return nil, fmt.Errorf("resvg: set sans-serif family: %w", err)
		}
	}
	if families.Serif != "" {
		if err := r.setFamily(ctx, r.fnFontDBSetSerif, families.Serif); err != nil {
			_ = rt.Close(ctx)
			return nil, fmt.Errorf("resvg: set serif family: %w", err)
		}
	}
	if families.Monospace != "" {
		if err := r.setFamily(ctx, r.fnFontDBSetMonospace, families.Monospace); err != nil {
			_ = rt.Close(ctx)
			return nil, fmt.Errorf("resvg: set monospace family: %w", err)
		}
	}
	if families.Cursive != "" {
		if err := r.setFamily(ctx, r.fnFontDBSetCursive, families.Cursive); err != nil {
			_ = rt.Close(ctx)
			return nil, fmt.Errorf("resvg: set cursive family: %w", err)
		}
	}
	if families.Fantasy != "" {
		if err := r.setFamily(ctx, r.fnFontDBSetFantasy, families.Fantasy); err != nil {
			_ = rt.Close(ctx)
			return nil, fmt.Errorf("resvg: set fantasy family: %w", err)
		}
	}

	return r, nil
}

// alloc reserves size bytes of guest memory, rejecting zero-size requests
// (undefined behavior in the guest allocator) and NULL results (allocation
// failure — without this check the caller would write at guest address 0,
// silently corrupting the guest's low memory).
func (r *Renderer) alloc(ctx context.Context, size uint64) (uint64, error) {
	if size == 0 {
		return 0, fmt.Errorf("alloc: zero-size allocation")
	}
	results, err := r.fnAllocMem.Call(ctx, size)
	if err != nil {
		return 0, fmt.Errorf("alloc: %w", err)
	}
	if results[0] == 0 {
		return 0, fmt.Errorf("alloc: guest allocation of %d bytes failed", size)
	}
	return results[0], nil
}

// setFamily writes a family name into WASM memory and calls the given setter function.
func (r *Renderer) setFamily(ctx context.Context, fn api.Function, name string) error {
	data := []byte(name)
	size := uint64(len(data))

	ptr, err := r.alloc(ctx, size)
	if err != nil {
		return err
	}

	if !r.module.Memory().Write(uint32(ptr), data) {
		_, _ = r.fnDeallocMem.Call(ctx, ptr, size)
		return fmt.Errorf("write family name: out of bounds")
	}

	results, err := fn.Call(ctx, ptr, size)
	if err != nil {
		_, _ = r.fnDeallocMem.Call(ctx, ptr, size)
		return fmt.Errorf("set family: %w", err)
	}

	_, _ = r.fnDeallocMem.Call(ctx, ptr, size)

	if int32(results[0]) < 0 {
		return fmt.Errorf("set family: %s", r.readError(ctx))
	}

	return nil
}

// addFont writes font data into WASM memory and registers it.
func (r *Renderer) addFont(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("font_db_add: empty font data")
	}
	size := uint64(len(data))

	ptr, err := r.alloc(ctx, size)
	if err != nil {
		return err
	}

	if !r.module.Memory().Write(uint32(ptr), data) {
		_, _ = r.fnDeallocMem.Call(ctx, ptr, size)
		return fmt.Errorf("write font data: out of bounds")
	}

	results, err := r.fnFontDBAdd.Call(ctx, ptr, size)
	if err != nil {
		_, _ = r.fnDeallocMem.Call(ctx, ptr, size)
		return fmt.Errorf("font_db_add: %w", err)
	}

	_, _ = r.fnDeallocMem.Call(ctx, ptr, size)

	if int32(results[0]) < 0 {
		return fmt.Errorf("font_db_add: %s", r.readError(ctx))
	}

	return nil
}

// Render converts SVG bytes to PNG at the given scale factor.
func (r *Renderer) Render(ctx context.Context, svg []byte, scale float64) ([]byte, error) {
	if r.dead {
		return nil, fmt.Errorf("resvg: renderer is unusable after a previous WASM failure; create a new Converter")
	}
	if len(svg) == 0 {
		return nil, fmt.Errorf("resvg: empty SVG input")
	}
	size := uint64(len(svg))

	svgPtr, err := r.alloc(ctx, size)
	if err != nil {
		return nil, fmt.Errorf("resvg: %w", err)
	}

	if !r.module.Memory().Write(uint32(svgPtr), svg) {
		_, _ = r.fnDeallocMem.Call(ctx, svgPtr, size)
		return nil, fmt.Errorf("resvg: write SVG data: out of bounds")
	}

	scaleBits := math.Float64bits(scale)
	results, err := r.fnRender.Call(ctx, svgPtr, size, scaleBits)
	if err != nil {
		// A Call error is a trap (or a closed module), not a graceful
		// renderer error: guest memory may be inconsistent, so refuse
		// further use instead of failing with opaque errors later.
		r.dead = true
		_, _ = r.fnDeallocMem.Call(ctx, svgPtr, size)
		return nil, fmt.Errorf("resvg: render: %w", err)
	}

	_, _ = r.fnDeallocMem.Call(ctx, svgPtr, size)

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
