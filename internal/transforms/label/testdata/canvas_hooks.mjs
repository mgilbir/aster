// Module resolution hook that supplies the optional `canvas` package vega
// probes for. The stand-in has just enough of a 2D context to let vega-label
// rasterize marks: paths are filled with the even-odd rule at pixel centres
// and stroked as the set of pixels whose centre is within lineWidth/2 of a
// segment. The Go test rasterizer implements the same two rules.
const source = `
class Ctx {
  constructor(w, h) {
    this.w = w; this.h = h;
    this.buf = new Uint8ClampedArray(w * h * 4);
    this.subs = [];
    this.lineWidth = 1;
  }
  beginPath() { this.subs = []; }
  moveTo(x, y) { this.subs.push({pts: [[x, y]], closed: false}); }
  lineTo(x, y) {
    if (!this.subs.length) return this.moveTo(x, y);
    this.subs[this.subs.length - 1].pts.push([x, y]);
  }
  closePath() { if (this.subs.length) this.subs[this.subs.length - 1].closed = true; }
  rect(x, y, w, h) {
    this.subs.push({pts: [[x, y], [x + w, y], [x + w, y + h], [x, y + h]], closed: true});
  }
  paint(x, y) {
    if (x >= 0 && y >= 0 && x < this.w && y < this.h) this.buf[(y * this.w + x) * 4 + 3] = 255;
  }
  fill() {
    for (let y = 0; y < this.h; ++y) for (let x = 0; x < this.w; ++x) {
      const px = x + 0.5, py = y + 0.5;
      let inside = false;
      for (const s of this.subs) {
        const p = s.pts, n = p.length;
        for (let i = 0, j = n - 1; i < n; j = i++) {
          const [xi, yi] = p[i], [xj, yj] = p[j];
          if ((yi > py) !== (yj > py) && px < (xj - xi) * (py - yi) / (yj - yi) + xi) inside = !inside;
        }
      }
      if (inside) this.paint(x, y);
    }
  }
  stroke() {
    const r = this.lineWidth / 2;
    for (let y = 0; y < this.h; ++y) for (let x = 0; x < this.w; ++x) {
      const px = x + 0.5, py = y + 0.5;
      let hit = false;
      for (const s of this.subs) {
        const p = s.pts, n = p.length, m = s.closed ? n : n - 1;
        for (let i = 0; i < m && !hit; ++i) {
          const a = p[i], b = p[(i + 1) % n];
          const dx = b[0] - a[0], dy = b[1] - a[1], l2 = dx * dx + dy * dy;
          let t = l2 === 0 ? 0 : ((px - a[0]) * dx + (py - a[1]) * dy) / l2;
          t = Math.max(0, Math.min(1, t));
          const ex = a[0] + t * dx - px, ey = a[1] + t * dy - py;
          if (Math.sqrt(ex * ex + ey * ey) <= r) hit = true;
        }
      }
      if (hit) this.paint(x, y);
    }
  }
  getImageData() { return {data: this.buf}; }
  measureText(t) { return {width: t.length * 6}; }
  setLineDash() {}
  save() {} restore() {}
}
export class Canvas {
  constructor(w, h) { this.width = w; this.height = h; this.ctx = new Ctx(w, h); }
  getContext() { return this.ctx; }
}
export class Image {}
export const createCanvas = (w, h) => new Canvas(w, h);
`;
export async function resolve(specifier, context, next) {
  if (specifier === 'canvas') {
    return {url: 'data:text/javascript,' + encodeURIComponent(source), shortCircuit: true};
  }
  return next(specifier, context);
}
