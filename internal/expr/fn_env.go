package expr

import (
	"math"

	"github.com/mgilbir/aster/internal/jsmath"
	"github.com/mgilbir/aster/internal/jsval"
)

// literalArg checks that argument i of a call is a literal, as vega-functions'
// dependency visitors require, and returns its string form.
func literalArg(n *Node, i int, msg string) (string, error) {
	if i >= len(n.Elems) || n.Elems[i].Kind != KindLiteral {
		return "", cerr("%s", msg)
	}
	return n.Elems[i].Value.AsString(), nil
}

func dataVisitor(c *compiler, n *Node) error {
	name, err := literalArg(n, 0, "First argument to data functions must be a string literal.")
	if err != nil {
		return err
	}
	c.addData(name)
	return nil
}

func indataVisitor(c *compiler, n *Node) error {
	name, err := literalArg(n, 0, "First argument to indata must be a string literal.")
	if err != nil {
		return err
	}
	field, err := literalArg(n, 1, "Second argument to indata must be a string literal.")
	if err != nil {
		return err
	}
	c.addIndex(IndexDep{Data: name, Field: field})
	c.addData(name)
	c.require(name)
	return nil
}

// scaleVisitor records a literal scale name, or "all scales" for an indirect
// lookup.
func scaleVisitor(c *compiler, n *Node) error {
	if len(n.Elems) == 0 {
		return cerr("Missing scale argument.")
	}
	if a := n.Elems[0]; a.Kind == KindLiteral {
		c.addScale(a.Value.AsString())
	} else {
		c.allScal = true
	}
	return nil
}

func selectionVisitor(c *compiler, n *Node) error {
	name, err := literalArg(n, 0, "First argument to selection functions must be a string literal.")
	if err != nil {
		return err
	}
	if len(n.Elems) >= 2 {
		last := n.Elems[len(n.Elems)-1]
		if last.Kind == KindLiteral && last.Value.IsStr() && last.Value.StrValue() == "intersect" {
			c.addIndex(IndexDep{Data: name, Field: "unit"})
		}
	}
	c.addData(name)
	c.require(name)
	return nil
}

func defv(name string, min int, visit func(c *compiler, n *Node) error, f builtinFn) {
	def(name, &funcDef{fn: f, min: min, visit: visit})
}

// emptyPair is [undefined, undefined], what the view-size functions return
// when there is no container or window.
func emptyPair() jsval.Value { return jsval.ArrOf(jsval.Undefined, jsval.Undefined) }

func (s *Scope) log(level LogLevel, args []jsval.Value) jsval.Value {
	if s.logger != nil {
		s.logger.Log(level, append([]jsval.Value(nil), args...))
	}
	if len(args) == 0 {
		return jsval.Undefined
	}
	return args[len(args)-1]
}

