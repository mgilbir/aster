package aster

import (
	"github.com/mgilbir/aster/internal/resvg"
	"github.com/mgilbir/aster/internal/textmeasure"
	"github.com/mgilbir/aster/internal/textmeasure/fonts/liberation"
	"github.com/mgilbir/aster/internal/textmeasure/fonts/notoemoji"
)

// fontPlan is the single source of truth for the fonts and generic-family
// mappings used by BOTH pipelines: go-text text measurement (which drives SVG
// layout) and resvg rasterization (which draws PNG glyphs). Building both from
// one plan keeps them from disagreeing about which custom fonts exist or what
// "sans-serif" / "monospace" resolve to.
//
// The embedded Liberation faces are always present in both pipelines (the
// measurer registers them internally; resvgFonts lists them here).
type fontPlan struct {
	custom      []fontEntry // user-registered fonts (family name + TTF data)
	systemFonts bool        // measurement only — see note in measurerOptions
	sansSerif   string      // generic "sans-serif" family name
	serif       string      // generic "serif" family name
	monospace   string      // generic "monospace" family name
}

func newFontPlan(cfg *config) fontPlan {
	sans := cfg.defaultFontFamily
	if sans == "" {
		sans = "Liberation Sans"
	}
	serif := cfg.defaultSerifFamily
	if serif == "" {
		serif = "Liberation Serif"
	}
	mono := cfg.defaultMonospaceFamily
	if mono == "" {
		mono = "Liberation Mono"
	}
	return fontPlan{
		custom:      cfg.fonts,
		systemFonts: cfg.systemFonts,
		sansSerif:   sans,
		serif:       serif,
		monospace:   mono,
	}
}

// measurerOptions produces the go-text measurer configuration.
//
// systemFonts is honored only here: resvg runs as WASM with no filesystem, so
// it cannot load OS-installed fonts. WithSystemFonts therefore affects SVG
// layout measurement but never PNG glyph rasterization.
func (p fontPlan) measurerOptions() []textmeasure.MeasurerOption {
	var opts []textmeasure.MeasurerOption
	if p.systemFonts {
		opts = append(opts, textmeasure.WithSystemFonts())
	}
	for _, f := range p.custom {
		opts = append(opts, textmeasure.WithFont(f.family, f.data))
	}
	opts = append(opts, textmeasure.WithDefaultFontFamily(p.sansSerif))
	opts = append(opts, textmeasure.WithDefaultSerifFamily(p.serif))
	opts = append(opts, textmeasure.WithDefaultMonospaceFamily(p.monospace))
	return opts
}

// resvgFonts produces the font data and generic-family mapping for resvg,
// derived from the same plan as measurerOptions so the two agree.
func (p fontPlan) resvgFonts() ([]resvg.Font, resvg.FamilyMapping) {
	fonts := []resvg.Font{
		{Data: liberation.SansRegular},
		{Data: liberation.SansBold},
		{Data: liberation.SansItalic},
		{Data: liberation.SansBoldItalic},
		{Data: liberation.MonoRegular},
		{Data: liberation.MonoBold},
		{Data: liberation.MonoItalic},
		{Data: liberation.MonoBoldItalic},
		{Data: liberation.SerifRegular},
		{Data: liberation.SerifBold},
		{Data: liberation.SerifItalic},
		{Data: liberation.SerifBoldItalic},
		// Monochrome emoji fallback, matching the measurement pipeline.
		{Data: notoemoji.Regular},
	}
	for _, f := range p.custom {
		fonts = append(fonts, resvg.Font{Data: f.data})
	}
	// cursive and fantasy have no bundled face; point them at the sans family so
	// both pipelines resolve them identically (the measurer routes them to its
	// sans fallback too).
	return fonts, resvg.FamilyMapping{
		SansSerif: p.sansSerif,
		Serif:     p.serif,
		Monospace: p.monospace,
		Cursive:   p.sansSerif,
		Fantasy:   p.sansSerif,
	}
}
