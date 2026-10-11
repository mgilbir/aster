package geo

import (
	"unsafe"

	"github.com/mgilbir/aster/internal/jsval"
)

// jsSum is a JavaScript running sum, `x += v` from 0, over JSON values, as
// topojson-client accumulates the deltas of a quantized arc: numbers add,
// and once a string (or an array or object, whose primitive is a string)
// comes along the sum is a string that every later value is concatenated
// to. A string sum grows with every position, so its bytes are charged to
// the topology's ledger, and its work to the meter; it is appended to in
// place, so it makes no garbage (V8 keeps such a string as a rope).
type jsSum struct {
	num   float64
	str   []byte
	isStr bool
}

func (s *jsSum) add(v jsval.Value, t *topology) error {
	if !s.isStr {
		switch v.Kind() {
		case jsval.KindStr, jsval.KindArr, jsval.KindObj:
		default:
			s.num += jsval.ToNumber(v)
			return nil
		}
	}
	tail := v.AsString()
	if !s.isStr {
		s.str, s.isStr = jsval.AppendJSNumber(nil, s.num), true
	}
	before := cap(s.str)
	if len(s.str)+len(tail) > before {
		// append's growth, charged before it happens.
		if err := t.charge(int64(2*(len(s.str)+len(tail))), 1); err != nil {
			return err
		}
		next := make([]byte, len(s.str), 2*(len(s.str)+len(tail)))
		copy(next, s.str)
		t.led.Free(int64(before))
		s.str = next
	}
	s.str = append(s.str, tail...)
	return t.m.Work(int64(len(tail))/16 + 1)
}

// number is ToNumber of the sum.
func (s *jsSum) number(t *topology) (float64, error) {
	if !s.isStr {
		return s.num, nil
	}
	if err := t.m.Work(int64(len(s.str))/16 + 1); err != nil {
		return 0, err
	}
	// A view of the bytes, which StringToNumber only reads.
	return jsval.StringToNumber(unsafe.String(unsafe.SliceData(s.str), len(s.str))), nil
}

// String is String(sum).
func (s *jsSum) String() string {
	if s.isStr {
		return string(s.str)
	}
	return jsval.JSNumberString(s.num)
}

// release gives back the bytes a string sum holds.
func (s *jsSum) release(t *topology) {
	t.led.Free(int64(cap(s.str)))
	s.str = nil
}

// jsScaleAdd is `sum * k + d`: a number, or a string when d is one (or an
// array or object).
func jsScaleAdd(sum *jsSum, k float64, d jsval.Value, t *topology) (jsval.Value, error) {
	x, err := sum.number(t)
	if err != nil {
		return jsval.Undefined, err
	}
	prod := float64(x * k)
	switch d.Kind() {
	case jsval.KindStr, jsval.KindArr, jsval.KindObj:
		str := jsval.JSNumberString(prod) + d.AsString()
		if err := t.charge(int64(len(str)), 1); err != nil {
			return jsval.Undefined, err
		}
		return jsval.Str(str), nil
	}
	return jsval.Num(prod + jsval.ToNumber(d)), nil
}
