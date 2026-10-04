package svgpdf

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
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
	"rect":  true,
	"path":  true,
	"line":  true,
	"text":  true,
	"tspan": true,
	"image": true,

	"linearGradient": true,
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
		return nil, limitErr("SVG input is %d bytes, limit is %d", len(svg), lim.MaxInputBytes)
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
	var root *element
	var stack []*element
	elements, tokens := 0, 0

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
				return nil, limitErr("more than %d elements", lim.MaxElements)
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
			if len(stack) > maxNestingDepth {
				return nil, fmt.Errorf("%w: SVG nesting exceeds %d levels", ErrLimit, maxNestingDepth)
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("svgpdf: unbalanced SVG markup")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 && (stack[len(stack)-1].name == "text" || stack[len(stack)-1].name == "tspan") {
				stack[len(stack)-1].text += string(t)
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

// collectClipPaths walks the tree and indexes <clipPath> definitions by id.
func collectClipPaths(root *element) (map[string]*element, error) {
	clips := make(map[string]*element)
	var walk func(e *element) error
	walk = func(e *element) error {
		if e.name == "clipPath" {
			id, ok := e.attr("id")
			if !ok {
				return fmt.Errorf("svgpdf: <clipPath> without id")
			}
			clips[id] = e
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
