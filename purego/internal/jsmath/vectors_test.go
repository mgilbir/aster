package jsmath

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"flag"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The vectors below pin the functions to V8's results. Arguments come from a
// fixed pseudo-random generator in this file; V8's answers are recorded by
// testdata/eval_v8.mjs. testdata/v8_<name>.bin.gz stores both as gzipped
// little-endian float64s: the argument tuples, then one result per tuple. The
// arguments are stored because the generator's float arithmetic (and Go's
// math.Exp/Log2) is not bit-identical across architectures.
//
//	go test ./purego/internal/jsmath -run TestV8Vectors -update    (re-record; needs node)
//	go test ./purego/internal/jsmath -run TestV8Live -live=200000  (fresh arguments, needs node)
var (
	update   = flag.Bool("update", false, "re-record the V8 vectors with node")
	liveN    = flag.Int("live", 0, "compare against node over this many fresh arguments per function")
	liveSeed = flag.Uint64("liveseed", 1, "seed for -live arguments")
)

const recordedCount = 20000

// vecFunc describes one function under test.
type vecFunc struct {
	name  string // property of Math (or "pow**")
	arity int
	f1    func(float64) float64
	f2    func(float64, float64) float64
	// special lists interesting arguments (thresholds) for the unary functions.
	special []float64
	// liveOnly functions already have recorded vectors elsewhere (math.json.gz);
	// they are only checked by -live.
	liveOnly bool
}

func (v *vecFunc) call(a []float64) float64 {
	if v.arity == 1 {
		return v.f1(a[0])
	}
	return v.f2(a[0], a[1])
}

var vecFuncs = []*vecFunc{
	{name: "pow", arity: 2, f2: Pow},
	{name: "exp", arity: 1, f1: Exp, special: []float64{709.782712893384, 709.7827128933841, -745.1332191019411, -745.1332191019412, -708.3964185322641, 0.5 * math.Ln2, 1.5 * math.Ln2, 0x1p-28, 0x1p-54, -0x1p-54, 1, -1}},
	{name: "expm1", arity: 1, f1: Expm1, special: []float64{709.782712893384, 56 * math.Ln2, -38.816242111356935, -37, 0.5 * math.Ln2, 1.5 * math.Ln2, 0x1p-54, 0x1p-55, 1, -1, 0.34657359027997264}},
	{name: "log", arity: 1, f1: Log, special: []float64{1, 0x1p-1022, 0x1p-1074, math.MaxFloat64, math.Sqrt2, math.Sqrt2 / 2, 1 + 0x1p-20, 1 - 0x1p-20, 2, 0.5, math.E}},
	{name: "log1p", arity: 1, f1: Log1p, special: []float64{-1, 0x1p-54, 0x1p-29, 0x1p-53, 0x1p53, 0x1p54, math.Sqrt2 - 1, -0.2928932188134525, -0.5, 1, 0.41421356237309503}},
	{name: "log2", arity: 1, f1: Log2, special: []float64{1, 2, 4, 8, 0.5, 1024, 0x1p-1022, 0x1p-1074, math.MaxFloat64, 10, 1000, math.Sqrt2}},
	{name: "log10", arity: 1, f1: Log10, special: []float64{1, 10, 100, 1000, 1e-5, 1e22, 1e23, 1e300, 0.1, 0.01, 0x1p-1074, math.MaxFloat64, 2}},
	{name: "cbrt", arity: 1, f1: Cbrt, special: []float64{1, 8, 27, 64, 125, 1000, -8, 0x1p-1074, 0x1p-1022, math.MaxFloat64, 0.001, 1e-9}},
	{name: "sinh", arity: 1, f1: Sinh, special: []float64{22, -22, 0x1p-28, 0x1p-29, 710.4758600739439, 709.7822265633563, 709.78, 1, 0.5, 1e-9}},
	{name: "cosh", arity: 1, f1: Cosh, special: []float64{22, -22, 0.5 * math.Ln2, 0x1p-55, 710.4758600739439, 709.7822265633563, 709.78, 1, 0.5, 1e-9}},
	{name: "tanh", arity: 1, f1: Tanh, special: []float64{22, -22, 1, 0x1p-55, 0x1p-28, 0.5, 20, 19.9, 1e-9}},
	{name: "asinh", arity: 1, f1: Asinh, special: []float64{0x1p28, 0x1p-28, 2, 1, 0.5, 1e-9, 1e300}},
	{name: "acosh", arity: 1, f1: Acosh, special: []float64{1, 2, 0x1p28, 0x1p29, 1.5, 1 + 0x1p-52, 1e300}},
	{name: "atanh", arity: 1, f1: Atanh, special: []float64{1, -1, 0.5, -0.5, 0x1p-28, 0x1p-29, 1 - 0x1p-53, 0.9999999999}},
	{name: "sin", arity: 1, f1: Sin, special: []float64{math.Pi / 4, math.Pi / 2, math.Pi, 2 * math.Pi, 0x1p-27, 1e5, 823549.6, 823550, 1e7, 1e10, 1e300, math.MaxFloat64}},
	{name: "cos", arity: 1, f1: Cos, special: []float64{math.Pi / 4, math.Pi / 2, math.Pi, 2 * math.Pi, 0x1p-27, 1e5, 823549.6, 823550, 1e7, 1e10, 1e300, math.MaxFloat64}},
	{name: "atan", arity: 1, liveOnly: true, f1: Atan, special: []float64{0.4375, 0.6875, 1.1875, 2.4375, 0x1p-27, 0x1p66, 1}},
	{name: "asin", arity: 1, liveOnly: true, f1: Asin, special: []float64{0.5, 0.975, 1, -1, 0x1p-27, 1 - 0x1p-53}},
	{name: "acos", arity: 1, liveOnly: true, f1: Acos, special: []float64{0.5, -0.5, 1, -1, 0x1p-57, 1 - 0x1p-53}},
	{name: "tan", arity: 1, f1: Tan, special: []float64{math.Pi / 4, math.Pi / 2, math.Pi, 3 * math.Pi / 2, 0x1p-28, 0.6743316650390625, 1e5, 1e6, 1e7, 1e10, 1e300, math.MaxFloat64}},
}

