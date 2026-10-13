package vega

import (
	"context"
	"errors"
	"testing"
)

type pollsCtx struct {
	context.Context
	n int
}

func (c *pollsCtx) Err() error {
	if c.n--; c.n < 0 {
		return context.Canceled
	}
	return nil
}

// A loaded body is copied a chunk at a time with the call's context polled in
// between: a context that ends after the first chunk stops the copy of a
// body of several chunks. (Counted in polls; the copy in one piece was the
// longest unpolled stretch of the big-CSV bounded case.)
func TestTextOfStopsBetweenChunks(t *testing.T) {
	_, err := textOf(&pollsCtx{Context: context.Background(), n: 1}, make([]byte, 4*textChunk))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a body of four chunks copied whole past a canceled context: %v", err)
	}
}
