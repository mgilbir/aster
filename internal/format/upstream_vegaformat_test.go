package format

import (
	"strings"
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/upstream"
)

// localeFor builds the vega-format locale a recorded call was made on.
func localeFor(fn string, def []any, local Zone) (*Locale, error) {
	arg := func(i int) jsval.Value { return upstream.ToValue(argAtCall(def, i)) }
	switch fn {
	case "numberFormatLocale", "numberFormatDefaultLocale":
		return NewLocale(arg(0), jsval.Undefined, local)
	case "timeFormatLocale", "timeFormatDefaultLocale":
		return NewLocale(jsval.Undefined, arg(0), local)
	case "resetDefaultLocale":
		return NewLocale(jsval.Undefined, jsval.Undefined, local)
	}
	return NewLocale(arg(0), arg(1), local)
}

// vegaLocaleMethod asks a locale one of its function-making methods and, when haveX, calls the
// function it made on x. ok is false when the method is not one the adapter maps.
func vegaLocaleMethod(l *Locale, method string, args []any, x any, haveX bool) (got any, threw, ok bool) {
	if method == "timeFormat" || method == "utcFormat" {
		// a string is an ordinary format, anything else a time multi-format object (or the default one)
		f, err := l.TimeFormatSpec(upstream.ToValue(argAtCall(args, 0)), method == "utcFormat")
		if err != nil {
			return nil, true, true
		}
		if !haveX {
			return "function", false, true
		}
		return f(upstream.Number(x)), false, true
	}
	spec, isString := argAtCall(args, 0).(string)
	switch method {
	case "format", "formatFloat", "formatPrefix", "timeParse", "utcParse":
		if !isString {
			return nil, false, false
		}
	case "formatSpan":
		if _, isString = argAtCall(args, 3).(string); !isString && !nullishArg(argAtCall(args, 3)) {
			return nil, false, false
		}
	default:
		return nil, false, false
	}
	made := func() (any, bool, bool) { return "function", false, true }
	switch method {
	case "format":
		f, err := l.NumberFormat(spec)
		if err != nil {
			return nil, true, true
		}
		if !haveX {
			return made()
		}
		return f.FormatValue(upstream.ToValue(x)), false, true
	case "formatFloat":
		f, err := l.FormatFloat(spec)
		if err != nil {
			return nil, true, true
		}
		if !haveX {
			return made()
		}
		return f(upstream.Number(x)), false, true
	case "formatPrefix":
		f, err := l.Number.FormatPrefix(spec, upstream.Number(argAtCall(args, 1)))
		if err != nil {
			return nil, true, true
		}
		if !haveX {
			return made()
		}
		return f(upstream.Number(x)), false, true
	case "formatSpan":
		specifier := ",f"
		if isString {
			specifier = argAtCall(args, 3).(string)
		}
		f, err := l.FormatSpan(upstream.Number(argAtCall(args, 0)), upstream.Number(argAtCall(args, 1)), upstream.Number(argAtCall(args, 2)), specifier)
		if err != nil {
			return nil, true, true
		}
		if !haveX {
			return made()
		}
		return f(upstream.Number(x)), false, true
	case "timeParse", "utcParse":
		p := l.TimeParse(spec)
		if method == "utcParse" {
			p = l.UTCParse(spec)
		}
		if !haveX {
			return made()
		}
		s, isStr := x.(string)
		if !isStr {
			return nil, false, false
		}
		tm, parsed := p.Parse(s)
		return parsedDate(tm, parsed), false, true
	}
	return nil, false, false
}

func TestUpstreamVegaFormat(t *testing.T) {
	r := upstream.Start(t, "vega-format")
	local := zoneOf(t, r)
	for i := range r.File.Calls {
		c := &r.File.Calls[i]
		if c.Oversized != nil {
			r.Skip("oversized")
			continue
		}
		base, isCall := strings.CutSuffix(c.Fn, "()")
		if !isCall {
			r.Skip("constructions (a locale object is the answer)")
			continue
		}
		l, err := localeFor(base, c.ConstructedWith, local)
		if err != nil {
			r.Check(c, nil, true)
			continue
		}
		via := c.ViaSteps()
		switch {
		case len(via) == 0 && c.Method != "":
			got, threw, ok := vegaLocaleMethod(l, c.Method, c.Args, nil, false)
			if !ok {
				r.Skip("unmapped method " + c.Method)
				continue
			}
			if threw {
				r.Check(c, nil, true)
				continue
			}
			r.Check(c, fnResult(c, got != nil), false)
		case len(via) == 1 && c.Method == "" && len(c.Args) == 1:
			got, threw, ok := vegaLocaleMethod(l, via[0].Method, via[0].Args, c.Args[0], true)
			if !ok {
				r.Skip("unmapped step " + via[0].Method)
				continue
			}
			if threw {
				r.Check(c, nil, true)
				continue
			}
			r.Check(c, got, false)
		default:
			r.Skip("locale methods of another shape")
		}
	}
	r.Done(120)
}