func vecFuncByName(n string) *vecFunc {
	for _, v := range vecFuncs {
		if v.name == n {
			return v
		}
	}
	return nil
}

// splitmix64 is a small, fixed generator so the recorded vectors stay
// reproducible across Go versions (math/rand's streams are not guaranteed).
type splitmix struct{ s uint64 }

func (r *splitmix) next() uint64 {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (r *splitmix) unit() float64  { return float64(r.next()>>11) / (1 << 53) }
func (r *splitmix) intn(n int) int { return int(r.next() % uint64(n)) }
func (r *splitmix) sign() float64 {
	if r.next()&1 == 0 {
		return 1
	}
	return -1
}

// logUniform returns a value whose binary exponent is uniform in [lo, hi].
func (r *splitmix) logUniform(lo, hi int) float64 {
	e := lo + r.intn(hi-lo+1)
	return math.Ldexp(1+r.unit(), e)
}

func nearby(r *splitmix, v float64) float64 {
	for k := r.intn(9) - 4; k != 0; {
		if k > 0 {
			v = math.Nextafter(v, math.Inf(1))
			k--
		} else {
			v = math.Nextafter(v, math.Inf(-1))
			k++
		}
	}
	return v
}

var specials = []float64{0, math.Copysign(0, -1), 1, -1, 2, -2, 0.5, -0.5, 10, 3, math.Inf(1), math.Inf(-1), math.NaN(), math.MaxFloat64, -math.MaxFloat64, 0x1p-1074, -0x1p-1074, 0x1p-1022}

// unaryArg draws one argument for a unary function.
func unaryArg(r *splitmix, v *vecFunc) float64 {
	switch c := r.intn(20); {
	case c == 0:
		return math.Float64frombits(r.next()) // any bit pattern
	case c == 1:
		return specials[r.intn(len(specials))]
	case c == 2:
		return nearby(r, r.sign()*v.special[r.intn(len(v.special))])
	case c == 3:
		return r.sign() * math.Ldexp(r.unit(), -1022) // subnormal
	case c <= 5:
		return r.sign() * r.logUniform(-1074+52, 1023) // full magnitude range
	case c == 6:
		return r.sign() * r.logUniform(-60, -1)
	case c == 7:
		return (r.unit()*2 - 1) // (-1, 1)
	case c == 8:
		return (r.unit()*2 - 1) * 4 // small
	case c == 9:
		return (r.unit()*2 - 1) * 40
	case c == 10:
		return (r.unit()*2 - 1) * 750 // exp/sinh range
	case c == 11:
		return float64(r.intn(2001) - 1000) // integers
	case c == 12:
		return float64(r.intn(2001)-1000) / 2 // halves
	case c == 13:
		return nearby(r, float64(r.intn(201)-100)) // near small integers
	case c == 14:
		return 1 + (r.unit()*2-1)*math.Ldexp(1, -r.intn(50)) // near 1
	case c == 15:
		return math.Pow(10, float64(r.intn(60)-30)) // powers of ten
	case c == 16:
		return r.unit() * 1e6
	case c == 17:
		return r.sign() * r.unit() * 1e-3
	default:
		return r.unit()*2 - 0.5 // around [0,1]
	}
}

// powArgs draws one (base, exponent) pair.
func powArgs(r *splitmix) (float64, float64) {
	switch c := r.intn(24); {
	case c == 0:
		return math.Float64frombits(r.next()), math.Float64frombits(r.next())
	case c == 1:
		return specials[r.intn(len(specials))], specials[r.intn(len(specials))]
	case c == 2: // hard decimal cases: 10^k, k an integer
		return 10, float64(r.intn(80) - 40)
	case c == 3: // 1.1**2.4 and friends
		return 1 + float64(r.intn(100))/10, float64(r.intn(100)-20) / 10
	case c == 4: // small integer powers of integers
		return float64(r.intn(1001)), float64(r.intn(60))
	case c == 5:
		return float64(r.intn(1001)) / float64(1+r.intn(100)), float64(r.intn(21) - 10)
	case c == 6: // negative bases, integer exponents
		return -r.logUniform(-20, 20), float64(r.intn(41) - 20)
	case c == 7: // negative bases, fractional exponents
		return -r.logUniform(-20, 20), r.unit()*20 - 10
	case c == 8: // near 1, huge exponent
		return 1 + (r.unit()*2-1)*math.Ldexp(1, -20-r.intn(30)), r.sign() * r.logUniform(20, 70)
	case c == 9: // overflow and underflow boundaries
		x := r.logUniform(-1074+52, 1023)
		y := r.sign() * (1000 + r.unit()*100) / math.Log2(x)
		return x, y
	case c == 10:
		return r.logUniform(-1074+52, 1023), r.sign() * r.logUniform(-3, 3)
	case c == 11: // roots
		return r.logUniform(-30, 30), []float64{0.5, 1.0 / 3, 0.25, 0.75, 1.5, 2.5, -0.5, 1.0 / 5, 2.0 / 3}[r.intn(9)]
	case c == 12:
		return r.unit() * 1000, r.unit()*40 - 20
	case c == 13:
		return r.unit() * 4, r.unit()*4 - 2
	case c == 14:
		return r.logUniform(-1074+52, 1023) * r.sign(), float64(r.intn(9) - 4)
	case c == 15: // subnormal bases
		return math.Ldexp(r.unit(), -1022), r.unit()*4 - 2
	case c == 16:
		return math.Pow(2, float64(r.intn(2000)-1000)), r.unit()*4 - 2
	case c == 17:
		return r.logUniform(-10, 10), float64(r.intn(2001)-1000) / 8
	case c == 18:
		return nearby(r, float64(r.intn(21)-10)), nearby(r, float64(r.intn(21)-10)/2)
	case c == 19:
		return r.unit(), r.sign() * r.logUniform(0, 9)
	case c == 20:
		return r.unit() * 2, float64(r.intn(400) - 200)
	case c == 21:
		return 2, r.unit()*2100 - 1075
	case c == 22:
		return math.Exp(r.unit()*20 - 10), r.unit()*100 - 50
	default:
		return r.unit()*20 - 10, float64(r.intn(21) - 10)
	}
}

func genArgs(v *vecFunc, seed uint64, n int) []float64 {
	r := &splitmix{s: seed*0x1234567 + uint64(len(v.name))*7919 + uint64(v.name[0])}
	out := make([]float64, 0, n*v.arity)
	for i := 0; i < n; i++ {
		if v.arity == 1 {
			out = append(out, unaryArg(r, v))
		} else {
			x, y := powArgs(r)
			out = append(out, x, y)
		}
	}
	return out
}

func vectorPath(name string) string {
	return filepath.Join("testdata", "v8_"+name+".bin.gz")
}

func floatsToBytes(f []float64) []byte {
	b := make([]byte, 8*len(f))
	for i, x := range f {
		binary.LittleEndian.PutUint64(b[8*i:], math.Float64bits(x))
	}
	return b
}

func bytesToFloats(b []byte) []float64 {
	f := make([]float64, len(b)/8)
	for i := range f {
		f[i] = math.Float64frombits(binary.LittleEndian.Uint64(b[8*i:]))
	}
	return f
}

// runNode evaluates the function in V8 over the given arguments.
func runNode(t testing.TB, v *vecFunc, args []float64) []float64 {
	cmd := exec.Command("node", "testdata/eval_v8.mjs", v.name)
	cmd.Stdin = bytes.NewReader(floatsToBytes(args))
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v", err)
	}
	return bytesToFloats(out.Bytes())
}

