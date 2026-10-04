package imageref

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mgilbir/aster/internal/budget"
)

var testLimits = Limits{MaxBytes: 100, MaxPixels: 100, Err: fmt.Errorf("test: %w", budget.ErrLimit)}

func TestFetch(t *testing.T) {
	load := func(ctx context.Context, href string) ([]byte, error) {
		if got := budget.From(ctx).LoadLeft(); got != 100 {
			return nil, fmt.Errorf("the loader may read %d bytes, want 100", got)
		}
		switch href {
		case "ok":
			return []byte("bytes"), nil
		case "big":
			return make([]byte, 101), nil
		}
		return nil, errors.New("not found")
	}
	got, err := Fetch(context.Background(), []string{"ok", "missing"}, load, testLimits)
	if err != nil || len(got) != 1 || string(got["ok"]) != "bytes" {
		t.Fatalf("Fetch = %q, %v; want only ok", got, err)
	}
	if _, err := Fetch(context.Background(), []string{"ok", "big"}, load, testLimits); !errors.Is(err, testLimits.Err) {
		t.Errorf("an image over the limit: err = %v", err)
	}
	if _, err := Fetch(context.Background(), make([]string, MaxFetched+1), load, testLimits); !errors.Is(err, budget.ErrLimit) {
		t.Errorf("too many images: err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Fetch(ctx, []string{"ok"}, load, testLimits); !errors.Is(err, context.Canceled) {
		t.Errorf("a done context: err = %v", err)
	}
	if got, err := Fetch(context.Background(), []string{"ok"}, nil, testLimits); got != nil || err != nil {
		t.Errorf("no loader: %v, %v", got, err)
	}
}

func TestDataURIAndConfig(t *testing.T) {
	if IsData("http://x") || !IsData(" data:,x") {
		t.Error("IsData")
	}
	if b, err := DataURI("data:,a%20b", testLimits); err != nil || string(b) != "a b" {
		t.Errorf("DataURI = %q, %v", b, err)
	}
	if _, err := DataURI("data:,"+string(make([]byte, 101)), testLimits); !errors.Is(err, testLimits.Err) {
		t.Errorf("an oversized payload: err = %v", err)
	}
	if _, _, err := Config([]byte("not an image"), testLimits); err == nil || errors.Is(err, budget.ErrLimit) {
		t.Errorf("not an image: err = %v", err)
	}
}
