package jsval

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

// pollCtx counts the polls of its Err and ends, if stop is set, at the
// stop-th: polls are counted, not timed, so no machine's speed changes it.
type pollCtx struct {
	context.Context
	mu    sync.Mutex
	polls int
	stop  int
}

func (c *pollCtx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.polls++
	if c.stop > 0 && c.polls >= c.stop {
		return context.Canceled
	}
	return nil
}

// pollDocs are documents a step of the parser reads long in each of the
// ways a document can be long: many values, one long string with escapes or
// without, a long run of white space, a long key.
var pollDocs = map[string][]byte{
	"rows":    rowsJSON(8000),
	"numbers": []byte("[" + strings.Repeat("1.5,", 1<<18) + "0]"),
	"string":  []byte(`["` + strings.Repeat("abcdefgh", 1<<17) + `"]`),
	"escapes": []byte(`["` + strings.Repeat(`a\né`, 1<<16) + `"]`),
	"space":   []byte(`[1,` + strings.Repeat(" \n\t\r", 1<<18) + `2]`),
	"key":     []byte(`[{"` + strings.Repeat("k", 1<<20) + `":1}]` + strings.Repeat(" ", slabMinDoc)),
}

// A metered parse polls its context throughout the document, inside a
// long token as between values, and ends at the poll its context ends at,
// with the context's error: a deadline does not wait for the rest of a
// document of tens of megabytes. Counted, not timed: a document polls at
// least once per 32 KiB of text (the meter polls every 1,024 steps of 16
// bytes), and a context that ends at its middle poll stops it.
func TestParseJSONContextPolls(t *testing.T) {
	for name, doc := range pollDocs {
		t.Run(name, func(t *testing.T) {
			want, err := ParseJSON(doc)
			if err != nil {
				t.Fatal(err)
			}
			full := &pollCtx{Context: context.Background()}
			got, err := ParseJSONContext(full, doc, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(AppendJSON(nil, got), AppendJSON(nil, want)) {
				t.Fatal("the metered parse reads the document differently")
			}
			if min := len(doc) / (32 << 10); full.polls < min {
				t.Errorf("%d polls for %d bytes; want at least %d: a stretch of the parse does not poll", full.polls, len(doc), min)
			}
			half := &pollCtx{Context: context.Background(), stop: full.polls / 2}
			if v, err := ParseJSONContext(half, doc, 0); !errors.Is(err, context.Canceled) || v.Kind() != KindUndefined {
				t.Errorf("a context ended at poll %d of %d did not stop the parse: %v", half.stop, full.polls, err)
			}
		})
	}
}

// The call's ledger, when the context carries one, is what the parse
// charges and polls through.
func TestParseJSONContextChargesLedger(t *testing.T) {
	doc := pollDocs["rows"]
	led := budget.NewLedger(context.Background(), budget.LedgerLimits{})
	if _, err := ParseJSONContext(budget.WithLedger(context.Background(), led), doc, 0); err != nil {
		t.Fatal(err)
	}
	// Every full 1,024 steps reach the ledger; the rest stays in the meter.
	if got, min := led.WorkDone(), int64(len(doc)/jsonBytesPerStep)-2*1024; got < min {
		t.Errorf("the ledger was charged %d steps for %d bytes; want at least %d", got, len(doc), min)
	}
}

// Wherever the polls fall, a metered parse reads a document as the
// unmetered one does, error for error: the documents of the fuzz corpus and
// the long ones above, parsed with a charge after every byte or few, which
// puts one inside every token.
func TestParseJSONMeteredMatches(t *testing.T) {
	docs := [][]byte{
		[]byte(`{"a":[1,2.5e3,-0,"xé😀",null,true,false],"a":{"b":{}}}`),
		[]byte(`[1e999,-1e999,1E-999,123456789012345678901234567890,0.1e1]`),
		[]byte(`"\ud800A\udc00 \u0000"`), []byte("\"\xff\xfe\""), []byte(`{"__proto__":{"x":1},"":[]}`),
		[]byte(`[[[[[[[[[[1]]]]]]]]]]`), []byte(` -`), []byte(`01`), []byte(`1.`), []byte(`[1,]`), []byte(`{"a" 1}`),
		[]byte(`tru`), []byte("\"\x01\""), []byte(`[] x`), []byte(` [ 1 , "a\nb" , { "k" : "v" } ] `), []byte(`"abc`), []byte(`"a\`),
		[]byte(`  `), []byte(``),
	}
	for _, d := range pollDocs {
		docs = append(docs, d[:min(len(d), 3000)], d[len(d)-min(len(d), 3000):])
	}
	for _, doc := range docs {
		want, werr := ParseJSON(doc)
		for every := 1; every <= 7; every++ {
			m := budget.NewMeter(context.Background())
			got, gerr := parseJSON(&m, every, doc, 0)
			if (werr == nil) != (gerr == nil) || (werr != nil && werr.Error() != gerr.Error()) {
				t.Fatalf("%.40q every %d: error %v, want %v", doc, every, gerr, werr)
			}
			if werr == nil && !bytes.Equal(AppendJSON(nil, got), AppendJSON(nil, want)) {
				t.Fatalf("%.40q every %d: read differently", doc, every)
			}
		}
	}
}
