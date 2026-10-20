package svgpdf

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"unsafe"

	"github.com/mgilbir/aster/internal/budget"
)

// element is a parsed SVG element. Only the Vega SVG-renderer output subset
// is accepted; anything else fails parsing with a descriptive error so the
// caller can fall back to raster output.
type element struct {
	name     string
	attrs    []attribute // in document order; a repeated name's last value wins
	children []*element
	text     string // character data, only meaningful for <text>
}

type attribute struct{ name, value string }

// elementBytes and attributeBytes are what one of each holds in the tree.
const (
	elementBytes   = int64(unsafe.Sizeof(element{}))
	attributeBytes = int64(unsafe.Sizeof(attribute{}))
)

// attr returns the attribute value and whether it was present.
func (e *element) attr(name string) (string, bool) {
	for i := len(e.attrs) - 1; i >= 0; i-- {
		if e.attrs[i].name == name {
			return e.attrs[i].value, true
		}
	}
	return "", false
}

// attrVal returns the attribute value, or "" when it is absent.
func (e *element) attrVal(name string) string {
	v, _ := e.attr(name)
	return v
}

// supportedElements is the element vocabulary of Vega's SVG renderer that the
// translator understands. Encountering anything else is an error.
var supportedElements = map[string]bool{
	"svg":   true,
	"g":     true,
	"a":     true, // a link around marks with an href
	"rect":  true,
	"path":  true,
	"line":  true,
	"text":  true,
	"tspan": true,
	"image": true,

	"linearGradient": true,
	"pattern":        true, // only the one Vega wraps a radial gradient in (see collectPatterns)
	"radialGradient": true,
	"stop":           true,
	"defs":           true,
	"clipPath":       true,
}

// maxNestingDepth bounds element nesting. SVGToPDF is public API accepting
// arbitrary SVG, and the tree is later walked recursively (collectClipPaths,
// renderer.element), so a pathologically nested document could otherwise
// exhaust the goroutine stack. Vega emits well under 20 levels; 512 leaves a
// wide margin while turning an abusive document into a clean error. Bounding
// the tree here bounds every downstream recursion over it.
const maxNestingDepth = 512

// parseSVG parses an SVG document into an element tree, rejecting elements
// outside the supported subset.
func parseSVG(ctx context.Context, svg string, lim Limits) (*element, error) {
	if len(svg) > lim.MaxInputBytes {
		return nil, limitErr("%s is %d bytes, limit is %d", lim.Input, len(svg), lim.MaxInputBytes)
	}
	if root, ok, err := parseSVGFast(ctx, svg, lim); err != nil {
		return nil, err
	} else if ok {
		return root, nil
	}
	return parseSVGXML(ctx, svg, lim)
}

// parseSVGXML is parseSVG on encoding/xml: the reference for what a document
// parses to and the source of every parse error.
func parseSVGXML(ctx context.Context, svg string, lim Limits) (*element, error) {
	dec := xml.NewDecoder(strings.NewReader(svg))
	led := budget.LedgerFrom(ctx)
	var root *element
	var stack []*element
	elements, tokens := 0, 0
	// The character data of each <text> and <tspan> open, by depth, joined
	// when it closes: encoding/xml gives each CDATA section and each stretch
	// between children as a token of its own, and adding each to a string
	// copies the text so far each time (C60).
	var pieces [][]string

	for {
		if tokens++; tokens&1023 == 0 {
			if err := ctxErr(ctx); err != nil {
				return nil, err
			}
		}
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("svgpdf: parsing SVG: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			if !supportedElements[name] {
				return nil, fmt.Errorf("svgpdf: unsupported SVG element <%s>", name)
			}
			if elements++; elements > lim.MaxElements {
				return nil, limitErr("%s has more than %d elements", lim.Input, lim.MaxElements)
			}
			// The element, its attributes and their values (encoding/xml
			// copies them), and its place in its parent's list: the tree
			// is the input's to size (ADR 0010).
			n := elementBytes + 8 + attributeBytes*int64(len(t.Attr))
			for _, a := range t.Attr {
				n += int64(len(a.Value))
			}
			if err := led.Alloc(n); err != nil {
				return nil, err
			}
			el := &element{name: name, attrs: make([]attribute, 0, len(t.Attr))}
			for _, a := range t.Attr {
				// Namespace declarations arrive with Space "xmlns"; keep
				// plain local names for everything else.
				if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
					continue
				}
				el.attrs = append(el.attrs, attribute{a.Name.Local, a.Value})
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, fmt.Errorf("svgpdf: multiple root elements")
				}
				if name != "svg" {
					return nil, fmt.Errorf("svgpdf: root element is <%s>, expected <svg>", name)
				}
				root = el
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, el)
			}
			stack = append(stack, el)
			if len(pieces) < len(stack) {
				pieces = append(pieces, nil)
			}
			pieces[len(stack)-1] = pieces[len(stack)-1][:0]
			if len(stack) > maxNestingDepth {
				return nil, limitErr("SVG nesting exceeds %d levels", maxNestingDepth)
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("svgpdf: unbalanced SVG markup")
			}
			if top := len(stack) - 1; len(pieces[top]) > 0 {
				stack[top].text = strings.Join(pieces[top], "")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if top := len(stack) - 1; top >= 0 && (stack[top].name == "text" || stack[top].name == "tspan") {
				pieces[top] = append(pieces[top], string(t))
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
			// ignore
		}
	}
	if root == nil {
		return nil, fmt.Errorf("svgpdf: no <svg> root element found")
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("svgpdf: unbalanced SVG markup")
	}
	return root, nil
}