func sameFloat(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Float64bits(a) == math.Float64bits(b)
}

func compare(t *testing.T, v *vecFunc, args, want []float64) {
	t.Helper()
	bad, total := 0, len(want)
	for i := 0; i < total; i++ {
		a := args[i*v.arity : (i+1)*v.arity]
		got := v.call(a)
		if !sameFloat(got, want[i]) {
			bad++
			if bad <= 5 {
				t.Errorf("%s(%v) = %v (%#x), V8 %v (%#x)", v.name, a, got, math.Float64bits(got), want[i], math.Float64bits(want[i]))
			}
		}
	}
	if bad > 0 {
		t.Errorf("%s: %d of %d differ from V8", v.name, bad, total)
	}
}

func TestV8Vectors(t *testing.T) {
	for _, v := range vecFuncs {
		if v.liveOnly {
			continue
		}
		t.Run(v.name, func(t *testing.T) {
			if *update {
				args := genArgs(v, 0, recordedCount)
				res := runNode(t, v, args)
				var buf bytes.Buffer
				zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
				zw.Write(floatsToBytes(append(args, res...)))
				zw.Close()
				if err := os.WriteFile(vectorPath(v.name), buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			f, err := os.Open(vectorPath(v.name))
			if err != nil {
				t.Skipf("no recorded vectors: %v", err)
			}
			defer f.Close()
			zr, err := gzip.NewReader(f)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(zr)
			if err != nil {
				t.Fatal(err)
			}
			rec := bytesToFloats(raw)
			if len(rec) != recordedCount*(v.arity+1) {
				t.Fatalf("recorded %d values, want %d", len(rec), recordedCount*(v.arity+1))
			}
			args, want := rec[:recordedCount*v.arity], rec[recordedCount*v.arity:]
			compare(t, v, args, want)
		})
	}
}

// TestV8Live records fresh arguments with node on the fly; it is the wide net
// used while developing (-live=200000) and is skipped by default.
func TestV8Live(t *testing.T) {
	if *liveN == 0 {
		t.Skip("set -live=N to compare against node")
	}
	names := os.Getenv("JSMATH_FUNCS")
	for _, v := range vecFuncs {
		if names != "" && !bytes.Contains([]byte(","+names+","), []byte(","+v.name+",")) {
			continue
		}
		t.Run(v.name, func(t *testing.T) {
			args := genArgs(v, *liveSeed, *liveN)
			want := runNode(t, v, args)
			if dir := os.Getenv("JSMATH_DUMP"); dir != "" {
				os.WriteFile(filepath.Join(dir, v.name+".args"), floatsToBytes(args), 0o644)
				os.WriteFile(filepath.Join(dir, v.name+".v8"), floatsToBytes(want), 0o644)
			}
			compare(t, v, args, want)
		})
	}
}
