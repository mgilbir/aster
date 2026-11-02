package oracle

import (
	"fmt"
	"testing"
)

// An answer says whether upstream measured text at a font size past the
// engine's 65,536 px clamp, which node-canvas measures as overflow noise that
// differs between hosts (the differential's huge-font class takes a
// difference only when it did).
func TestAnswerSaysTextWasMeasuredPastTheClamp(t *testing.T) {
	o := For(t, VL6)
	for size, want := range map[string]bool{"1e6": true, "65537": true, "65536": false, "11": false} {
		spec := fmt.Sprintf(`{"width":100,"height":50,"marks":[{"type":"text","encode":{"update":{"text":{"value":"label"},"limit":{"value":40},"fontSize":{"value":%s}}}}]}`, size)
		res, err := o.SVG(false, []byte(spec))
		if err != nil {
			t.Fatal(err)
		}
		if res.Err != "" {
			t.Fatalf("font size %s: upstream refuses: %s", size, res.Err)
		}
		if res.MeasuredPastClamp != want {
			t.Errorf("font size %s: MeasuredPastClamp = %v, want %v", size, res.MeasuredPastClamp, want)
		}
	}
}
