package jsval

import (
	"context"
	"fmt"
	"math"
	"slices"
	"unsafe"

	"github.com/mgilbir/aster/internal/budget"
)

// The walks of a value: String of an array, JSON, vega-util's stringValue,
// deep equality and the relational comparison of two arrays or objects. They
// are the only code that visits a value whole, and they are bounded here, not
// at their callers (ADR 0017).
//
// A value built at run time can share its parts: `b1 = [b0, b0, …]`, then
// `b2 = [b1, b1, …]`, holds a few hundred arrays in memory, each charged
// when an expression made it, and stands for a tree of 10^8 elements, which
// String, JSON.stringify and a deep comparison each visit in full, as
// upstream does too. So a walk is bounded by what it does, not by what the
// value holds:
//
//   - with a call's Walker (WalkerFor), each value visited is a step of work
//     charged to the call's ledger, which polls the call's context, so the
//     deadline ends the walk wherever it is; the text it builds is charged
//     to the ledger's bytes before it is allocated (past smallWalk bytes, for
//     the rest of the call: the caller keeps it), so the memory limit ends it
//     too;
//   - without one (AsString, Equal, Relate, StringValue, AppendJSON,
//     MarshalJSON), it visits at most MaxDetachedWalk values and builds at
//     most maxDetachedBytes: forms for values whose size the specification's
//     text already bounds (a specification's parsed literals), and a
//     backstop for any other caller;
//   - either way, a value nested deeper than MaxValueDepth, and a string of
//     more than MaxStringLength UTF-16 code units, are the engine's limits.
//
// A walk that passes a bound panics with a *budget.Stop wrapping the error
// (budget.ErrLimit, or the context's), which the public API returns as the
// call's error, as every bound that stops work deep inside a stage does.

// MaxStringLength bounds the strings the engine builds as JavaScript strings,
// in UTF-16 code units as JavaScript measures them: the strings expressions
// build and the String or stringValue of a value. It is an engine limit, far
// below V8's String::kMaxLength (2^29 - 24 code units); a string past it ends
// the call with an error wrapping budget.ErrLimit.
const MaxStringLength = 1 << 24

// MaxDetachedWalk is how many values a walk made without a call's Walker may
// visit. Tens of milliseconds of work: a specification's own literals are
// far smaller, and a value larger than this is one an expression or a
// transform built, which the call's Walker walks.
const MaxDetachedWalk = 1 << 22

// maxDetachedBytes bounds the text a walk without a call's Walker builds.
const maxDetachedBytes = 1 << 24

// maxStringBytes bounds the bytes of a String or stringValue under
// construction: a string of MaxStringLength code units holds at most three
// bytes each.
const maxStringBytes = 3 * MaxStringLength

// smallWalk is the size of text a walk builds without charging it: a chart
// prints millions of short labels, keys and names, each held by whatever
// holds its value (as an expression's strings up to its smallString are).
const smallWalk = 512

// walkPoll is how many values a walk visits between two charges of its work.
const walkPoll = 1 << 10

var (
	errWalkDepth  = fmt.Errorf("%w: a value nests deeper than %d levels", budget.ErrLimit, MaxValueDepth)
	errDetached   = fmt.Errorf("%w: a value of more than %d parts was walked without the call's budget", budget.ErrLimit, MaxDetachedWalk)
	errDetachedSz = fmt.Errorf("%w: a value's text of more than %d bytes was built without the call's budget", budget.ErrLimit, maxDetachedBytes)
)

// Walker bounds the walks of one call: it charges their steps to the call's
// budget.Ledger, which polls the call's context, and the text they build to
// the ledger's bytes. Make one with WalkerFor where the call's context is at
// hand and keep it with the state of the call (a Walker is two words, safe to
// copy and to use from any goroutine of the call). The zero Walker is the
// detached one AsString and the package functions use.
type Walker struct {
	ctx context.Context
	l   *budget.Ledger
	// date, when set, writes a date's text in String (WithDateText).
	date func(dst []byte, ms float64) []byte
}

