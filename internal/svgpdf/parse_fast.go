package svgpdf

import (
	"context"
	"strings"
	"unicode/utf8"
)

// parseSVGFast is a direct scanner for the well-formed, plain markup Vega's
// SVG renderer emits. encoding/xml spends most of the parse allocating tokens
// and translating namespaces; this builds the same element tree straight from
// the input, with names and values sharing the input's bytes.
//
// It accepts only a strict subset of what parseSVG accepts: elements of the
// supported vocabulary, simple names, quoted attribute values, the five
// predefined entities, and no comments, processing instructions, CDATA,
// carriage returns or namespace prefixes other than xmlns declarations. On
// anything else, on any malformation, and on any limit it reports ok false
// without an error, and parseSVG parses the document again with encoding/xml,
// so a document's tree, and the error for a bad document, are exactly those of
// encoding/xml. The only error it returns itself is a cancelled context.
func parseSVGFast(ctx context.Context, svg string, lim Limits) (root *element, ok bool, err error) {
	var (
		stack    []*element
		elements int
		pos      int
		slab     []element
		attrs    []attribute
	)
	for pos < len(svg) {
		// Character data up to the next tag.
		end := strings.IndexByte(svg[pos:], '<')
		if end < 0 {
			end = len(svg)
		} else {
			end += pos
		}
		if end > pos {
			data, ok := fastCharData(svg[pos:end], false)
			if !ok {
				return nil, false, nil
			}
			if len(stack) == 0 {
				if strings.TrimLeft(data, " \t\n") != "" {
					return nil, false, nil
				}
			} else if top := stack[len(stack)-1]; top.name == "text" || top.name == "tspan" {
				if top.text == "" {
					top.text = data
				} else {
					top.text += data
				}
			}
		}
		if end >= len(svg) {
			break
		}
		pos = end + 1
		if pos >= len(svg) {
			return nil, false, nil
		}

		if svg[pos] == '/' { // end tag
			if len(stack) == 0 {
				return nil, false, nil
			}
			top := stack[len(stack)-1]
			pos++
			if !strings.HasPrefix(svg[pos:], top.name) {
				return nil, false, nil
			}
			pos += len(top.name)
			for pos < len(svg) && isXMLSpace(svg[pos]) {
				pos++
			}
			if pos >= len(svg) || svg[pos] != '>' {
				return nil, false, nil
			}
			pos++
			stack = stack[:len(stack)-1]
			continue
		}

		// Start tag.
		name, n := fastName(svg[pos:])
		if n == 0 || !supportedElements[name] {
			return nil, false, nil
		}
		pos += n
		if elements++; elements > lim.MaxElements {
			return nil, false, nil
		}
		if elements&1023 == 0 {
			if err := ctxErr(ctx); err != nil {
				return nil, false, err
			}
		}
		if len(slab) == cap(slab) {
			slab = make([]element, 0, 256)
		}
		slab = append(slab, element{name: name})
		el := &slab[len(slab)-1]
		if len(attrs) == cap(attrs) {
			attrs = make([]attribute, 0, 1024)
		}
		first := len(attrs)
		selfClose := false
	tag:
		for {
			sp := pos
			for pos < len(svg) && isXMLSpace(svg[pos]) {
				pos++
			}
			if pos >= len(svg) {
				return nil, false, nil
			}
			switch svg[pos] {
			case '>':
				pos++
				break tag
			case '/':
				if pos+1 >= len(svg) || svg[pos+1] != '>' {
					return nil, false, nil
				}
				pos += 2
				selfClose = true
				break tag
			}
			if pos == sp { // attributes need leading space
				return nil, false, nil
			}
			an, n := fastAttrName(svg[pos:])
			if n == 0 {
				return nil, false, nil
			}
			pos += n
			for pos < len(svg) && isXMLSpace(svg[pos]) {
				pos++
			}
			if pos >= len(svg) || svg[pos] != '=' {
				return nil, false, nil
			}
			pos++
			for pos < len(svg) && isXMLSpace(svg[pos]) {
				pos++
			}
			if pos >= len(svg) || (svg[pos] != '"' && svg[pos] != '\'') {
				return nil, false, nil
			}
			q := svg[pos]
			pos++
			vend := strings.IndexByte(svg[pos:], q)
			if vend < 0 {
				return nil, false, nil
			}
			raw := svg[pos : pos+vend]
			pos += vend + 1
			val, ok := fastCharData(raw, true)
			if !ok {
				return nil, false, nil
			}
			if an == "xmlns" || strings.HasPrefix(an, "xmlns:") {
				continue // namespace declarations are not attributes
			}
			if len(attrs) == cap(attrs) {
				// Keep one element's attributes contiguous.
				grown := make([]attribute, len(attrs)-first, max(1024, 2*(len(attrs)-first)+1))
				copy(grown, attrs[first:])
				attrs, first = grown, 0
			}
			attrs = append(attrs, attribute{an, val})
		}
		if len(attrs) > first {
			el.attrs = attrs[first:len(attrs):len(attrs)]
		}

		if len(stack) == 0 {
			if root != nil || name != "svg" {
				return nil, false, nil
			}
			root = el
		} else {
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, el)
		}
		if !selfClose {
			stack = append(stack, el)
			if len(stack) > maxNestingDepth {
				return nil, false, nil
			}
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, false, nil
	}
	return root, true, nil
}

func isXMLSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' }

// fastName scans an element name made of ASCII letters, digits, '_', '-' and
// '.', starting with a letter or '_'; n is 0 when s does not start with one.
func fastName(s string) (name string, n int) {
	for n < len(s) {
		b := s[n]
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b == '_':
		case n > 0 && (b >= '0' && b <= '9' || b == '-' || b == '.'):
		default:
			return s[:n], n
		}
		n++
	}
	return s[:n], n
}

// fastAttrName is fastName that also takes an "xmlns:" declaration prefix.
func fastAttrName(s string) (name string, n int) {
	name, n = fastName(s)
	if name == "xmlns" && n < len(s) && s[n] == ':' {
		rest, m := fastName(s[n+1:])
		if m == 0 {
			return "", 0
		}
		_ = rest
		return s[:n+1+m], n + 1 + m
	}
	return name, n
}

// fastCharData validates character data or an attribute value the way
// encoding/xml does, and resolves the predefined entities. ok is false for
// anything the scanner leaves to encoding/xml.
func fastCharData(s string, attr bool) (out string, ok bool) {
	clean := true
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch {
		case b == '&':
			clean = false
		case b == '<' && attr, b == '\r':
			return "", false
		case b == '>':
			if i >= 2 && s[i-1] == ']' && s[i-2] == ']' {
				return "", false
			}
		case b < 0x20:
			if b != '\t' && b != '\n' {
				return "", false
			}
		case b >= 0x80:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				return "", false
			}
			if r > 0xD7FF && r < 0xE000 || r == 0xFFFE || r == 0xFFFF {
				return "", false
			}
			i += size - 1
		}
	}
	if clean {
		return s, true
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '&' {
			sb.WriteByte(s[i])
			continue
		}
		semi := strings.IndexByte(s[i:], ';')
		if semi < 0 {
			return "", false
		}
		switch s[i+1 : i+semi] {
		case "lt":
			sb.WriteByte('<')
		case "gt":
			sb.WriteByte('>')
		case "amp":
			sb.WriteByte('&')
		case "apos":
			sb.WriteByte('\'')
		case "quot":
			sb.WriteByte('"')
		default:
			return "", false
		}
		i += semi
	}
	return sb.String(), true
}
