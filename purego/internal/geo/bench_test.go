package geo

import (
	"testing"

	"github.com/mgilbir/aster/purego/internal/jsval"
)

func benchCountries(b *testing.B) (jsval.Value, jsval.Value) {
	b.Helper()
	world := loadTopology(b, "world-110m.json")
	return world, topoFeatureOf(b, world, "countries")
}

func benchPath(b *testing.B, typ string, fit bool) {
	_, countries := benchCountries(b)
	p, err := NewProjection(typ)
	if err != nil {
		b.Fatal(err)
	}
	if fit {
		p.FitSize(960, 500, countries)
	}
	path := p.Path()
	b.ReportAllocs()
	b.ResetTimer()
	n := 0
	for i := 0; i < b.N; i++ {
		s, _ := path.String(countries)
		n += len(s)
	}
	b.SetBytes(int64(n / b.N))
}

// Projecting and stringifying all of world-110m (177 countries, 10k points).
func BenchmarkWorldPathMercator(b *testing.B)     { benchPath(b, "mercator", true) }
func BenchmarkWorldPathNaturalEarth(b *testing.B) { benchPath(b, "naturalEarth1", true) }
func BenchmarkWorldPathOrthographic(b *testing.B) { benchPath(b, "orthographic", false) }
func BenchmarkWorldPathAlbers(b *testing.B)       { benchPath(b, "albers", false) }

func BenchmarkPerFeaturePath(b *testing.B) {
	_, countries := benchCountries(b)
	p, _ := NewProjection("naturalEarth1")
	p.FitSize(960, 500, countries)
	path := p.Path()
	feats := countries.Get("features").Items()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, f := range feats {
			path.String(f)
		}
	}
}

func BenchmarkUSStatesAlbersUsa(b *testing.B) {
	us := loadTopology(b, "us-10m.json")
	states := topoFeatureOf(b, us, "states")
	p, _ := NewProjection("albersUsa")
	path := p.Path()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path.String(states)
	}
}

func BenchmarkFitWorld(b *testing.B) {
	_, countries := benchCountries(b)
	p, _ := NewProjection("mercator")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.FitSize(960, 500, countries)
	}
}

func BenchmarkForward(b *testing.B) {
	p, _ := NewProjection("mercator")
	b.ReportAllocs()
	x := 0.0
	for i := 0; i < b.N; i++ {
		px, py, _ := p.Forward(float64(i%360)-180, float64(i%170)-85)
		x += px + py
	}
	_ = x
}

func BenchmarkTopoFeatureWorld(b *testing.B) {
	world := loadTopology(b, "world-110m.json")
	obj := world.Get("objects").Get("countries")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := TopoFeature(world, obj); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTopoMeshUS(b *testing.B) {
	us := loadTopology(b, "us-10m.json")
	obj := us.Get("objects").Get("counties")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := TopoMesh(us, obj, MeshInterior); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGraticule(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		g := NewGraticule()
		if _, err := g.Lines(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGeoCentroidBoundsArea(b *testing.B) {
	_, countries := benchCountries(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		GeoArea(countries)
		GeoBounds(countries)
		GeoCentroid(countries)
	}
}