// WalkerFor returns the Walker of the call that ctx belongs to: its ledger,
// and its context, polled directly when there is no ledger. A nil ctx gives
// the detached Walker.
func WalkerFor(ctx context.Context) Walker {
	if ctx == nil {
		return Walker{}
	}
	f, _ := ctx.Value(dateTextKey{}).(func(dst []byte, ms float64) []byte)
	return Walker{ctx: ctx, l: budget.LedgerFrom(ctx), date: f}
}

type dateTextKey struct{}

// ContextWithDateText returns ctx carrying f, the call's
// Date.prototype.toString (in the view's time zone, which jsval cannot
// know): the Walkers of the call (WalkerFor) write a date with it wherever
// JavaScript's String writes one, inside an array too (String([d]),
// [d].join(), ” + [d], a relational comparison of arrays). JSON writes a
// date as its toJSON whatever the zone.
func ContextWithDateText(ctx context.Context, f func(dst []byte, ms float64) []byte) context.Context {
	return context.WithValue(ctx, dateTextKey{}, f)
}

// WithDateText is w with String writing a date, at any depth, as f does
// (Date.prototype.toString in a time zone, which jsval cannot know): the
// String of a value as the expression evaluator and the transforms' keys
// write it. Without it (and without ContextWithDateText in the call's
// context) a date's String is its epoch number.
func (w Walker) WithDateText(f func(dst []byte, ms float64) []byte) Walker {
	w.date = f
	return w
}

// Detached reports whether w is the zero Walker, bounded by MaxDetachedWalk
// rather than by a call.
func (w Walker) Detached() bool { return w.ctx == nil }

// DepthExceeded ends the call with the error of a value nested past
// MaxValueDepth, as String does. It is for a caller that visits the arrays of
// a value itself, to write their text as it goes (the SVG writer's bounded
// output), and has met one nested deeper.
func (w Walker) DepthExceeded() { stop(errWalkDepth) }

// walk is the state of one walk.
type walk struct {
	ctx context.Context
	l   *budget.Ledger
	// pending is the values visited since the last charge; left, for a
	// detached walk, how many more it may visit.
	pending int64
	left    int64

	// buf is the text being built: base is where this walk's text starts
	// (AppendJSON appends to its caller's buffer), and from is the capacity
	// it was given. charged is what the ledger was charged for its growth;
	// maxBytes bounds len(buf)-base.
	buf      []byte
	base     int
	from     int
	charged  int64
	maxBytes int
	// limit, when positive, makes the walk a prefix: it stops writing, with
	// done set, once the text passes limit bytes.
	limit int
	done  bool
	// date writes a date's text in String (Walker.WithDateText).
	date func(dst []byte, ms float64) []byte
	// keep says the text outlives the walk (String, StringValue): what it
	// was charged stays charged for the rest of the call. Otherwise the
	// text is the caller's buffer, or dropped once compared, and the charge
	// is given back when the walk ends: it bounded the walk while it built.
	keep bool
}

func (w Walker) start(buf []byte, maxBytes int) walk {
	k := walk{ctx: w.ctx, l: w.l, date: w.date, buf: buf, base: len(buf), from: cap(buf), maxBytes: maxBytes}
	if w.ctx == nil {
		k.left = MaxDetachedWalk
		k.maxBytes = min(maxBytes, maxDetachedBytes)
	}
	return k
}

// stop ends the walk, and the call it belongs to, with err.
func stop(err error) { panic(&budget.Stop{Err: err}) }

// step counts one value visited.
func (k *walk) step() {
	if k.pending++; k.pending >= walkPoll {
		k.charge()
	}
}

// charge reports the values visited since the last charge.
func (k *walk) charge() {
	n := k.pending
	k.pending = 0
	switch {
	case k.l != nil:
		if err := k.l.Work(n); err != nil {
			stop(err)
		}
	case k.ctx != nil:
		if err := k.ctx.Err(); err != nil {
			stop(err)
		}
	default:
		if k.left -= n; k.left < 0 {
			stop(errDetached)
		}
	}
}

// finish charges what is left of the walk's work, and gives back the charge
// of text the walk does not keep.
func (k *walk) finish() {
	if k.pending > 0 {
		k.charge()
	}
	if !k.keep {
		k.release()
	}
}

// release gives back to the ledger what a walk whose text it does not keep
// charged for it.
func (k *walk) release() {
	k.l.Free(k.charged)
	k.charged = 0
}

