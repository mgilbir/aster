package scale

import (
	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

// Scale type names, as in vega-scale.
const (
	TypeIdentity   = "identity"
	TypeLinear     = "linear"
	TypeLog        = "log"
	TypePow        = "pow"
	TypeSqrt       = "sqrt"
	TypeSymlog     = "symlog"
	TypeTime       = "time"
	TypeUTC        = "utc"
	TypeSequential = "sequential"
	TypeDiverging  = "diverging"
	TypeQuantile   = "quantile"
	TypeQuantize   = "quantize"
	TypeThreshold  = "threshold"
	TypeOrdinal    = "ordinal"
	TypePoint      = "point"
	TypeBand       = "band"
	TypeBinOrdinal = "bin-ordinal"
)

// Metadata flags of a registered scale type (vega-scale's scale metadata).
type flags uint8

const (
	fContinuous flags = 1 << iota
	fDiscrete
	fDiscretizing
	fInterpolating
	fLog
	fTemporal
	fQuantile
)

type registration struct {
	make  func() Scale
	flags flags
}

var registry = map[string]registration{}

func register(typ string, f flags, make func() Scale) {
	registry[typ] = registration{make: make, flags: f}
}

func init() {
	const c, d, z, i, l = fContinuous, fDiscrete, fDiscretizing, fInterpolating, fLog
	register(TypeIdentity, 0, func() Scale { return NewIdentity() })
	register(TypeLinear, c, func() Scale { return NewLinear() })
	register(TypeLog, c|l, func() Scale { return NewLog() })
	register(TypePow, c, func() Scale { return NewPow() })
	register(TypeSqrt, c, func() Scale { return NewSqrt() })
	register(TypeSymlog, c, func() Scale { return NewSymlog() })
	// backwards compatibility: plain "sequential" is sequential-linear
	register(TypeTime, c|fTemporal, func() Scale { return NewTime(LocalZone) })
	register(TypeUTC, c|fTemporal, func() Scale { return NewUTC() })
	register(TypeSequential, c|i, func() Scale { return NewSequential() })
	register("sequential-linear", c|i, func() Scale { return NewSequential() })
	register("sequential-log", c|i|l, func() Scale { return NewSequentialLog() })
	register("sequential-pow", c|i, func() Scale { return NewSequentialPow() })
	register("sequential-sqrt", c|i, func() Scale { return NewSequentialSqrt() })
	register("sequential-symlog", c|i, func() Scale { return NewSequentialSymlog() })
	register("diverging-linear", c|i, func() Scale { return NewDiverging() })
	register("diverging-log", c|i|l, func() Scale { return NewDivergingLog() })
	register("diverging-pow", c|i, func() Scale { return NewDivergingPow() })
	register("diverging-sqrt", c|i, func() Scale { return NewDivergingSqrt() })
	register("diverging-symlog", c|i, func() Scale { return NewDivergingSymlog() })
	register(TypeQuantile, z|fQuantile, func() Scale { return NewQuantile() })
	register(TypeQuantize, z, func() Scale { return NewQuantize() })
	register(TypeThreshold, z, func() Scale { return NewThreshold() })
	register(TypeBinOrdinal, d|z, func() Scale { return NewBinOrdinal() })
	register(TypeOrdinal, d, func() Scale { return NewOrdinal() })
	register(TypeBand, d, func() Scale { return NewBand() })
	register(TypePoint, d, func() Scale { return NewPoint() })
}

// New constructs a scale of the given vega-scale type with d3's defaults, or
// reports false for an unknown type. The scale's Type() is the name asked for.
func New(typ string) (Scale, bool) {
	r, ok := registry[typ]
	if !ok {
		return nil, false
	}
	s := r.make()
	if t, ok := s.(Typed); ok {
		t.SetType(typ)
	}
	return s, true
}

// IsValidScaleType reports whether typ is a registered scale type.
func IsValidScaleType(typ string) bool { _, ok := registry[typ]; return ok }

func hasFlag(typ string, f flags) bool { return registry[typ].flags&f != 0 }

// IsContinuous: the scale is defined over a continuous-valued domain.
func IsContinuous(typ string) bool { return hasFlag(typ, fContinuous) }

// IsDiscrete: the scale is defined over a discrete domain and range.
func IsDiscrete(typ string) bool { return hasFlag(typ, fDiscrete) }

// IsDiscretizing: the scale discretizes a continuous domain to a discrete range.
func IsDiscretizing(typ string) bool { return hasFlag(typ, fDiscretizing) }

// IsInterpolating: the scale range is defined using a colour interpolator.
func IsInterpolating(typ string) bool { return hasFlag(typ, fInterpolating) }

// IsLogarithmic: the scale performs a logarithmic transform of its domain.
func IsLogarithmic(typ string) bool { return hasFlag(typ, fLog) }

// IsTemporal: the scale domain is defined over date-time values.
func IsTemporal(typ string) bool { return hasFlag(typ, fTemporal) }

// IsQuantile reports the quantile scale type.
func IsQuantile(typ string) bool { return hasFlag(typ, fQuantile) }

// NewIn is New with an explicit calendar for the "time" scale type (the zone
// of the runtime's locale); every other type ignores it.
func NewIn(typ string, local format.Zone) (Scale, bool) {
	if typ == TypeTime {
		s := NewTime(local)
		s.SetType(typ)
		return s, true
	}
	return New(typ)
}

// ScaleCopy is vega-scale's scaleCopy: a copy that keeps the type tag.
func ScaleCopy(s Scale) Scale {
	c := s.Copy()
	if t, ok := c.(Typed); ok {
		t.SetType(s.Type())
	}
	return c
}

// InvertRange is vega-scale's invertRange, attached to a scale on creation:
// scales with invert map a range interval to a domain interval, scales with
// only invertExtent map it to the domain extent of the covered range values,
// and band/point scales list the domain values. ok is false when the scale
// has none, or upstream answers undefined.
func InvertRange(s Scale, lo, hi jsval.Value) (jsval.Value, bool) {
	if ri, ok := s.(RangeInverter); ok {
		return ri.InvertRange(lo, hi)
	}
	if ei, ok := s.(interface {
		Scale
		ExtentInverter
	}); ok {
		return InvertRangeExtent(ei, lo, hi)
	}
	return jsval.Undefined, false
}

// Set applies the property setter called name (as vega-encode's
// `scale[key](value)` does) and reports whether this scale has such a setter.
// Supported names: domain, range, rangeRound, clamp, round, unknown, base,
// exponent, constant, padding, paddingInner, paddingOuter, align, zero-arg
// setters are not covered. Numeric setters coerce with Number().
func Set(s Scale, name string, v jsval.Value) bool {
	switch name {
	case "domain":
		s.SetDomain(v.Items())
		return true
	case "range":
		s.SetRange(v.Items())
		return true
	case "rangeRound":
		if r, ok := s.(Rounder); ok {
			r.SetRangeRound(v.Items())
			return true
		}
	case "clamp":
		if c, ok := s.(Clamper); ok {
			c.SetClamp(v.IsTruthy())
			return true
		}
	case "round":
		switch x := s.(type) {
		case Bander:
			x.SetRound(v.IsTruthy())
			return true
		case *Radial:
			x.SetRound(v.IsTruthy())
			return true
		}
	case "unknown":
		if u, ok := s.(Unknowner); ok {
			u.SetUnknown(v)
			return true
		}
	case "base":
		switch x := s.(type) {
		case *Continuous:
			return x.SetBase(jsval.ToNumber(v))
		case *Sequential:
			return x.SetBase(jsval.ToNumber(v))
		case *Diverging:
			return x.SetBase(jsval.ToNumber(v))
		}
	case "exponent":
		switch x := s.(type) {
		case *Continuous:
			return x.SetExponent(jsval.ToNumber(v))
		case *Sequential:
			return x.SetExponent(jsval.ToNumber(v))
		case *Diverging:
			return x.SetExponent(jsval.ToNumber(v))
		}
	case "constant":
		switch x := s.(type) {
		case *Continuous:
			return x.SetConstant(jsval.ToNumber(v))
		case *Sequential:
			return x.SetConstant(jsval.ToNumber(v))
		case *Diverging:
			return x.SetConstant(jsval.ToNumber(v))
		}
	case "padding", "paddingInner", "paddingOuter", "align":
		b, ok := s.(*Band)
		if !ok {
			return false
		}
		f := jsval.ToNumber(v)
		switch name {
		case "padding":
			b.SetPadding(f)
		case "paddingInner":
			if b.point {
				return false // deleted from point scales
			}
			b.SetPaddingInner(f)
		case "paddingOuter":
			b.SetPaddingOuter(f)
		default:
			b.SetAlign(f)
		}
		return true
	}
	return false
}
