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

// Override text measurement if Go provides it.
if (typeof __aster_measure_text === "function") {
  // vega.textMetrics is the module-level object used by the scenegraph.
  if (vega.textMetrics) {
    const origWidth = vega.textMetrics.width;
    vega.textMetrics.width = function (item, text) {
      if (text == null || text === "") return 0;
      const str = String(text);
      // Build a CSS font string from the item properties.
      const fontSize = item.fontSize || 11;
      const fontFamily = item.font || "sans-serif";
      const fontStyle = item.fontStyle || "normal";
      const fontWeight = item.fontWeight || "normal";
      const cssFont =
        fontStyle + " " + fontWeight + " " + fontSize + "px " + fontFamily;
      try {
        return __aster_measure_text(str, cssFont);
      } catch (e) {
        // Fall back to Vega's default estimation if Go measurement fails.
        if (typeof origWidth === "function") {
          return origWidth(item, text);
        }
        return str.length * fontSize * 0.6;
      }
    };
  }
}

/**
 * Compile a Vega-Lite spec to a Vega spec.
 * @param {string} specJSON - Vega-Lite spec as JSON string
 * @returns {string} - Vega spec as JSON string
 */
export function vegaLiteToVega(specJSON) {
  const vlSpec = JSON.parse(specJSON);
  const vgSpec = vegaLite.compile(vlSpec).spec;
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
  const vgSpecJSON = vegaLiteToVega(specJSON);
  return await vegaToSvg(vgSpecJSON, theme);
}
