package aster_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mgilbir/aster"
)

// A loaded body that starts with a UTF-8 byte order mark reads as the same
// body without it, in every text format (C39): upstream reads a URL with
// Response.text(), which drops one. It was a JSON syntax error, and part of a
// CSV's first column name, which then matched no field. A second mark is
// text, as it is to Response.text().
func TestLoadedDataDropsOneByteOrderMark(t *testing.T) {
	const bom = "\xEF\xBB\xBF"
	bodies := map[string]string{
		"json":     `[{"a":"x","b":3},{"a":"y","b":5}]`,
		"csv":      "a,b\nx,3\ny,5\n",
		"tsv":      "a\tb\nx\t3\ny\t5\n",
		"dsv":      "a|b\nx|3\ny|5\n",
		"topojson": `{"type":"Topology","objects":{"o":{"type":"GeometryCollection","geometries":[{"type":"Point","coordinates":[1,2],"properties":{"a":"x","b":3}}]}},"arcs":[]}`,
	}
	formats := map[string]string{
		"json": `{"type":"json"}`, "csv": `{"type":"csv","parse":"auto"}`, "tsv": `{"type":"tsv","parse":"auto"}`,
		"dsv": `{"type":"dsv","delimiter":"|","parse":"auto"}`, "topojson": `{"type":"topojson","feature":"o"}`,
	}
	for name, body := range bodies {
		render := func(prefix string) (string, error) {
			l := funcLoader{
				sanitize: func(_ context.Context, uri string) (string, error) { return uri, nil },
				load:     func(context.Context, string) ([]byte, error) { return []byte(prefix + body), nil },
			}
			c, err := aster.New(aster.WithLoader(l))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			field := "datum.a"
			if name == "topojson" {
				field = "datum.properties.a"
			}
			spec := `{"width":60,"height":40,"data":[{"name":"d","url":"d.` + name + `","format":` + formats[name] + `}],
				"marks":[{"type":"text","from":{"data":"d"},"encode":{"enter":{"text":{"signal":"` + field + `"},"y":{"value":10}}}}]}`
			return c.VegaToSVG([]byte(spec))
		}
		want, err := render("")
		if err != nil {
			t.Fatalf("%s without a mark: %v", name, err)
		}
		if !strings.Contains(want, ">x<") {
			t.Fatalf("%s: the chart does not draw the field: %s", name, want)
		}
		got, err := render(bom)
		if err != nil {
			t.Errorf("%s with a mark: %v", name, err)
		} else if got != want {
			t.Errorf("%s with a mark draws\n%s\nwant\n%s", name, got, want)
		}
		if name == "json" {
			if _, err := render(bom + bom); err == nil {
				t.Errorf("json with two marks parsed: only one is dropped")
			}
		}
	}
}