// room makes room for n more bytes of text, charging the growth before it is
// allocated and stopping the walk past its bound.
func (k *walk) room(n int) {
	if len(k.buf)+n > cap(k.buf) {
		k.grow(n)
	}
}

// grow is room past the buffer's capacity.
func (k *walk) grow(n int) {
	if need := len(k.buf) - k.base + n; need > k.maxBytes || need < 0 {
		if k.ctx == nil && k.maxBytes == maxDetachedBytes {
			stop(errDetachedSz)
		}
		stop(fmt.Errorf("%w: a value's text of more than %d bytes", budget.ErrLimit, k.maxBytes))
	}
	grow := max(len(k.buf)+n, 2*cap(k.buf), 16)
	if k.maxBytes < math.MaxInt-k.base {
		grow = min(grow, k.base+k.maxBytes)
	}
	if over := int64(grow-k.from) - smallWalk; over > k.charged {
		if err := k.l.Alloc(over - k.charged); err != nil {
			stop(err)
		}
		k.charged = over
	}
	k.buf = slices.Grow(k.buf, grow-len(k.buf))
}

func (k *walk) write(s string) {
	if k.done {
		return
	}
	k.room(len(k.cut(s)))
	k.buf = append(k.buf, k.cut(s)...)
	k.over()
}

// cut is s, or for a prefix walk as much of it as takes the text past the
// limit.
func (k *walk) cut(s string) string {
	if k.limit > 0 {
		if rest := k.limit + 1 - (len(k.buf) - k.base); len(s) > rest {
			return s[:max(rest, 0)]
		}
	}
	return s
}

func (k *walk) writeByte(c byte) {
	if k.done {
		return
	}
	k.room(1)
	k.buf = append(k.buf, c)
	k.over()
}

// over marks a prefix walk done once its text passes the limit.
func (k *walk) over() {
	if k.limit > 0 && len(k.buf)-k.base > k.limit {
		k.done = true
	}
}

// dateText writes the date ms with the walk's date writer.
func (k *walk) dateText(ms float64) {
	var b [96]byte
	t := k.date(b[:0], ms)
	if k.done {
		return
	}
	k.room(len(t))
	k.buf = append(k.buf, t...)
	k.over()
}

func (k *walk) number(f float64) {
	if k.done {
		return
	}
	var nb [32]byte
	t := AppendJSNumber(nb[:0], f)
	k.room(len(t))
	k.buf = append(k.buf, t...)
	k.over()
}