func init() {
	// ---- data ----
	defv("data", 1, dataVisitor, func(s *Scope, args []jsval.Value) jsval.Value {
		if s.data != nil {
			if v, ok := s.data.Data(s.str(args[0])); ok {
				return v
			}
		}
		return jsval.Arr(nil)
	})
	defv("indata", 3, indataVisitor, func(s *Scope, args []jsval.Value) jsval.Value {
		if s.indata == nil {
			return jsval.Undefined
		}
		if n, ok := s.indata.InData(s.str(args[0]), s.str(args[1]), args[2]); ok {
			return jsval.Int(n)
		}
		return jsval.Undefined
	})
	fn("setdata", func(s *Scope, args []jsval.Value) jsval.Value {
		if s.writer == nil {
			return jsval.Undefined
		}
		return s.writer.SetData(s.str(arg(args, 0)), arg(args, 1))
	})
	fn("modify", func(s *Scope, args []jsval.Value) jsval.Value {
		if s.writer == nil {
			return jsval.Num(0)
		}
		return s.writer.Modify(s.str(arg(args, 0)), arg(args, 1), arg(args, 2), arg(args, 3), arg(args, 4), arg(args, 5))
	})
	defv("treePath", 1, dataVisitor, func(s *Scope, args []jsval.Value) jsval.Value {
		if s.tree == nil {
			return jsval.Undefined
		}
		return s.tree.TreePath(s.str(args[0]), arg(args, 1), arg(args, 2))
	})
	defv("treeAncestors", 1, dataVisitor, func(s *Scope, args []jsval.Value) jsval.Value {
		if s.tree == nil {
			return jsval.Undefined
		}
		return s.tree.TreeAncestors(s.str(args[0]), arg(args, 1))
	})

	// ---- scales ----
	scaleFn := func(name string, f func(sp ScaleProvider, args []jsval.Value) jsval.Value, missing jsval.Value) {
		defv(name, 1, scaleVisitor, func(s *Scope, args []jsval.Value) jsval.Value {
			if s.scales == nil {
				if missing.IsArr() {
					return jsval.Arr(nil)
				}
				return missing
			}
			return f(s.scales, args)
		})
	}
	scaleFn("scale", func(sp ScaleProvider, a []jsval.Value) jsval.Value {
		return sp.Scale(a[0], arg(a, 1), arg(a, 2))
	}, jsval.Undefined)
	scaleFn("invert", func(sp ScaleProvider, a []jsval.Value) jsval.Value {
		return sp.Invert(a[0], arg(a, 1), arg(a, 2))
	}, jsval.Undefined)
	scaleFn("domain", func(sp ScaleProvider, a []jsval.Value) jsval.Value { return sp.Domain(a[0], arg(a, 1)) }, jsval.Arr(nil))
	scaleFn("range", func(sp ScaleProvider, a []jsval.Value) jsval.Value { return sp.Range(a[0], arg(a, 1)) }, jsval.Arr(nil))
	scaleFn("bandwidth", func(sp ScaleProvider, a []jsval.Value) jsval.Value { return sp.Bandwidth(a[0], arg(a, 1)) }, jsval.Num(0))
	scaleFn("copy", func(sp ScaleProvider, a []jsval.Value) jsval.Value { return sp.Copy(a[0], arg(a, 1)) }, jsval.Undefined)
	scaleFn("gradient", func(sp ScaleProvider, a []jsval.Value) jsval.Value {
		return sp.Gradient(a[0], arg(a, 1), arg(a, 2), arg(a, 3), arg(a, 4))
	}, jsval.Undefined)
	// gradient of an unknown scale reads scale.domain() of undefined.
	funcTable["gradient"].fn = wrapGradient(funcTable["gradient"].fn)
	// The internal helpers mark encoders call resolve their scale directly.
	scaleFn("_scale", func(sp ScaleProvider, a []jsval.Value) jsval.Value {
		return sp.Scale(a[0], arg(a, 1), jsval.Undefined)
	}, jsval.Undefined)
	scaleFn("_range", func(sp ScaleProvider, a []jsval.Value) jsval.Value { return sp.Range(a[0], jsval.Undefined) }, jsval.Arr(nil))
	scaleFn("_bandwidth", func(sp ScaleProvider, a []jsval.Value) jsval.Value { return sp.Bandwidth(a[0], jsval.Undefined) }, jsval.Num(0))
	// bandspace is pure: vega-scale's bandSpace.
	fn("bandspace", func(s *Scope, args []jsval.Value) jsval.Value {
		count, inner, outer := s.orZero(arg(args, 0)), s.orZero(arg(args, 1)), s.orZero(arg(args, 2))
		space := count - inner + outer*2
		switch {
		case count == 0:
			return jsval.Num(0)
		case space > 0:
			return jsval.Num(space)
		}
		return jsval.Num(1)
	})

	// ---- geo ----
	geoFn := func(name string, f func(g GeoProvider, args []jsval.Value) jsval.Value) {
		defv(name, 0, scaleVisitor, func(s *Scope, args []jsval.Value) jsval.Value {
			if s.geo == nil {
				return jsval.Undefined
			}
			return f(s.geo, args)
		})
	}
	geoFn("geoArea", func(g GeoProvider, a []jsval.Value) jsval.Value { return g.GeoArea(arg(a, 0), arg(a, 1), arg(a, 2)) })
	geoFn("geoBounds", func(g GeoProvider, a []jsval.Value) jsval.Value { return g.GeoBounds(arg(a, 0), arg(a, 1), arg(a, 2)) })
	geoFn("geoCentroid", func(g GeoProvider, a []jsval.Value) jsval.Value {
		return g.GeoCentroid(arg(a, 0), arg(a, 1), arg(a, 2))
	})
	geoFn("geoShape", func(g GeoProvider, a []jsval.Value) jsval.Value { return g.GeoShape(arg(a, 0), arg(a, 1), arg(a, 2)) })
	geoFn("geoScale", func(g GeoProvider, a []jsval.Value) jsval.Value { return g.GeoScale(arg(a, 0), arg(a, 1)) })
	geoFn("geoTranslate", func(g GeoProvider, a []jsval.Value) jsval.Value { return g.GeoTranslate(arg(a, 0), arg(a, 1)) })
	fn("pathShape", func(s *Scope, args []jsval.Value) jsval.Value {
		if s.geo == nil {
			return jsval.Undefined
		}
		return s.geo.PathShape(arg(args, 0))
	})

	// ---- view ----
	fn("containerSize", func(s *Scope, args []jsval.Value) jsval.Value {
		if s.view == nil {
			return emptyPair()
		}
		return s.view.ContainerSize()
	})
	fn("windowSize", func(s *Scope, args []jsval.Value) jsval.Value {
		if s.view == nil {
			return emptyPair()
		}
		return s.view.WindowSize()
	})
	fn("screen", func(s *Scope, args []jsval.Value) jsval.Value {
		if s.view == nil {
			return jsval.Obj(nil)
		}
		return s.view.Screen()
	})
	fn("encode", func(s *Scope, args []jsval.Value) jsval.Value {
		item, retval := arg(args, 0), arg(args, 2)
		if item.IsTruthy() {
			if s.view != nil {
				return s.view.Encode(item, arg(args, 1), retval)
			}
			// `item.mark.source` of an item that has no mark.
			s.getProp(s.getProp(item, "mark"), "source")
		}
		if !retval.IsUndefined() {
			return retval
		}
		return item
	})
	fn("inScope", func(s *Scope, args []jsval.Value) jsval.Value {
		if s.view == nil {
			return jsval.False
		}
		return jsval.Bool(s.view.InScope(arg(args, 0)))
	})
	fn("intersect", func(s *Scope, args []jsval.Value) jsval.Value {
		if !arg(args, 0).IsTruthy() || s.view == nil {
			return jsval.Arr(nil)
		}
		return s.view.Intersect(arg(args, 0), arg(args, 1), arg(args, 2))
	})
	fn("intersectLasso", func(s *Scope, args []jsval.Value) jsval.Value {
		if s.view == nil {
			return jsval.Arr(nil)
		}
		return s.view.IntersectLasso(arg(args, 0), arg(args, 1), arg(args, 2))
	})

	// ---- logging ----
	fn("warn", func(s *Scope, args []jsval.Value) jsval.Value { return s.log(LogWarn, args) })
	fn("info", func(s *Scope, args []jsval.Value) jsval.Value { return s.log(LogInfo, args) })
	fn("debug", func(s *Scope, args []jsval.Value) jsval.Value { return s.log(LogDebug, args) })

	// ---- touch and lasso geometry ----
	fn("pinchDistance", func(s *Scope, args []jsval.Value) jsval.Value {
		t0, t1 := s.touches(arg(args, 0))
		dx := s.num(s.getProp(t0, "clientX")) - s.num(s.getProp(t1, "clientX"))
		dy := s.num(s.getProp(t0, "clientY")) - s.num(s.getProp(t1, "clientY"))
		return jsval.Num(jsHypot([]float64{dx, dy}))
	})
	fn("pinchAngle", func(s *Scope, args []jsval.Value) jsval.Value {
		t0, t1 := s.touches(arg(args, 0))
		dy := s.num(s.getProp(t0, "clientY")) - s.num(s.getProp(t1, "clientY"))
		dx := s.num(s.getProp(t0, "clientX")) - s.num(s.getProp(t1, "clientX"))
		return jsval.Num(jsmath.Atan2(dy, dx))
	})
	fn("lassoAppend", func(s *Scope, args []jsval.Value) jsval.Value {
		lasso := toArray(arg(args, 0))
		xv, yv := arg(args, 1), arg(args, 2)
		x, y := s.num(xv), s.num(yv)
		minDist := 5.0
		if d := arg(args, 3); !d.IsUndefined() {
			minDist = s.num(d)
		}
		if n := len(lasso); n > 0 {
			last := lasso[n-1]
			if !last.IsUndefined() {
				lx, ly := s.num(s.getIndex(last, jsval.Num(0))), s.num(s.getIndex(last, jsval.Num(1)))
				if !(jsHypot([]float64{lx - x, ly - y}) > minDist) {
					return jsval.Arr(lasso)
				}
			}
		}
		out := make([]jsval.Value, 0, len(lasso)+1)
		out = append(out, lasso...)
		out = append(out, jsval.ArrOf(xv, yv)) // the point keeps the arguments as given
		return jsval.Arr(out)
	})
	fn("lassoPath", func(s *Scope, args []jsval.Value) jsval.Value {
		lasso := toArray(arg(args, 0))
		var out []byte
		for i, pt := range lasso {
			// `[x, y]` destructuring needs an iterable.
			if !pt.IsArr() && !pt.IsStr() {
				typeError("%s is not iterable", pt.Kind())
			}
			x, y := s.getIndex(pt, jsval.Num(0)), s.getIndex(pt, jsval.Num(1))
			switch {
			case i == 0:
				out = append(out, "M "...)
				out = append(out, s.str(x)...)
				out = append(out, ',')
				out = append(out, s.str(y)...)
				out = append(out, ' ')
			case i == len(lasso)-1:
				out = append(out, " Z"...)
			default:
				out = append(out, "L "...)
				out = append(out, s.str(x)...)
				out = append(out, ',')
				out = append(out, s.str(y)...)
				out = append(out, ' ')
			}
		}
		return jsval.Str(string(out))
	})

	// ---- pan and zoom ----
	// linear transforms coerce with `toNumber(x) ?? 0`, so a missing domain
	// end reads as 0 rather than NaN.
	type transform struct {
		lift, ground func(float64) float64
		linear       bool
	}
	linear := transform{
		lift:   func(x float64) float64 { return x },
		ground: func(x float64) float64 { return x },
		linear: true,
	}
	in := func(s *Scope, t transform, v jsval.Value) float64 {
		if t.linear && v.IsUndefined() {
			return 0
		}
		return s.num(v)
	}
	logT := func(sign float64) transform {
		return transform{
			lift:   func(x float64) float64 { return jsmath.Log(sign * x) },
			ground: func(x float64) float64 { return sign * jsmath.Exp(x) },
		}
	}
	powT := func(exp float64) transform {
		f := func(e float64) func(float64) float64 {
			return func(x float64) float64 {
				if x < 0 {
					return -jsmath.Pow(-x, e)
				}
				return jsmath.Pow(x, e)
			}
		}
		return transform{lift: f(exp), ground: f(1 / exp)}
	}
	symlogT := func(c float64) transform {
		return transform{
			lift: func(x float64) float64 { return jsSign(x) * jsmath.Log1p(math.Abs(x/c)) },
			ground: func(x float64) float64 {
				return float64(jsSign(x)*jsmath.Expm1(math.Abs(x))) * c
			},
		}
	}
	pan := func(name string, mk func(s *Scope, domain jsval.Value, args []jsval.Value) transform) {
		fn(name, func(s *Scope, args []jsval.Value) jsval.Value {
			dom := s.domainItems(arg(args, 0))
			t := mk(s, dom, args)
			d0, d1 := t.lift(in(s, t, dom.Index(0))), t.lift(in(s, t, dom.Index(dom.Len()-1)))
			dd := float64((d1 - d0) * s.num(arg(args, 1)))
			return jsval.ArrOf(jsval.Num(t.ground(d0-dd)), jsval.Num(t.ground(d1-dd)))
		})
	}
	zoom := func(name string, mk func(s *Scope, domain jsval.Value, args []jsval.Value) transform) {
		fn(name, func(s *Scope, args []jsval.Value) jsval.Value {
			dom := s.domainItems(arg(args, 0))
			t := mk(s, dom, args)
			d0, d1 := t.lift(in(s, t, dom.Index(0))), t.lift(in(s, t, dom.Index(dom.Len()-1)))
			var da float64
			if anchor := arg(args, 1); !anchor.IsNullish() {
				da = t.lift(in(s, t, anchor))
			} else {
				da = (d0 + d1) / 2
			}
			scale := s.num(arg(args, 2))
			return jsval.ArrOf(
				jsval.Num(t.ground(da+float64((d0-da)*scale))),
				jsval.Num(t.ground(da+float64((d1-da)*scale))))
		})
	}
	pan("panLinear", func(*Scope, jsval.Value, []jsval.Value) transform { return linear })
	zoom("zoomLinear", func(*Scope, jsval.Value, []jsval.Value) transform { return linear })
	signOf := func(s *Scope, d jsval.Value) float64 { return jsSign(s.num(d.Index(0))) }
	pan("panLog", func(s *Scope, d jsval.Value, _ []jsval.Value) transform { return logT(signOf(s, d)) })
	zoom("zoomLog", func(s *Scope, d jsval.Value, _ []jsval.Value) transform { return logT(signOf(s, d)) })
	pan("panPow", func(s *Scope, _ jsval.Value, a []jsval.Value) transform { return powT(s.num(arg(a, 2))) })
	zoom("zoomPow", func(s *Scope, _ jsval.Value, a []jsval.Value) transform { return powT(s.num(arg(a, 3))) })
	pan("panSymlog", func(s *Scope, _ jsval.Value, a []jsval.Value) transform { return symlogT(s.num(arg(a, 2))) })
	zoom("zoomSymlog", func(s *Scope, _ jsval.Value, a []jsval.Value) transform { return symlogT(s.num(arg(a, 3))) })
}

