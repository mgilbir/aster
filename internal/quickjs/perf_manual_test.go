package quickjs

import (
	"testing"
	"time"
)

// Manual microbenchmarks comparing engine hot spots. Run explicitly:
//   go test ./internal/quickjs/ -run TestPerfProbe -v
func TestPerfProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("manual probe")
	}
	r := newRuntime(t, Config{})

	probes := map[string]string{
		"num-format": `let n=0; for(let i=0;i<300000;i++){ n += String(0.1234567*i).length; } export default String(n);`,
		"math-loop":  `let n=0; for(let i=0;i<3000000;i++){ n += Math.sqrt(i)*Math.sin(i); } export default String(n|0);`,
		"str-concat": `let s=""; for(let i=0;i<100000;i++){ s += "M1.234,5.678L9.876,5.432Z"; } export default String(s.length);`,
		"json-parse": `const o = {a:[]}; for(let i=0;i<20000;i++) o.a.push({x:i*0.1,y:i*0.2}); const j = JSON.stringify(o); let n=0; for(let i=0;i<20;i++){ n += JSON.parse(j).a.length; } export default String(n);`,
	}
	for name, code := range probes {
		start := time.Now()
		out, err := r.EvalModule(code)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%-12s %10v (result %s)", name, time.Since(start), out)
	}
}
