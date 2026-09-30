package scene

import "math"

// Num is a float64 that remembers whether it was ever assigned.
//
// Vega's scenegraph items are plain JavaScript objects, and the renderer
// constantly distinguishes "property absent" (undefined/null) from "property is
// zero or NaN": `item.x2 != null`, `item.strokeWidth != null ? +sw : 1`,
// `item.x || 0`. Num keeps that distinction in eight bytes, with the zero Num
// meaning "unset", so a zero Item has nothing set and needs no constructor.
//
// The encoding stores the IEEE bits XORed with a reserved quiet-NaN payload; the
// all-zero word therefore decodes to that payload (which N never produces from
// a real value, because real NaNs are canonicalised).
type Num struct{ bits uint64 }

// unsetBits is a quiet NaN with a payload no arithmetic produces.
const unsetBits uint64 = 0x7FF8_5A5A_0000_0001

// N wraps a value as a set Num. NaN is stored as the canonical NaN, which is
// distinguishable from the unset state.
func N(v float64) Num {
	if v != v {
		v = math.NaN()
	}
	return Num{math.Float64bits(v) ^ unsetBits}
}

// Set reports whether a value was assigned (JavaScript's `v != null`).
func (n Num) Set() bool { return n.bits != 0 }

// Val returns the value; an unset Num reads as NaN, so comparisons such as
// `n.Val() > 0` are false for unset exactly as `undefined > 0` is in
// JavaScript.
func (n Num) Val() float64 { return math.Float64frombits(n.bits ^ unsetBits) }

// Zero is JavaScript's `v || 0`: unset and NaN both read as 0.
func (n Num) Zero() float64 {
	v := n.Val()
	if v != v {
		return 0
	}
	return v
}

// Or is `value(v, def)`: def when unset, otherwise the stored value (NaN kept).
func (n Num) Or(def float64) float64 {
	if n.bits == 0 {
		return def
	}
	return n.Val()
}

// Truthy is JavaScript truthiness of the number (non-zero and not NaN).
func (n Num) Truthy() bool {
	v := n.Val()
	return v != 0 && v == v
}

// Tri is a three-valued boolean: unset, false or true. Zero value is unset.
type Tri uint8

const (
	// Unset is an absent property.
	Unset Tri = iota
	// No is an explicit false.
	No
	// Yes is an explicit true.
	Yes
)

// B converts a bool to an explicit Tri.
func B(b bool) Tri {
	if b {
		return Yes
	}
	return No
}

// IsFalse reports an explicit false (`v === false`).
func (t Tri) IsFalse() bool { return t == No }

// IsTrue reports truthiness of the stored value (unset is falsy).
func (t Tri) IsTrue() bool { return t == Yes }

// Paint is the value of a fill or stroke property: absent, an explicit null, a
// colour string (possibly empty) or a gradient. Upstream tells `undefined` from
// `null` in exactly one place (the group foreground path), hence the two
// non-value states.
type Paint struct {
	kind paintKind
	s    string
	g    *Gradient
}

type paintKind uint8

const (
	paintUnset paintKind = iota
	paintNull
	paintColor
	paintGradient
)

// Color makes a colour paint. The empty string is an explicit empty colour,
// which upstream still emits as an attribute but treats as falsy.
func Color(s string) Paint { return Paint{kind: paintColor, s: s} }

// GradientPaint makes a gradient paint; identity of g matters, since gradient
// definitions are keyed by object.
func GradientPaint(g *Gradient) Paint {
	if g == nil {
		return Paint{}
	}
	return Paint{kind: paintGradient, g: g}
}

// NullPaint is an explicit null.
func NullPaint() Paint { return Paint{kind: paintNull} }

// IsNull is `v === null`.
func (p Paint) IsNull() bool { return p.kind == paintNull }

// Present is `v != null`: a colour (even empty) or gradient.
func (p Paint) Present() bool { return p.kind >= paintColor }

// Truthy is JavaScript truthiness: a non-empty colour or any gradient.
func (p Paint) Truthy() bool {
	return p.kind == paintGradient || (p.kind == paintColor && p.s != "")
}

// Str returns the colour string ("" for gradients and absent values).
func (p Paint) Str() string { return p.s }

// Gradient returns the gradient, or nil.
func (p Paint) Gradient() *Gradient { return p.g }

// Gradient is a Vega gradient object: a linear or radial colour ramp.
//
// Coordinates are Num because upstream fills in the defaults lazily, and only
// for gradients that have no id yet (see the SVG renderer).
type Gradient struct {
	Radial bool
	// ID, when non-empty, names the gradient; the renderer then trusts the
	// coordinates as given instead of filling in defaults.
	ID string
	// X1,Y1,X2,Y2 are the linear endpoints (radial: focal centre, outer centre).
	X1, Y1, X2, Y2 Num
	// R1, R2 are the radial inner and outer radii.
	R1, R2 Num
	Stops  []GradientStop
}

// GradientStop is one colour stop.
type GradientStop struct {
	Offset float64
	Color  string
}
