package svgpdf

import (
	"math"
	"strings"

	"github.com/mgilbir/aster/internal/csscolor"
	pdf0 "github.com/mgilbir/pdf0"
)

// Links. Vega wraps a mark with an href in <a xlink:href="…">. Its contents
// are drawn as a group's are, and the page gets a Link annotation whose
// action opens the URI, over the box of what they drew.

// linkAnnot is one link: its box on the page and the URI it opens.
type linkAnnot struct {
	box rect
	uri string
}

// linkAnnotBytes is about what an annotation holds, here and in the
// document, besides its URI.
const linkAnnotBytes = 256

// linkSchemes are the schemes a link may open. The SVG is untrusted
// (SVGToPDF's input is anybody's), so this is an allow-list of its own,
// whatever the Loader allowed when the SVG was written: a javascript:,
// data: or file: URI never becomes a PDF action, nor does a relative one,
// which a PDF has no base to resolve against.
var linkSchemes = map[string]bool{"http": true, "https": true, "mailto": true}

// linkURI is the URI a link opens, or "" for none: an absolute URI of an
// allowed scheme, its bytes outside printable ASCII percent-encoded (a /URI
// is a 7-bit ASCII string, PDF 12.6.4.8).
func linkURI(href string) string {
	href = strings.Trim(href, xmlSpace)
	colon := strings.IndexByte(href, ':')
	if colon <= 0 {
		return ""
	}
	scheme := href[:colon]
	for i := 0; i < len(scheme); i++ {
		c := scheme[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.')) {
			return ""
		}
	}
	if !linkSchemes[csscolor.ASCIILower(scheme)] { // the scheme is ASCII (checked above)
		return ""
	}
	var b strings.Builder
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(href); i++ {
		if c := href[i]; c > 0x20 && c < 0x7F {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// link draws the contents of an <a>, and adds its annotation when it opens
// an allowed URI and drew something.
func (r *renderer) link(e *element, st gstate) error {
	uri := linkURI(e.attrVal("href"))
	if uri == "" {
		return r.children(e, st)
	}
	box := &rect{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	r.w.links = append(r.w.links, box)
	err := r.children(e, st)
	r.w.links = r.w.links[:len(r.w.links)-1]
	if err != nil || !(box.x0 <= box.x1 && box.y0 <= box.y1) {
		return err
	}
	r.alloc(linkAnnotBytes + int64(len(uri)))
	r.links = append(r.links, linkAnnot{*box, uri})
	return nil
}

// annotations are the page's /Annots: a Link with a URI action each.
func annotations(links []linkAnnot) pdf0.Array {
	out := make(pdf0.Array, 0, len(links))
	for _, l := range links {
		action := &pdf0.Dictionary{}
		action.Set("S", pdf0.Name("URI"))
		action.Set("URI", pdf0.String{Value: []byte(l.uri)})
		a := &pdf0.Dictionary{}
		a.Set("Type", pdf0.Name("Annot"))
		a.Set("Subtype", pdf0.Name("Link"))
		a.Set("Rect", pdf0.Array{pdf0.Real(l.box.x0), pdf0.Real(l.box.y0), pdf0.Real(l.box.x1), pdf0.Real(l.box.y1)})
		a.Set("Border", pdf0.Array{pdf0.Integer(0), pdf0.Integer(0), pdf0.Integer(0)})
		a.Set("A", action)
		out = append(out, a)
	}
	return out
}