// text is the walk's text as a string; the walk's buffer is not used again.
func (k *walk) text() string {
	b := k.buf[k.base:]
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

// checkUnits ends the walk when its text, a JavaScript string, is longer
// than MaxStringLength code units. A byte holds at most one code unit, so
// only text of more bytes than that is measured.
func (k *walk) checkUnits() {
	if b := k.buf[k.base:]; len(b) > MaxStringLength {
		if u := utf16Units(b); u > MaxStringLength {
			stop(fmt.Errorf("%w: a string of %d UTF-16 code units exceeds the limit of %d", budget.ErrLimit, u, MaxStringLength))
		}
	}
}

// utf16Units is the length in UTF-16 code units of UTF-8 (or WTF-8) text: one
// per character, two for one above U+FFFF.
func utf16Units(b []byte) int {
	n := 0
	for _, c := range b {
		if c&0xC0 != 0x80 {
			n++
		}
		if c >= 0xF0 {
			n++
		}
	}
	return n
}

// String is String(v): see Value.AsString.
func (w Walker) String(v Value) string {
	switch v.Kind() {
	case KindArr:
	case KindObj:
		return objectString(v.obj())
	case KindTimestamp:
		if w.date != nil {
			return string(w.date(nil, v.n()))
		}
		return v.primString()
	default:
		return v.primString()
	}
	items := v.Items()
	switch len(items) {
	case 0:
		return ""
	case 1:
		if items[0].Kind() != KindArr {
			if items[0].IsNullish() {
				return ""
			}
			return w.String(items[0])
		}
	}
	if s, ok := w.flatString(items); ok {
		return s
	}
	k := w.start(nil, maxStringBytes)
	k.keep = true
	k.str(v, 0)
	k.finish()
	k.checkUnits()
	return k.text()
}

// flatString is the String of a short array of numbers and strings (a
// coordinate pair, a two-line label), made without the walk's state; ok is
// false for anything else.
func (w Walker) flatString(items []Value) (string, bool) {
	if len(items) > 8 {
		return "", false
	}
	n := len(items)
	for _, it := range items {
		switch it.Kind() {
		case KindStr:
			n += len(it.s())
		case KindNum:
			n += 24
		default:
			return "", false
		}
	}
	if n > smallWalk {
		return "", false
	}
	var sb [128]byte
	b := sb[:0]
	for i, it := range items {
		if i > 0 {
			b = append(b, ',')
		}
		if it.Kind() == KindStr {
			b = append(b, it.s()...)
		} else {
			b = AppendJSNumber(b, it.n())
		}
	}
	return string(b), true
}

// AppendString appends String(v) to dst. The text is the caller's: what it
// adds past smallWalk bytes is charged while it is built, and given back
// once it is (the caller charges what it keeps).
func (w Walker) AppendString(dst []byte, v Value) []byte {
	switch v.Kind() {
	case KindStr:
		if len(v.s()) <= smallWalk {
			return append(dst, v.s()...)
		}
	case KindNum:
		return AppendJSNumber(dst, v.n())
	}
	k := w.start(dst, maxStringBytes)
	k.str(v, 0)
	k.finish()
	return k.buf
}

// AppendStringPrefix appends String(v) to dst, stopping once more than limit
// bytes are written (they may then end inside a character): the start of a
// value's text, for a message, without building the rest.
func (w Walker) AppendStringPrefix(dst []byte, v Value, limit int) []byte {
	k := w.start(dst, prefixBytes(limit))
	k.limit = max(limit, 1)
	k.str(v, 0)
	k.finish()
	return k.buf
}

// prefixBytes bounds the text of a prefix walk of limit bytes: each write is
// cut at the limit, but the last may be a JSON string's escapes (six bytes a
// byte) or a number.
func prefixBytes(limit int) int { return 6*max(limit, 1) + 64 }

func (k *walk) str(v Value, depth int) {
	if depth > MaxValueDepth {
		stop(errWalkDepth)
	}
	k.step()
	switch v.Kind() {
	case KindArr:
		for i, it := range v.arr().list() {
			if k.done {
				return
			}
			if i > 0 {
				k.writeByte(',')
			}
			if !it.IsNullish() {
				k.str(it, depth+1)
			}
		}
	case KindNum:
		k.number(v.n())
	case KindTimestamp:
		if k.date != nil {
			k.dateText(v.n())
		} else {
			k.number(v.n())
		}
	case KindStr:
		k.write(v.s())
	case KindObj:
		k.write(objectString(v.obj()))
	default:
		k.write(v.primString())
	}
}

// primString is String(v) of a value that is not an array or an object.
func (v Value) primString() string {
	switch v.Kind() {
	case KindStr:
		return v.s()
	case KindNum, KindTimestamp:
		return JSNumberString(v.n())
	case KindBool:
		if v.n() != 0 {
			return "true"
		}
		return "false"
	case KindNull:
		return "null"
	case KindUndefined:
		return "undefined"
	case KindPattern:
		return v.pat().String()
	}
	return ""
}

// plainHost says whether o is a plain object of Object.prototype: no host
// payload, or only a conversion scope (SetConversionScope).
func plainHost(o *Object) bool {
	if o.host == nil {
		return true
	}
	ph, ok := o.host.(protoHost)
	return ok && ph.proto.IsUndefined()
}

func objectString(o *Object) string {
	if o != nil && o.str != nil {
		return o.str(o)
	}
	if o != nil {
		if ph, ok := o.host.(protoHost); ok && ph.scope != nil && !ph.scope.ended {
			return protoString(o, ph.scope)
		}
	}
	checkToPrimitive(o)
	return "[object Object]"
}

// errNoPrimitive is the TypeError OrdinaryToPrimitive raises.
var errNoPrimitive = &Thrown{Name: "TypeError", Msg: "Cannot convert object to primitive value"}

// checkToPrimitive raises the TypeError of String(o) or Number(o) for a
// plain object that holds a toString member of its own: no JSON value is a
// function, so neither its toString nor the valueOf it inherits (which
// answers the object itself) gives a primitive. ({"toString": "x"} + "" is
// a TypeError in JavaScript.)
func checkToPrimitive(o *Object) {
	if o != nil && plainHost(o) && o.Has("toString") {
		panic(errNoPrimitive)
	}
}

// StringValue is vega-util's stringValue: see the package function.
func (w Walker) StringValue(v Value) string {
	switch v.Kind() {
	case KindArr, KindStr, KindObj, KindTimestamp:
	default:
		return v.primString()
	}
	k := w.start(nil, maxStringBytes)
	k.keep = true
	k.sval(v, 0)
	k.finish()
	k.checkUnits()
	return k.text()
}

// AppendStringValuePrefix appends stringValue(v) to dst, stopping once more
// than limit bytes are written: the start of the text, for a message.
func (w Walker) AppendStringValuePrefix(dst []byte, v Value, limit int) []byte {
	k := w.start(dst, prefixBytes(limit))
	k.limit = max(limit, 1)
	k.sval(v, 0)
	k.finish()
	return k.buf
}

func (k *walk) sval(v Value, depth int) {
	switch v.Kind() {
	case KindArr:
		if depth > MaxValueDepth {
			stop(errWalkDepth)
		}
		k.step()
		k.writeByte('[')
		for i, it := range v.arr().list() {
			if k.done {
				return
			}
			if i > 0 {
				k.writeByte(',')
			}
			switch {
			case it.IsNull():
				k.write("null")
			case !it.IsUndefined():
				k.sval(it, depth+1)
			}
		}
		k.writeByte(']')
	case KindStr, KindObj, KindTimestamp:
		// JSON (a Date, an object to stringValue, is its toJSON), with
		// U+2028 and U+2029 escaped: the expression parser takes them for
		// line terminators.
		at := len(k.buf)
		k.json(v, "", "", depth)
		k.escapeSeparators(at)
	default:
		k.step()
		k.write(v.primString())
	}
}

// escapeSeparators escapes U+2028 and U+2029 in the text written since at.
func (k *walk) escapeSeparators(at int) {
	n := 0
	for i := at; i+2 < len(k.buf); i++ {
		if k.buf[i] == 0xE2 && k.buf[i+1] == 0x80 && (k.buf[i+2] == 0xA8 || k.buf[i+2] == 0xA9) {
			n++
		}
	}
	if n == 0 {
		return
	}
	// Each three-byte separator becomes six bytes of escape.
	k.room(3 * n)
	end := len(k.buf)
	k.buf = k.buf[:end+3*n]
	j := len(k.buf)
	for i := end - 1; i >= at; i-- {
		if i >= at+2 && k.buf[i-2] == 0xE2 && k.buf[i-1] == 0x80 && (k.buf[i] == 0xA8 || k.buf[i] == 0xA9) {
			last := '8' + k.buf[i] - 0xA8 // read before the escape overwrites it
			j -= 6
			copy(k.buf[j:], `\u202`)
			k.buf[j+5] = last
			i -= 2
			continue
		}
		j--
		k.buf[j] = k.buf[i]
	}
	k.over()
}

// AppendJSON appends JSON.stringify(v) to dst: see the package function.
func (w Walker) AppendJSON(dst []byte, v Value) []byte {
	return w.AppendJSONIndent(dst, v, "")
}

// AppendJSONIndent appends JSON.stringify(v, null, indent) to dst.
func (w Walker) AppendJSONIndent(dst []byte, v Value, indent string) []byte {
	prefix := ""
	if indent != "" {
		prefix = "\n"
	}
	k := w.start(dst, math.MaxInt)
	k.json(v, indent, prefix, 0)
	k.finish()
	return k.buf
}

// appendJSONPrefix appends JSON.stringify(v), stopping once more than limit
// bytes are written: the start of the text, for a message.
func (w Walker) appendJSONPrefix(dst []byte, v Value, limit int) []byte {
	k := w.start(dst, prefixBytes(limit))
	k.limit = max(limit, 1)
	k.json(v, "", "", 0)
	k.finish()
	return k.buf
}

func (k *walk) json(v Value, indent, prefix string, depth int) {
	if depth > MaxValueDepth {
		stop(errWalkDepth)
	}
	k.step()
	if IsFunction(v) {
		k.write("null") // as in an array; a member is left out
		return
	}
	switch v.Kind() {
	case KindUndefined, KindNull:
		k.write("null")
	case KindBool:
		if v.n() != 0 {
			k.write("true")
		} else {
			k.write("false")
		}
	case KindNum:
		if math.IsNaN(v.n()) || math.IsInf(v.n(), 0) {
			k.write("null")
		} else {
			k.number(v.n())
		}
	case KindTimestamp:
		// Date.prototype.toJSON: the ISO string, null for an Invalid Date.
		var b [32]byte
		if iso, ok := AppendISODate(b[:0], v.n()); ok {
			k.room(len(iso) + 2)
			k.buf = append(k.buf, '"')
			k.buf = append(k.buf, iso...)
			k.buf = append(k.buf, '"')
			k.over()
		} else {
			k.write("null")
		}
	case KindStr:
		k.quoted(v.s())
	case KindPattern:
		k.write("{}")
	case KindArr:
		items := v.arr().list()
		if len(items) == 0 {
			k.write("[]")
			return
		}
		inner := prefix + indent
		k.writeByte('[')
		for i, it := range items {
			if k.done {
				return
			}
			if i > 0 {
				k.writeByte(',')
			}
			if indent != "" {
				k.write(inner)
			}
			k.json(it, indent, inner, depth+1)
		}
		if indent != "" {
			k.write(prefix)
		}
		k.writeByte(']')
	case KindObj:
		o := v.obj()
		inner := prefix + indent
		k.writeByte('{')
		n := 0
		for i := 0; i < o.Len(); i++ {
			if k.done {
				return
			}
			val := o.ValueAt(i)
			if val.Kind() == KindUndefined || IsFunction(val) {
				continue
			}
			if n > 0 {
				k.writeByte(',')
			}
			n++
			if indent != "" {
				k.write(inner)
			}
			k.quoted(o.KeyAt(i))
			k.writeByte(':')
			if indent != "" {
				k.writeByte(' ')
			}
			k.json(val, indent, inner, depth+1)
		}
		if n > 0 && indent != "" {
			k.write(prefix)
		}
		k.writeByte('}')
	}
}

// quoted writes s as a JSON string (appendQuoted), its length measured and
// charged first.
func (k *walk) quoted(s string) {
	if k.done {
		return
	}
	s = k.cut(s)
	if plainJSON(s) {
		k.room(len(s) + 2)
		k.buf = append(k.buf, '"')
		k.buf = append(k.buf, s...)
		k.buf = append(k.buf, '"')
	} else {
		k.room(quotedLen(s))
		k.buf = appendQuoted(k.buf, s)
	}
	k.over()
}

// Equal is deep structural equality (not JavaScript ===): see the package
// function.
func (w Walker) Equal(a, b Value) bool {
	switch a.Kind() {
	case KindArr, KindObj:
		if a.Kind() != b.Kind() {
			return false
		}
		if a.p == b.p {
			return true
		}
		// A small value (a dash pattern, a coordinate pair) compares
		// without the walk's state; a larger one is walked, metered.
		budget := smallEqual
		if eq, done := equalSmall(a, b, &budget); done {
			return eq
		}
		k := w.start(nil, 0)
		eq := k.equal(a, b, 0)
		k.finish()
		return eq
	}
	return equalPrimitive(a, b)
}

// smallEqual is how many values Equal compares before it walks them.
const smallEqual = 64

// equalSmall is Equal of values of at most *budget parts in all; done is
// false, with eq meaningless, once they hold more.
func equalSmall(a, b Value, budget *int) (eq, done bool) {
	if *budget--; *budget < 0 {
		return false, false
	}
	switch a.Kind() {
	case KindArr:
		if b.Kind() != KindArr {
			return false, true
		}
		if a.p == b.p {
			return true, true
		}
		x, y := a.arr().list(), b.arr().list()
		if len(x) != len(y) {
			return false, true
		}
		for i := range x {
			if x[i].Kind() == KindNum && y[i].Kind() == KindNum {
				// The common element, compared in place.
				if *budget--; *budget < 0 {
					return false, false
				}
				if f, g := x[i].n(), y[i].n(); f != g && !(f != f && g != g) {
					return false, true
				}
				continue
			}
			if eq, done := equalSmall(x[i], y[i], budget); !done || !eq {
				return eq, done
			}
		}
		return true, true
	case KindObj:
		return false, false // objects are walked
	}
	return equalPrimitive(a, b), true
}

func equalPrimitive(a, b Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case KindUndefined, KindNull:
		return true
	case KindBool:
		return a.n() == b.n()
	case KindNum, KindTimestamp:
		return a.n() == b.n() || (math.IsNaN(a.n()) && math.IsNaN(b.n()))
	case KindStr:
		return a.s() == b.s()
	case KindPattern:
		p, q := a.pat(), b.pat()
		return p == q || (p.Source == q.Source && p.Flags == q.Flags)
	}
	return false
}

