// bridge.js — Wires Vega/Vega-Lite to Go-provided callbacks.
// This file is committed and embedded into the binary.
//
// Go registers these globals before this module loads:
//   __aster_load(url)          → async, returns string (or throws)
//   __aster_sanitize(uri)      → sync, returns sanitized string (or throws)
//   __aster_measure_text(text, font) → sync, returns number (width in px)

import * as vega from "vega";
import * as vegaLite from "vega-lite";
import { resetSVGDefIds } from "vega-scenegraph";

// Create a custom Vega loader that delegates to Go callbacks.
function createLoader() {
  const loader = vega.loader();

  // Override http to use Go's loader.
  loader.http = async function (url, options) {
    if (typeof __aster_load !== "function") {
      throw new Error("aster: resource loading denied (no loader configured)");
    }
    return await __aster_load(url);
  };

  // Override file to always deny (Go controls all I/O).
  loader.file = async function (filename) {
    throw new Error(
      "aster: file loading denied: " + filename + " (use a Loader to allow access)",
    );
  };

  // Override sanitize to use Go's sanitizer.
  const origSanitize = loader.sanitize.bind(loader);
  loader.sanitize = async function (uri, options) {
    if (typeof __aster_sanitize === "function") {
      const sanitized = __aster_sanitize(uri);
      return { href: sanitized };
    }
    return origSanitize(uri, options);
  };

  return loader;
}

// Text measurement: the runtime's polyfills give Vega a measuring canvas
// context backed by __aster_measure_text, so Vega measures text through its
// own canvas path (see installPolyfills in internal/runtime).

/**
 * Compile a Vega-Lite spec to a Vega spec.
 *
 * The theme config must be applied here, at compile time: the Vega-Lite
 * compiler consumes config keys like `background` and `view.continuousWidth`
 * and bakes their resolved values into the emitted Vega spec, where they
 * shadow any config passed later to vega.parse. Applying the theme only at
 * parse time silently drops those keys.
 *
 * @param {string} specJSON - Vega-Lite spec as JSON string
 * @param {string} [theme] - Optional Vega theme config JSON
 * @returns {string} - Vega spec as JSON string
 */
export function vegaLiteToVega(specJSON, theme) {
  const vlSpec = JSON.parse(specJSON);
  const opts = theme ? { config: JSON.parse(theme) } : undefined;
  const vgSpec = vegaLite.compile(vlSpec, opts).spec;
  return JSON.stringify(vgSpec);
}

/**
 * Render a Vega spec to SVG.
 * @param {string} specJSON - Vega spec as JSON string
 * @param {string} [theme] - Optional Vega theme config JSON
 * @returns {Promise<string>} - SVG string
 */
export async function vegaToSvg(specJSON, theme) {
  // Reset clip-path/gradient ID counters so each render produces
  // deterministic IDs regardless of how many renders preceded it.
  resetSVGDefIds();
  // Seed Vega's random source per render so sample, jitter and bootstrap
  // confidence intervals are reproducible. 123456789 is the seed Vega-Lite's
  // own example renders use (vg2svg --seed 123456789).
  vega.setRandom(vega.randomLCG(123456789));

  const spec = JSON.parse(specJSON);
  const loader = createLoader();

  const runtimeOpts = {};
  if (theme) {
    runtimeOpts.config = JSON.parse(theme);
  }

  const runtime = vega.parse(spec, runtimeOpts.config);
  const view = new vega.View(runtime, {
    renderer: "none",
    loader: loader,
  });

  try {
    const svg = await view.toSVG();
    return svg;
  } finally {
    view.finalize();
  }
}

/**
 * Render a Vega-Lite spec directly to SVG.
 * @param {string} specJSON - Vega-Lite spec as JSON string
 * @param {string} [theme] - Optional Vega theme config JSON
 * @returns {Promise<string>} - SVG string
 */
export async function vegaLiteToSvg(specJSON, theme) {
  // Theme goes to both stages: compile-time for the keys the Vega-Lite
  // compiler consumes (see vegaLiteToVega), parse-time for the rest. Keys
  // applied at compile time win at parse time (explicit spec values shadow
  // parse config), so the double application is safe.
  const vgSpecJSON = vegaLiteToVega(specJSON, theme);
  return await vegaToSvg(vgSpecJSON, theme);
}
