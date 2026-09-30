// Evaluates a Math function in V8 over binary float64 arguments.
//
// Usage: node eval_v8.mjs <name> < args.bin > results.bin
//
// <name> is a property of Math, or "pow**" for the exponentiation operator. The
// input is a sequence of little-endian float64 argument tuples (one value per
// tuple for unary functions, two for pow); the output is one little-endian
// float64 result per tuple. The Go tests (vectors_test.go) generate the
// arguments, so the JavaScript side stays a plain evaluator.
import { readFileSync, writeSync } from 'node:fs';

const name = process.argv[2];
const arity = name === 'pow' || name === 'pow**' || name === 'atan2' || name === 'hypot' ? 2 : 1;
const buf = readFileSync(0);
const args = new Float64Array(buf.buffer, buf.byteOffset, buf.length / 8);
const n = args.length / arity;
const out = new Float64Array(n);

let f;
if (name === 'pow**') {
  // Both operands are runtime values so the compiler cannot fold the power.
  f = (x, y) => x ** y;
} else {
  f = Math[name];
}
if (arity === 1) {
  for (let i = 0; i < n; i++) out[i] = f(args[i]);
} else {
  for (let i = 0; i < n; i++) out[i] = f(args[2 * i], args[2 * i + 1]);
}
const bytes = Buffer.from(out.buffer);
for (let off = 0; off < bytes.length;) off += writeSync(1, bytes, off);