func (k *walk) equal(a, b Value, depth int) bool {
	if depth > MaxValueDepth {
		stop(errWalkDepth)
	}
	k.step()
	switch a.Kind() {
	case KindArr:
		if b.Kind() != KindArr {
			return false
		}
		if a.p == b.p {
			// The same array: equal, however large, without a walk.
			return true
		}
		x, y := a.arr().list(), b.arr().list()
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if !k.equal(x[i], y[i], depth+1) {
				return false
			}
		}
		return true
	case KindObj:
		if b.Kind() != KindObj {
			return false
		}
		x, y := a.obj(), b.obj()
		if x == y {
			return true
		}
		if !plainHost(x) || !plainHost(y) {
			return false
		}
		if x.Len() != y.Len() {
			return false
		}
		for i := 0; i < x.Len(); i++ {
			bv, ok := y.Get(x.KeyAt(i))
			if !ok || !k.equal(x.ValueAt(i), bv, depth+1) {
				return false
			}
		}
		return true
	}
	return equalPrimitive(a, b)
}

// Relate is the abstract relational comparison: see the package function.
func (w Walker) Relate(a, b Value) (c int, ok bool) {
	if a.Kind() == KindNum && b.Kind() == KindNum {
		return cmpFloat(a.n(), b.n())
	}
	pa := w.primitive(a)
	pb := w.primitive(b)
	return RelatePrimitives(pa, pb)
}