// domainItems checks pan/zoom's precondition that the domain is a non-empty
// array ("Domain array must not be empty").
func (s *Scope) domainItems(d jsval.Value) jsval.Value {
	if !d.IsArr() || d.Len() == 0 {
		throw("Error", "Domain array must not be empty")
	}
	return d
}

func jsSign(x float64) float64 {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	}
	return x // 0, -0 or NaN
}

// touches reads event.touches[0] and event.touches[1].
func (s *Scope) touches(event jsval.Value) (t0, t1 jsval.Value) {
	t := s.getProp(event, "touches")
	return s.getIndex(t, jsval.Num(0)), s.getIndex(t, jsval.Num(1))
}

// toArray is vega-util's array(): null and undefined become [], an array is
// itself, and anything else is wrapped.
func toArray(v jsval.Value) []jsval.Value {
	switch {
	case v.IsNullish():
		return nil
	case v.IsArr():
		return v.Items()
	}
	return []jsval.Value{v}
}

// wrapGradient makes gradient() of a scale the runtime does not know fail the
// way upstream does.
func wrapGradient(f builtinFn) builtinFn {
	return func(s *Scope, args []jsval.Value) jsval.Value {
		r := f(s, args)
		if r.IsUndefined() {
			typeError("Cannot read properties of undefined (reading 'domain')")
		}
		return r
	}
}
