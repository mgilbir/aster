// A module resolution hook that hides the optional `canvas` package
// (node-canvas) from what imports it, as if it were not installed: vega then
// measures text with its width estimate and keeps an image's URL rather than
// loading it with node-canvas's Image (which forgets a remote URL). The
// generators whose recordings the Go tests replay with the estimate register
// it before importing vega:
//
//   register('<relative path to this file>', import.meta.url);
export async function resolve(specifier, context, next) {
  if (specifier === 'canvas') {
    const e = new Error("Cannot find package 'canvas' (hidden by no-canvas-hook.mjs)");
    e.code = 'ERR_MODULE_NOT_FOUND';
    throw e;
  }
  return next(specifier, context);
}