// primitive is ToPrimitive with hint "number" as the relational operators
// use it: a date reads as its epoch, an array or object as its string form,
// which is dropped once compared (the walk gives back its charge as it ends).
func (w Walker) primitive(v Value) Value {
	switch v.Kind() {
	case KindTimestamp:
		return Num(v.n())
	case KindObj:
		return Str(objectString(v.obj()))
	case KindPattern:
		return Str(v.pat().String())
	case KindArr:
		k := w.start(nil, maxStringBytes)
		k.str(v, 0)
		k.finish()
		k.checkUnits()
		return Str(k.text())
	}
	return v
}

// MessageBytes is how much of a value's text a message quotes: warnings and
// errors are cut to 1 KiB where they are reported.
const MessageBytes = 1 << 10

// StringPrefix is the start of String(v), cut at a character boundary after
// at most limit bytes: the text of a value for a message (upstream's
// `'Invalid date: ' + d`), which costs no more than limit, so it needs no
// call's Walker.
func StringPrefix(v Value, limit int) string {
	if v.Kind() == KindStr {
		return string(cutUTF8([]byte(v.s()[:min(len(v.s()), limit+4)]), limit))
	}
	var w Walker
	return string(cutUTF8(w.AppendStringPrefix(nil, v, limit), limit))
}

