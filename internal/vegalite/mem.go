package vegalite

import (
	"strings"
	"unicode/utf8"
)

// The compiler's byte meter. Work (compileCtx.work) bounds the compiler's
// time; this bounds its memory: the bytes the specification makes the
// compiler allocate in its large structures are charged to the call's ledger
// (budget.LedgerFrom, the ledger WithMemoryLimit sizes) before they are
// allocated, and past what the ledger has left the compilation fails with an
// error wrapping budget.ErrLimit. Upstream builds the same strings until V8's
// string limit throws; the call's budget is reached first, and refused the
// same way whatever the output would have been (no RangeError emulation).
//
// What is charged: every string the specification sizes (sbuf, the only
// string builder the package uses for them, see TestNoUnmeteredBuilders),
// the copies a compilation makes per model or per dataflow node (the
// ancestor parse a child copies, the field sets and parses the optimizer
// clones and merges), and the specification's own copy (Compile's deep
// clone). The charges are kept until the call ends: the compiled
// specification holds most of what was charged, and what it does not hold
// is in proportion to it.

// allocAhead is how many bytes the meter takes from the ledger at a time:
// a charge costs an addition and a comparison, and the ledger's atomic
// operation is paid once per allocAhead bytes. What is taken and not used
// is given back when the compilation ends.
const allocAhead = 64 << 10

// alloc charges n bytes the compilation is about to allocate, and panics
// with an error wrapping budget.ErrLimit when the call's ledger cannot pay
// for them. Call it before the allocation, with its real size (capacity,
// and what it points to that it owns). It also counts the allocation as
// work, a step per kilobyte, so that a compilation that allocates much
// between two loop steps still polls the deadline.
func (c *compileCtx) alloc(n int64) {
	if c == nil || n <= 0 {
		return
	}
	c.work(n >> 10)
	c.memUsed += n
	if n <= c.memAhead {
		c.memAhead -= n
		return
	}
	need := n - c.memAhead
	take := max(need, allocAhead)
	if err := c.ledger.Alloc(take); err != nil {
		// Without room for the batch, take only what is needed.
		if take == need {
			panic(limitError{"vegalite: compiling the specification: " + err.Error()})
		}
		if err := c.ledger.Alloc(need); err != nil {
			panic(limitError{"vegalite: compiling the specification: " + err.Error()})
		}
		take = need
	}
	c.memAhead = take - need
}

// free gives back n bytes charged with alloc once what they paid for is no
// longer reachable (a scratch index, a string hashed and dropped): they go
// to the meter's reservation, for the compilation's next charges, and back
// to the ledger when it ends.
func (c *compileCtx) free(n int64) {
	if c == nil || n <= 0 {
		return
	}
	n = min(n, c.memUsed)
	c.memUsed -= n
	c.memAhead += n
}

// releaseAhead gives back to the ledger what the meter took and did not
// use, once the compilation is over.
func (c *compileCtx) releaseAhead() {
	c.ledger.Free(c.memAhead)
	c.memAhead = 0
}

// sbuf is a strings.Builder that charges the compilation for its memory
// before it grows (see alloc). It grows as strings.Builder.Grow does, to
// twice its capacity plus what is written, so the charge is the size of the
// buffer allocated, and a write of n bytes costs O(n) plus, amortised, O(1)
// to charge. Every string whose length the specification controls is
// written through one (TestNoUnmeteredBuilders).
type sbuf struct {
	cc *compileCtx
	b  strings.Builder
	// charged is what the buffers it allocated were charged (release).
	charged int64
}

// newSbuf is an empty sbuf charging cc.
func newSbuf(cc *compileCtx) sbuf { return sbuf{cc: cc} }

// Grow makes room for n more bytes, charging the buffer it allocates.
func (s *sbuf) Grow(n int) {
	if s.b.Cap()-s.b.Len() >= n {
		return
	}
	c := 2*s.b.Cap() + n
	s.cc.alloc(int64(c))
	s.charged += int64(c)
	s.b.Grow(n)
}

// release gives back what the buffer was charged, once neither it nor any
// string made of it is reachable (a hash key hashed, a check made).
func (s *sbuf) release() {
	s.cc.free(s.charged)
	s.charged = 0
}

// WriteString appends x.
func (s *sbuf) WriteString(x string) {
	s.Grow(len(x))
	s.b.WriteString(x)
}

// PutByte appends c (named so as not to be io.ByteWriter, which returns an
// error).
func (s *sbuf) PutByte(c byte) {
	s.Grow(1)
	s.b.WriteByte(c)
}

// PutBytes appends p.
func (s *sbuf) PutBytes(p []byte) {
	s.Grow(len(p))
	s.b.Write(p)
}

// WriteRune appends r, UTF-8 encoded.
func (s *sbuf) WriteRune(r rune) {
	n := utf8.RuneLen(r)
	if n < 0 {
		n = utf8.RuneLen(utf8.RuneError) // written in place of an invalid rune
	}
	s.Grow(n)
	s.b.WriteRune(r)
}

// Len is the number of bytes written.
func (s *sbuf) Len() int { return s.b.Len() }

// String is what was written.
func (s *sbuf) String() string { return s.b.String() }

// Reset empties the buffer; what it held is not given back (the string made
// of it may still be held).
func (s *sbuf) Reset() { s.b.Reset() }

// valueBytes is what one jsval.Value in a slice costs.
const valueBytes = 16

// arrayBytes and objectBytes are what an array or object of n items
// costs: its header, its values, and an object's keys and index.
func arrayBytes(n int) int64  { return 32 + int64(n)*valueBytes }
func objectBytes(n int) int64 { return 96 + int64(n)*48 }

// joinCC is strings.Join charging cc for the string it makes, before it
// makes it: the parts are as many as the specification lists.
func joinCC(cc *compileCtx, parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	n := len(sep) * (len(parts) - 1)
	for _, p := range parts {
		n += len(p)
	}
	cc.alloc(int64(n))
	return strings.Join(parts, sep)
}

// cat is the concatenation of parts, charged before it is made: for the
// expressions that copy a text the specification sizes, which it can make
// the compiler copy once per field, view or selection (a number format of
// the config, written into the tooltip of every field).
func (c *compileCtx) cat(parts ...string) string {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	c.alloc(int64(n))
	return strings.Join(parts, "")
}
