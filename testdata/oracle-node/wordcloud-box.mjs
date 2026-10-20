// A deterministic canvas for upstream's wordcloud layout (vega-wordcloud's
// CloudLayout.js), shared by the oracle (oracle.mjs, the "wordcloud": "box"
// request option) and the golden layouts of internal/transforms/wordcloud
// (testdata/gen_wordcloud.mjs).
//
// d3-cloud places words by the pixels a canvas rasterizes them to, and
// node-canvas rasterizes glyphs differently on each platform (its Pango and
// Cairo), so a word cloud compared through node-canvas is checked on one
// platform at most. With this canvas text measures 0.6 em per code point and
// every word paints a solid (rotated) box 0.8 em above and 0.2 em below its
// baseline, the stroke (padding) growing it by half the line width; the
// engine's wordcloud.BoxRenderer implements the same model, so both sides
// lay the same words out at the same places on every platform, and what is
// compared is the transform: how its parameters reach the layout, and the
// tuples it writes.

// boxContext is a 2D context for a canvas of width × height. A canvas of at
// most one pixel wide has no pixels (CloudLayout reads one pixel of it to
// learn the device ratio, which is then 1).
export function boxContext(width, height) {
  let px = null;
  const W = width,
    H = height;
  if (W > 1) px = new Uint8Array(W * H * 4);
  return {
    fillStyle: '', strokeStyle: '', textAlign: '', lineWidth: 1, font: '',
    tx: 0, ty: 0, ang: 0, stack: [],
    save() { this.stack.push([this.tx, this.ty, this.ang, this.font, this.lineWidth]); },
    restore() { [this.tx, this.ty, this.ang, this.font, this.lineWidth] = this.stack.pop(); },
    clearRect() { if (px) px.fill(0); },
    translate(x, y) { this.tx += x; this.ty += y; },
    rotate(a) { this.ang += a; },
    fontPx() { return +/(\d+)px/.exec(this.font)[1]; },
    // a canvas draws and measures the text of any value: String(t)
    measureText(t) { return { width: [...String(t)].length * 0.6 * this.fontPx() }; },
    box(t, grow) {
      const p = this.fontPx(), half = [...String(t)].length * 0.6 * p / 2;
      const x0 = -half - grow, x1 = half + grow, y0 = -0.8 * p - grow, y1 = 0.2 * p + grow;
      const s = Math.sin(this.ang), c = Math.cos(this.ang);
      let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
      for (const [a, b] of [[x0, y0], [x1, y0], [x0, y1], [x1, y1]]) {
        const gx = this.tx + c * a - s * b, gy = this.ty + s * a + c * b;
        minX = Math.min(minX, gx); maxX = Math.max(maxX, gx);
        minY = Math.min(minY, gy); maxY = Math.max(maxY, gy);
      }
      const ix0 = Math.max(Math.floor(minX) - 1, 0), ix1 = Math.min(Math.ceil(maxX) + 1, W - 1);
      const iy0 = Math.max(Math.floor(minY) - 1, 0), iy1 = Math.min(Math.ceil(maxY) + 1, H - 1);
      for (let y = iy0; y <= iy1; y++) for (let x = ix0; x <= ix1; x++) {
        const dx = x + 0.5 - this.tx, dy = y + 0.5 - this.ty;
        const lx = c * dx + s * dy, ly = c * dy - s * dx;
        if (lx >= x0 && lx < x1 && ly >= y0 && ly < y1) px[(y * W + x) * 4] = 255;
      }
    },
    fillText(t) { this.box(t, 0); },
    strokeText(t) { this.box(t, this.lineWidth / 2); },
    getImageData(x, y, w, h) {
      if (w === 1 && h === 1 && !px) return { data: new Uint8Array(4) };
      return { data: px };
    },
  };
}

// boxCanvas is a canvas whose context is a boxContext, made when its size is
// set (CloudLayout sets the width, then the height, then asks for the
// context).
export function boxCanvas() {
  const cv = { width: 0, height: 0 };
  let ctx = boxContext(0, 0);
  return {
    get width() { return cv.width; }, set width(v) { cv.width = v; },
    get height() { return cv.height; },
    set height(v) { cv.height = v; ctx = boxContext(cv.width, v); },
    getContext() { return ctx; },
  };
}
