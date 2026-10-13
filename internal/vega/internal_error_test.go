package vega

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// panicLoader panics with v on every load: a bug outside the engine's control.
type panicLoader struct{ v any }

func (panicLoader) Sanitize(_ context.Context, uri string) (string, error) { return uri, nil }
func (l panicLoader) Load(context.Context, string) ([]byte, error)         { panic(l.v) }

// A panic the engine did not expect is an error whose message is the panic's
// alone; the goroutine stack it happened on goes to panicHook, for whoever is
// debugging, not into the message every caller sees.
func TestInternalErrorsCarryNoStack(t *testing.T) {
	var stacks [][]byte
	panicHook = func(s []byte) { stacks = append(stacks, s) }
	defer func() { panicHook = nil }()
	sentinel := errors.New("boom")
	for name, spec := range map[string]string{
		"while the dataflow is made": `{"data":[{"name":"d","url":"x.json"}]}`,
		"while it runs":              `{"signals":[{"name":"u","value":"x.json"}],"data":[{"name":"d","url":{"signal":"u"}}]}`,
	} {
		for _, v := range []any{"boom", sentinel} {
			stacks = nil
			_, err := Render(context.Background(), mustJSON(t, spec), Options{Loader: panicLoader{v}})
			if err == nil || !strings.Contains(err.Error(), "boom") {
				t.Fatalf("%s, panic %#v: err = %v", name, v, err)
			}
			if strings.Contains(err.Error(), "goroutine") || strings.Contains(err.Error(), "\n") {
				t.Errorf("%s, panic %#v: the message carries a stack: %q", name, v, err)
			}
			if v == sentinel && !errors.Is(err, sentinel) {
				t.Errorf("%s: err %v does not wrap the panic's error", name, err)
			}
			if len(stacks) != 1 || !strings.Contains(string(stacks[0]), "goroutine") {
				t.Errorf("%s, panic %#v: panicHook got %d stacks", name, v, len(stacks))
			}
		}
	}
}
