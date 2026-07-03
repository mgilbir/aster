// Package quickjs runs the QuickJS-NG JavaScript engine compiled to a WASI
// reactor WebAssembly module, driven from Go via wazero. The wasm binary is
// built from the pinned upstream QuickJS-NG release by quickjs-wasm/
// (Docker, `make vendor-quickjs`), with LTO, an 8MB stack-first layout, and
// the WASI stack-overflow guard re-enabled (see quickjs-wasm/stack-guard.patch).
package quickjs

import _ "embed"

// Version is the QuickJS-NG release tag the embedded binary is built from.
// Keep in sync with QUICKJS_NG_VERSION in quickjs-wasm/Dockerfile.
const Version = "v0.15.1"

//go:embed quickjs.wasm
var wasmBytes []byte
