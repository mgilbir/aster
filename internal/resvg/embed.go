// Package resvg renders SVG to PNG using the resvg library compiled to WASM.
package resvg

import _ "embed"

//go:embed resvg.wasm
var wasmBytes []byte
