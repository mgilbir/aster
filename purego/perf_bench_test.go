package purego_test

import (
	"context"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mgilbir/aster/purego"
)

// galleryBench are Vega gallery specs that stress different hot paths:
// contours, force layout, a map with many paths, stacks, hierarchies, dense
// scatter plots and heat maps.
var galleryBench = []string{
	"scatter-plot", "stacked-area-chart", "treemap", "county-unemployment",
	"force-directed-layout", "beeswarm-plot",
}

// memLoader answers from memory what the wrapped loader served once, so the
// benchmarks measure the engine and not the file system. It is safe for
// concurrent use.
type memLoader struct {
	inner purego.Loader
	mu    sync.Mutex
	data  map[string][]byte
}

func newMemLoader(inner purego.Loader) *memLoader {
	return &memLoader{inner: inner, data: map[string][]byte{}}
}

func (m *memLoader) Sanitize(ctx context.Context, uri string) (string, error) {
	return m.inner.Sanitize(ctx, uri)
}

func (m *memLoader) Load(ctx context.Context, uri string) ([]byte, error) {
	m.mu.Lock()
	b, ok := m.data[uri]
	m.mu.Unlock()
	if ok {
		return b, nil
	}
	b, err := m.inner.Load(ctx, uri)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.data[uri] = b
	m.mu.Unlock()
	return b, nil
}

func (m *memLoader) Close() error {
	if c, ok := m.inner.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

type benchCase struct {
	name string
	spec []byte
	lite bool
}

func benchCases(b *testing.B) []benchCase {
	var cs []benchCase
	for _, n := range benchSpecs {
		cs = append(cs, benchCase{"vl/" + n, benchSpec(b, n), true})
	}
	for _, n := range galleryBench {
		spec, err := os.ReadFile("testdata/corpus/vg-gallery/" + n + ".vg.json")
		if err != nil {
			b.Skip(err)
		}
		cs = append(cs, benchCase{"vg/" + n, spec, false})
	}
	return cs
}

func (bc benchCase) svg(c *purego.Converter) (string, error) {
	if bc.lite {
		return c.VegaLiteToSVG(bc.spec)
	}
	return c.VegaToSVG(bc.spec)
}

// BenchmarkRenderSVG renders the benchmark and gallery specs to SVG on a warm
// converter (purego only, no reference engine).
func BenchmarkRenderSVG(b *testing.B) {
	for _, bc := range benchCases(b) {
		b.Run(bc.name, func(b *testing.B) {
			c, err := purego.New(purego.WithLoader(newMemLoader(corpusLoader(b))))
			if err != nil {
				b.Fatal(err)
			}
			defer c.Close()
			if _, err := bc.svg(c); err != nil {
				b.Skip(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := bc.svg(c); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkParallel measures throughput (renders per second, "renders/s") of
// the same render mix run by 1, 4 and 10 goroutines, either sharing one
// Converter or with one Converter each.
func BenchmarkParallel(b *testing.B) {
	var cs []benchCase
	for _, bc := range benchCases(b) {
		switch bc.name {
		case "vl/bar", "vl/trellis_bar", "vg/scatter-plot", "vg/stacked-area-chart", "vg/treemap":
			cs = append(cs, bc)
		}
	}
	for _, mode := range []string{"shared", "own"} {
		for _, g := range []int{1, 4, 10} {
			b.Run(mode+"/"+itoa(g), func(b *testing.B) {
				ld := newMemLoader(corpusLoader(b))
				conv := make([]*purego.Converter, g)
				for i := range conv {
					if i > 0 && mode == "shared" {
						conv[i] = conv[0]
						continue
					}
					c, err := purego.New(purego.WithLoader(ld))
					if err != nil {
						b.Fatal(err)
					}
					conv[i] = c
					for _, bc := range cs {
						if _, err := bc.svg(c); err != nil {
							b.Fatal(err)
						}
					}
				}
				var next atomic.Int64
				var wg sync.WaitGroup
				b.ReportAllocs()
				b.ResetTimer()
				start := time.Now()
				for w := 0; w < g; w++ {
					wg.Add(1)
					go func(c *purego.Converter) {
						defer wg.Done()
						for {
							n := next.Add(1)
							if n > int64(b.N) {
								return
							}
							if _, err := cs[int(n)%len(cs)].svg(c); err != nil {
								b.Error(err)
								return
							}
						}
					}(conv[w])
				}
				wg.Wait()
				b.ReportMetric(float64(b.N)/time.Since(start).Seconds(), "renders/s")
				runtime.KeepAlive(conv)
			})
		}
	}
}

func itoa(n int) string {
	if n == 1 {
		return "1"
	} else if n == 4 {
		return "4"
	}
	return "10"
}