// clipDef is a <clipPath>, read: the shapes it is the union of, the
// transform it states, and whether its one shape's clip-rule is evenodd.
type clipDef struct {
	shapes    []*element
	transform Matrix
	evenOdd   bool
}

// collectClipPaths walks the tree and indexes <clipPath> definitions by id,
// each read by readClip.
func collectClipPaths(root *element) (map[string]*clipDef, error) {
	clips := make(map[string]*clipDef)
	var walk func(e *element) error
	walk = func(e *element) error {
		if e.name == "clipPath" {
			id, ok := e.attr("id")
			if !ok {
				return fmt.Errorf("svgpdf: <clipPath> without id")
			}
			c, err := readClip(e)
			if err != nil {
				return err
			}
			clips[id] = c
			return nil
		}
		for _, c := range e.children {
			if err := walk(c); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return clips, nil
}

// clipShapeAttrs are the attributes of a <clipPath>'s shapes that make
// their geometry.
var clipShapeAttrs = map[string]map[string]bool{
	"rect": {"x": true, "y": true, "width": true, "height": true},
	"path": {"d": true},
}

// readClip reads a <clipPath>: every attribute of it and of its shapes is
// one the clip draws, one with no effect on a clip (a fill, an opacity), or
// an error. Its transform is applied; clipPathUnits other than user space,
// a clip-path on it or on a shape, a style, and a clip-rule other than
// nonzero but on its one shape are refused. A shape not displayed adds
// nothing.
func readClip(e *element) (*clipDef, error) {
	c := &clipDef{transform: Identity()}
	evenOdd := false
	read := func(el *element, own map[string]bool) (displayed bool, err error) {
		displayed = true
		for _, a := range el.attrs {
			v := strings.TrimSpace(a.value)
			switch {
			case own[a.name], a.name == "id" && el == e, isIgnorableAttr(a.name):
			case a.name == "clipPathUnits" && el == e:
				if v != "userSpaceOnUse" {
					return false, fmt.Errorf("svgpdf: unsupported clipPathUnits %s on <clipPath>", quoted(v))
				}
			case a.name == "transform":
				if el != e {
					return false, fmt.Errorf("svgpdf: transform on <clipPath> children is not supported")
				}
				if m, ok := parseTransform(v); ok {
					c.transform = m
				}
			case a.name == "clip-rule":
				switch v {
				case "evenodd":
					evenOdd = true
				case "nonzero":
					evenOdd = false
				}
			case a.name == "display" && el != e:
				displayed = v != "none"
			case a.name == "clip-path", a.name == "style":
				return false, fmt.Errorf("svgpdf: unsupported attribute %s on <%s> in a <clipPath>", quoted(a.name), el.name)
			case props[a.name] != propNone:
				// A paint, an opacity, a font: no effect on a clip.
			default:
				return false, fmt.Errorf("svgpdf: unsupported attribute %s on <%s> in a <clipPath>", quoted(a.name), el.name)
			}
		}
		return displayed, nil
	}
	if _, err := read(e, nil); err != nil {
		return nil, err
	}
	inherited := evenOdd
	for _, ch := range e.children {
		own, ok := clipShapeAttrs[ch.name]
		if !ok {
			return nil, fmt.Errorf("svgpdf: unsupported element <%s> inside <clipPath>", ch.name)
		}
		evenOdd = inherited
		displayed, err := read(ch, own)
		if err != nil {
			return nil, err
		}
		if !displayed {
			continue
		}
		c.shapes = append(c.shapes, ch)
		c.evenOdd = c.evenOdd || evenOdd
	}
	// The shapes are one path clipped by one rule: their union only under
	// nonzero, unless there is one.
	if c.evenOdd && len(c.shapes) > 1 {
		return nil, fmt.Errorf("svgpdf: a <clipPath> of several shapes with clip-rule evenodd is not supported")
	}
	return c, nil
}
