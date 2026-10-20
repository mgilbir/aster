// Package pngopt post-processes the PNGs the rasterizer renders: lossless
// recoding into the cheapest equivalent color format, and guarded lossy
// palette quantization. Both work on the image the renderer holds, under the
// call's context and ledger (QuantizeOrRecodeImage, RecodeImage). There is
// no form that decodes a PNG first: it would run outside both.
package pngopt
