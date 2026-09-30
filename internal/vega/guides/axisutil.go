package guides

import (
	"strings"

	"github.com/mgilbir/aster/internal/jsval"
)

// The helpers below build encoders that depend on the axis orientation. The
// orient may be a signal, in which case a signal expression (or a rule set)
// replaces the static choice.

func isXOrient(o Value) bool { return o.IsStr() && (o.StrValue() == top || o.StrValue() == bottom) }

// getSign is the sign coefficient of an orient: a when left/top, else b.
func getSign(orient Value, a, b float64) Value {
	if isSignal(orient) {
		o := signalOf(orient)
		return ifExpr(o+" === 'left' || "+o+" === 'top'", jsval.Num(a), jsval.Num(b))
	}
	if orient.IsStr() && (orient.StrValue() == left || orient.StrValue() == top) {
		return jsval.Num(a)
	}
	return jsval.Num(b)
}

// ifX conditions on an x-direction axis (top/bottom).
func ifX(orient, a, b Value) Value {
	if isSignal(orient) {
		o := signalOf(orient)
		return ifEnc(o+" === 'top' || "+o+" === 'bottom'", a, b)
	}
	if isXOrient(orient) {
		return a
	}
	return b
}

// ifY conditions on a y-direction axis (left/right and anything else).
func ifY(orient, a, b Value) Value {
	if isSignal(orient) {
		o := signalOf(orient)
		return ifEnc(o+" !== 'top' && "+o+" !== 'bottom'", a, b)
	}
	if isXOrient(orient) {
		return b
	}
	return a
}

func ifTop(orient Value, a, b string) Value {
	if isSignal(orient) {
		return ifExpr(signalOf(orient)+" === 'top'", jsval.Str(a), jsval.Str(b))
	}
	if orient.IsStr() && orient.StrValue() == top {
		return objv("value", a)
	}
	return objv("value", b)
}

func ifRight(orient Value, a, b string) Value {
	if isSignal(orient) {
		return ifExpr(signalOf(orient)+" === 'right'", jsval.Str(a), jsval.Str(b))
	}
	if orient.IsStr() && orient.StrValue() == right {
		return objv("value", a)
	}
	return objv("value", b)
}

func isSimple(enc Value) bool { return enc.IsNullish() || (enc.IsObj() && enc.Len() == 1) }

// ifEnc produces `{signal: "test ? (a) : (b)"}` when both branches are simple
// values or signals, and an encoder rule set otherwise.
func ifEnc(test string, a, b Value) Value {
	if !a.IsNullish() {
		a = jsval.Obj(encoder(a))
	}
	if !b.IsNullish() {
		b = jsval.Obj(encoder(b))
	}
	if isSimple(a) && isSimple(b) {
		return objv("signal", test+" ? ("+simpleExpr(a)+") : ("+simpleExpr(b)+")")
	}
	rule := extend(obj("test", test), a)
	rules := []Value{jsval.Obj(rule)}
	if b.IsTruthy() {
		rules = append(rules, b)
	}
	return jsval.Arr(rules)
}

// simpleExpr renders a one-key encoder as an expression; a missing branch is
// the JavaScript literal null.
func simpleExpr(enc Value) string {
	if enc.IsNullish() {
		return "null"
	}
	if s := enc.Get("signal"); s.IsTruthy() {
		return s.AsString()
	}
	v := enc.Get("value")
	if v.IsUndefined() {
		return "undefined"
	}
	return stringValue(v)
}

// toExpr renders a raw config value as an expression operand.
func toExpr(v Value) string {
	switch {
	case isSignal(v):
		return signalOf(v)
	case v.IsNullish():
		return "null"
	}
	return stringValue(v)
}

func ifExpr(test string, a, b Value) Value {
	return objv("signal", test+" ? ("+toExpr(a)+") : ("+toExpr(b)+")")
}

// ifOrient selects among four orient-specific values with a chain of
// conditionals that ends in `(null)`; patch replaces that tail.
func ifOrient(orient string, t, b, l, r Value) Value {
	var s strings.Builder
	if !l.IsNullish() {
		s.WriteString(orient + " === 'left' ? (" + toExpr(l) + ") : ")
	}
	if !b.IsNullish() {
		s.WriteString(orient + " === 'bottom' ? (" + toExpr(b) + ") : ")
	}
	if !r.IsNullish() {
		s.WriteString(orient + " === 'right' ? (" + toExpr(r) + ") : ")
	}
	if !t.IsNullish() {
		s.WriteString(orient + " === 'top' ? (" + toExpr(t) + ") : ")
	}
	s.WriteString("(null)")
	return objv("signal", s.String())
}

// mult scales a sign by a constant: a static sign folds to a number, a signal
// sign becomes an expression.
func mult(sign Value, v float64) Value {
	if v == 0 {
		return jsval.Num(0)
	}
	if isSignal(sign) {
		return objv("signal", "("+signalOf(sign)+") * "+jsval.JSNumberString(v))
	}
	return objv("value", sign.NumValue()*v)
}

// patch completes an ifOrient chain whose tail is `(null)` with the signal of
// base; anything else is returned unchanged.
func patch(v, base Value) Value {
	s := v.Get("signal")
	if s.IsStr() && strings.HasSuffix(s.StrValue(), "(null)") {
		return objv("signal", s.StrValue()[:len(s.StrValue())-6]+base.Get("signal").AsString())
	}
	return v
}
