// Package svgpdf translates the SVG output of Vega's SVG renderer into a
// single-page vector PDF.
//
// It supports exactly the SVG subset Vega emits (rect, g, path, line, text,
// clipPath); anything outside that subset is a descriptive error, never a
// silent omission, so callers can fall back to raster (PNG) rendering. Text
// is converted to filled glyph outlines — no fonts are embedded.
//
// Coordinates map 1 SVG px to 1 PDF pt; a chart rendered at 400×300 becomes
// a 400×300 pt page, which \includegraphics and other embedders can scale
// freely without quality loss.
package svgpdf

// Convert translates an SVG document into PDF bytes. The shaper is required
// when the SVG contains text elements; pass nil only for text-free charts.
func Convert(svg string, shaper TextShaper) ([]byte, error) {
	root, err := parseSVG(svg)
	if err != nil {
		return nil, err
	}
	content, gsList, width, height, err := render(root, shaper)
	if err != nil {
		return nil, err
	}
	return buildPDF(content, gsList, width, height)
}
