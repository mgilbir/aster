// Records bit-exact results of V8's Math functions on pseudo-random inputs, to
// check how closely Go's math package tracks them.
//   node testdata/gen_math.mjs | gzip -9n > testdata/math_bits.json.gz
const f64 = new Float64Array(1), u64 = new BigUint64Array(f64.buffer);
const bits = x => { f64[0] = x; return u64[0].toString(16); };
let seed = 12345;
const rnd = () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648; };
const out = {};
const gen = (name, n, sample, tag = name) => {
  out[name] = out[name] || [];
  for (let i = 0; i < n; i++) {
    const args = sample();
    out[name].push([...args.map(bits), bits(Math[name](...args))]);
  }
};
const range = (a, b) => a + (b - a) * rnd();
for (const [name, lo, hi] of [['sin', -100, 100], ['cos', -100, 100], ['tan', -10, 10], ['exp', -50, 50], ['log', 0, 1000], ['atan', -50, 50], ['asin', -1, 1], ['acos', -1, 1]]) gen(name, 2000, () => [range(lo, hi)]);
gen('sin', 300, () => [range(-1e6, 1e6)]);
gen('cos', 300, () => [range(-1e6, 1e6)]);
gen('log', 300, () => [Math.exp(range(-30, 30))]);
gen('exp', 300, () => [range(-700, 700)]);
gen('atan2', 2000, () => [range(-10, 10), range(-10, 10)]);
gen('pow', 4000, () => [range(0, 20), range(-10, 10)]);
gen('pow', 1000, () => [range(-20, 20), Math.round(range(-10, 10))]);
gen('pow', 1000, () => [range(0.9, 1.1), range(-1e9, 1e9)]);
gen('pow', 500, () => [range(0, 1e5), range(0, 4)]);
gen('pow', 500, () => [range(1, 10), 1 / range(1, 5)]);
gen('pow', 300, () => [Math.exp(range(-50, 50)), range(-3, 3)]);
gen('pow', 300, () => [10, Math.round(range(-30, 30))]);
gen('pow', 300, () => [2, range(-1074, 1023)]);
process.stdout.write(JSON.stringify(out) + '\n');