// StringIs reports whether String(v) is s: `v == s` read as text, as a
// keyword is matched by its text (`String(x) === 'radial'`). It reads at
// most len(s)+1 bytes of v's text, so it needs no call's Walker.
func (v Value) StringIs(s string) bool {
	if v.Kind() == KindStr {
		return v.s() == s
	}
	var w Walker
	var buf [64]byte
	return string(w.AppendStringPrefix(buf[:0], v, len(s))) == s
}

// PropertyKey is the property key v names when upstream reads `obj[v]` or
// `v in obj`: String(v) (ToPropertyKey), so the array ["a"] names "a" and
// undefined names "undefined". ok is false when the key is longer than limit
// bytes: a table whose keys are at most limit bytes long holds no such key,
// and the key is not built. A string is its own key whatever its length.
func (v Value) PropertyKey(limit int) (key string, ok bool) {
	if v.Kind() == KindStr {
		return v.s(), true
	}
	var w Walker
	b := w.AppendStringPrefix(nil, v, limit)
	if len(b) > limit {
		return "", false
	}
	return string(b), true
}

// Strict is the string v holds when upstream compares it with ===, a switch
// or Set.has: ok only for a string, so ["linear"] names nothing.
func (v Value) Strict() (s string, ok bool) {
	if v.Kind() != KindStr {
		return "", false
	}
	return v.s(), true
}

