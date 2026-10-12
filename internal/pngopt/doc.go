// Package pngopt post-processes the PNGs the rasterizer renders: lossless
// recoding into the cheapest equivalent color format, and guarded lossy
// palette quantization. Both work on the image the renderer holds, under the
// call's context and ledger (QuantizeOrRecodeImage, RecodeImage); the
// byte-slice forms decode a PNG first and run unbounded, for tests and tools.
package pngopt
