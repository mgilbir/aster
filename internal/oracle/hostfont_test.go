package oracle

import (
	"strings"
	"testing"
)

// hostSansSerif is a fontconfig rule that makes a host's default font a
// bundled font other than DejaVu Sans, as a macOS host's default is a system
// font: any pattern naming none of the oracle's families is answered with
// Liberation Sans first.
func hostSansSerif() string {
	var b strings.Builder
	b.WriteString(`  <match target="pattern">`)
	for _, f := range []string{"DejaVu Sans", "DejaVu Sans Mono", "Liberation Sans", "Liberation Serif", "Liberation Mono", "Noto Emoji"} {
		b.WriteString(`<test qual="all" name="family" compare="not_eq"><string>` + f + `</string></test>`)
	}
	b.WriteString(`<edit name="family" mode="prepend" binding="strong"><string>Liberation Sans</string></edit></match>` + "\n")
	return b.String()
}

// A font string the canvas rejects leaves it on its initial font, 10px
// sans-serif, and what that name means must not depend on the host: the
// engine measures it as its default family, and the oracle must too. The
// label's font size is infinite, so its font is "Infinitypx sans-serif", which
// the canvas rejects; the label is cut to the limit by the width of the initial
// font. With the host's default made another font, the answer must not change.
func TestRejectedFontMeasuresAsTheEngineDefault(t *testing.T) {
	const spec = `{"width":200,"height":50,"marks":[{"type":"text","encode":{"enter":{` +
		`"x":{"value":5},"y":{"value":20},"text":{"value":"throughput of the system"},` +
		`"limit":{"value":60},"fontSize":{"signal":"1/0"}}}}]}`
	shared := For(t, VL6)
	svg := func(hostile bool) string {
		t.Helper()
		if hostile {
			t.Setenv("ASTER_ORACLE_TEST_FONTCONF", hostSansSerif())
		} else {
			t.Setenv("ASTER_ORACLE_TEST_FONTCONF", "")
		}
		o := &Oracle{root: shared.root, set: shared.set, zone: shared.zone, version: shared.version, free: make(chan *proc, 1)}
		o.free <- nil
		defer func() {
			if p := <-o.free; p != nil {
				p.stop()
			}
		}()
		res, err := o.node(map[string]any{"op": "svg", "spec": spec})
		if err != nil {
			t.Fatal(err)
		}
		if res.Err != "" {
			t.Fatalf("upstream refuses the spec: %s", res.Err)
		}
		return res.SVG
	}
	plain, hostile := svg(false), svg(true)
	if plain != hostile {
		t.Errorf("the host's default font changes the answer:\n plain   %.400s\n hostile %.400s", plain, hostile)
	}
	if !strings.Contains(plain, "…") {
		t.Errorf("the label was not cut to its limit, so the test does not reach the font: %.600s", plain)
	}
}