// WalkMeter meters a walk of a value made outside this package, by code with
// rules of its own (JSON.stringify's for a date and a cycle, a comparison
// that is not Equal's): each value it visits is a Step and the text it keeps
// is charged with Text, as the walks here charge theirs, so the same bounds
// end it. Make one per walk with Walker.Meter, and call Done at its end.
type WalkMeter struct{ k walk }

// Meter starts a metered walk.
func (w Walker) Meter() WalkMeter { return WalkMeter{k: w.start(nil, maxStringBytes)} }

// Step counts one value visited at depth (the root is at 0), stopping the
// walk past MaxValueDepth or past the call's bounds.
func (m *WalkMeter) Step(depth int) {
	if depth > MaxValueDepth {
		stop(errWalkDepth)
	}
	m.k.step()
}

// Text charges the walk's text grown to n bytes, before it is grown: what
// passes smallWalk bytes is charged to the call's ledger until Done, and
// more than the bound of a value's text (three times MaxStringLength bytes,
// or the detached bound) stops the walk.
func (m *WalkMeter) Text(n int) {
	if n > m.k.maxBytes {
		if m.k.ctx == nil && m.k.maxBytes == maxDetachedBytes {
			stop(errDetachedSz)
		}
		stop(fmt.Errorf("%w: a value's text of more than %d bytes", budget.ErrLimit, m.k.maxBytes))
	}
	if over := int64(n) - smallWalk; over > m.k.charged {
		// Ahead, doubling, as the caller's buffer grows: an append per
		// value costs a comparison, not a charge.
		want := min(max(over, 2*m.k.charged, 4096), int64(m.k.maxBytes))
		if err := m.k.l.Alloc(want - m.k.charged); err != nil {
			stop(err)
		}
		m.k.charged = want
	}
}

// Done charges what is left of the walk's work and gives back the charge of
// its text, which is the caller's.
func (m *WalkMeter) Done() { m.k.finish() }

// Join is Array.prototype.join(sep) of items: each element's String, null
// and undefined as "", separated by sep (an element that is an array joins
// its own with commas, as its String does).
func (w Walker) Join(items []Value, sep string) string {
	if len(items) == 1 {
		if items[0].IsNullish() {
			return ""
		}
		return w.String(items[0])
	}
	k := w.start(nil, maxStringBytes)
	k.keep = true
	for i, it := range items {
		if i > 0 {
			k.step()
			k.write(sep)
		}
		if !it.IsNullish() {
			k.str(it, 1)
		}
	}
	k.finish()
	k.checkUnits()
	return k.text()
}

// Work charges n steps of work done over a value outside a walk (each
// evaluation of an interpolator that rebuilds an array element by element),
// polling the call's context; past the deadline the call stops with a
// *budget.Stop. The detached Walker charges nothing: what it builds was
// bounded when it was walked.
func (w Walker) Work(n int64) {
	switch {
	case w.l != nil:
		w.l.MustWork(n)
	case w.ctx != nil:
		if err := w.ctx.Err(); err != nil {
			stop(err)
		}
	}
}

// Keep charges the call's ledger for n bytes about to be allocated and kept
// for the rest of the call (an interpolated array an item holds), stopping
// the call past its memory limit.
func (w Walker) Keep(n int64) {
	if w.l != nil {
		w.l.MustAlloc(n)
	}
}
