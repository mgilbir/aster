package scale

import (
	"math"

	"github.com/mgilbir/aster/internal/format"
	"github.com/mgilbir/aster/internal/jsval"
)

// d3TickFormat is d3-scale's scale.tickFormat(count, specifier) for the linear,
// pow, symlog and log families, using loc's number locale (d3 uses its default
// en-US locale). Vega itself formats labels through TickFormat (which is
// locale-aware and also handles time); these methods exist for callers that
// want d3's own behaviour, including its log-scale label thinning.
func d3TickFormat(loc *format.Locale, t *tspec, domain []float64, ticks func(TickCount) []float64, count TickCount, specifier jsval.Value) (func(float64) string, error) {
	if t.kind != kindLog {
		return d3LinearTickFormat(loc, domain, count.countOrDefault(), specifier)
	}
	n := count.countOrDefault()
	// d3 log: the default specifier is "s" for base 10 and "," otherwise, and
	// an integer base trims trailing zeros unless a precision is given.
	spec := ","
	if t.base == 10 {
		spec = "s"
	}
	if !specifier.IsNullish() {
		var err error
		if spec, err = numberSpecifier(specifier); err != nil {
			return nil, err
		}
	}
	s, err := format.ParseSpecifier(spec)
	if err != nil {
		return nil, err
	}
	if m := math.Mod(t.base, 1); (m == 0 || m != m) && !s.HasPrecision() {
		s.Trim = true
	}
	nf, err := loc.Number.FormatSpecifier(s)
	if err != nil {
		return nil, err
	}
	if math.IsInf(n, 1) {
		return nf.Format, nil
	}
	keep := t.logKeep(n, len(ticks(TickCount{})))
	return func(d float64) string {
		if keep(d) {
			return nf.Format(d)
		}
		return ""
	}, nil
}

// TickFormat is scale.tickFormat(count, specifier) with d3's semantics.
func (c *Continuous) TickFormat(loc *format.Locale, count TickCount, specifier jsval.Value) (func(float64) string, error) {
	return d3TickFormat(loc, &c.tspec, c.domain, c.Ticks, count, specifier)
}

// TickFormat is scale.tickFormat(count, specifier) with d3's semantics.
func (s *Sequential) TickFormat(loc *format.Locale, count TickCount, specifier jsval.Value) (func(float64) string, error) {
	return d3TickFormat(loc, &s.tspec, []float64{s.x0, s.x1}, s.Ticks, count, specifier)
}

// TickFormat is scale.tickFormat(count, specifier) with d3's semantics.
func (d *Diverging) TickFormat(loc *format.Locale, count TickCount, specifier jsval.Value) (func(float64) string, error) {
	return d3TickFormat(loc, &d.tspec, []float64{d.x0, d.x1, d.x2}, d.Ticks, count, specifier)
}

// TickFormat is scale.tickFormat(count, specifier) with d3's semantics.
func (q *Quantize) TickFormat(loc *format.Locale, count TickCount, specifier jsval.Value) (func(float64) string, error) {
	t := newTspec(kindLinear)
	return d3TickFormat(loc, &t, []float64{q.x0, q.x1}, q.Ticks, count, specifier)
}

// TickFormat is scale.tickFormat(count, specifier) with d3's semantics.
func (s *Identity) TickFormat(loc *format.Locale, count TickCount, specifier jsval.Value) (func(float64) string, error) {
	t := newTspec(kindLinear)
	return d3TickFormat(loc, &t, s.domain, s.Ticks, count, specifier)
}

// TickFormat is bin-ordinal's tickFormat: d3's linear tickFormat over its
// first and last domain values.
func (b *BinOrdinal) TickFormat(loc *format.Locale, count TickCount, specifier jsval.Value) (func(float64) string, error) {
	t := newTspec(kindLinear)
	return d3TickFormat(loc, &t, b.domain, func(TickCount) []float64 { return nil }, count, specifier)
}

// TickFormat is the time scale's tickFormat: with no specifier, d3's multi-scale
// format (each tick labelled with the coarsest unit it starts); otherwise the
// format for the specifier.
func (s *Time) TickFormat(loc *format.Locale, count TickCount, specifier jsval.Value) (func(float64) string, error) {
	f, err := loc.TimeFormatSpec(specifier, s.zone.IsUTC())
	return f, err
}

// d3LinearTickFormat is d3-scale's tickFormat(start, stop, count, specifier):
// it fills in the precision the tick step calls for. Unlike vega-format's
// formatSpan, an "s" specifier is always turned into a fixed-point format
// scaled by the SI prefix of the larger end point, even when a precision is
// given.
func d3LinearTickFormat(loc *format.Locale, domain []float64, count float64, specifier jsval.Value) (func(float64) string, error) {
	spec, err := spanSpecifier(specifier)
	if err != nil {
		return nil, err
	}
	s, err := format.ParseSpecifier(spec)
	if err != nil {
		return nil, err
	}
	start, stop := endpoints(domain)
	step := TickStep(start, stop, count)
	value := math.Max(math.Abs(start), math.Abs(stop))
	switch s.Type {
	case "s":
		if !s.HasPrecision() {
			if p := format.PrecisionPrefix(step, value); !math.IsNaN(p) {
				s.Precision = p
			}
		}
		reparsed, err := format.ParseSpecifier(s.String())
		if err != nil {
			reparsed = s
		}
		return loc.Number.FormatPrefixSpecifier(reparsed, value)
	case "", "e", "g", "p", "r":
		if !s.HasPrecision() {
			if p := format.PrecisionRound(step, value); !math.IsNaN(p) {
				if s.Type == "e" {
					p--
				}
				s.Precision = p
			}
		}
	case "f", "%":
		if !s.HasPrecision() {
			if p := format.PrecisionFixed(step); !math.IsNaN(p) {
				if s.Type == "%" {
					p -= 2
				}
				s.Precision = p
			}
		}
	}
	nf, err := loc.Number.FormatSpecifier(s)
	if err != nil {
		return nil, err
	}
	return nf.Format, nil
}
