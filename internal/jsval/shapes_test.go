package jsval

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

// dumpShape writes v with everything about its objects that the parser
// decides: key order, values, how many leading keys are array indexes, and
// whether the object has an index.
func dumpShape(b *strings.Builder, v Value) {
	switch v.Kind() {
	case KindObj:
		o := v.ObjValue()
		fmt.Fprintf(b, "{n%d i%t ", o.nidx, o.index != nil)
		for i, k := range o.keys {
			fmt.Fprintf(b, "%q:", k)
			dumpShape(b, o.vals[i])
			b.WriteByte(',')
		}
		b.WriteByte('}')
	case KindArr:
		b.WriteByte('[')
		for _, e := range v.Items() {
			dumpShape(b, e)
			b.WriteByte(',')
		}
		b.WriteByte(']')
	case KindNum:
		fmt.Fprintf(b, "%x", math.Float64bits(v.NumValue()))
	default:
		fmt.Fprintf(b, "%d:%q", v.Kind(), v.String())
	}
}

func parseShapes(text []byte, on bool) (string, error) {
	defer func(v bool) { useShapes = v }(useShapes)
	useShapes = on
	v, err := ParseJSON(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	dumpShape(&b, v)
	return b.String(), nil
}

// TestShapesMatchGeneral: objects built from the keys of the object before
// them (parser.shapes) are exactly what the general path builds, on rows
// that repeat, extend, shorten, reorder and respell (escapes, other UTF-8)
// each other's keys, repeat a key, use array-index keys, nest, and pass the
// 32 keys of an index and the 64 of a shape; and broken text fails the same.
func TestShapesMatchGeneral(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	// u escapes a code unit as JSON text does (\uXXXX), for keys spelled
	// two ways.
	u := func(hex string) string { return string(rune(0x5c)) + "u" + hex }
	pool := []string{`a`, `b`, `ab`, `a` + u("0062"), u("0061") + `b`, `abc`, ``, `0`, `1`, `7`, `01`, `4294967294`,
		`x\"`, `\\`, `é`, u("00e9"), `name`, `name `, `Name`, `\n`, `日本`, `IMDB Rating`, `Release Date`, u("0030")}
	for i := 0; i < 70; i++ {
		pool = append(pool, fmt.Sprintf("k%d", i))
	}
	value := func(depth int, obj func(int) string) string {
		switch r := rng.IntN(10); {
		case r == 0 && depth < 4:
			return obj(depth + 1)
		case r == 1 && depth < 4:
			return "[" + obj(depth+1) + "," + obj(depth+1) + "]"
		case r < 5:
			return fmt.Sprint(rng.IntN(1000))
		case r < 8:
			return `"v` + fmt.Sprint(rng.IntN(5)) + `"`
		default:
			return "null"
		}
	}
	var obj func(depth int) string
	var last []string
	obj = func(depth int) string {
		var keys []string
		switch r := rng.IntN(8); {
		case r < 4 && last != nil: // the keys before, maybe one changed or one more or fewer
			keys = append(keys, last...)
			switch rng.IntN(5) {
			case 0:
				if len(keys) > 0 {
					keys[rng.IntN(len(keys))] = pool[rng.IntN(len(pool))]
				}
			case 1:
				keys = append(keys, pool[rng.IntN(len(pool))])
			case 2:
				if len(keys) > 0 {
					keys = keys[:len(keys)-1]
				}
			}
		case r == 4: // many keys, some the same
			for i := 0; i < 30+rng.IntN(45); i++ {
				keys = append(keys, pool[rng.IntN(len(pool))])
			}
		case r == 5: // many keys, all different: past an index's 32 and a shape's 64
			for i := 0; i < 28+rng.IntN(45); i++ {
				keys = append(keys, fmt.Sprintf("k%d", i))
			}
		default:
			for i := 0; i < rng.IntN(8); i++ {
				keys = append(keys, pool[rng.IntN(len(pool))])
			}
		}
		if depth == 1 {
			last = keys
		}
		var b strings.Builder
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `"%s":%s`, k, value(depth, obj))
		}
		b.WriteByte('}')
		return b.String()
	}
	for iter := 0; iter < 300; iter++ {
		var b strings.Builder
		b.WriteByte('[')
		for b.Len() < slabMinDoc+rng.IntN(slabMinDoc) {
			if b.Len() > 1 {
				b.WriteByte(',')
			}
			b.WriteString(obj(1))
		}
		b.WriteByte(']')
		doc := []byte(b.String())
		docs := [][]byte{doc}
		// Broken: cut short, a byte changed (a key's quote, an escape).
		cut := append([]byte(nil), doc[:rng.IntN(len(doc))]...)
		flip := append([]byte(nil), doc...)
		flip[rng.IntN(len(flip))] = `"\{}:,x`[rng.IntN(7)]
		docs = append(docs, cut, flip)
		for i, d := range docs {
			off, errOff := parseShapes(d, false)
			on, errOn := parseShapes(d, true)
			if fmt.Sprint(errOff) != fmt.Sprint(errOn) || off != on {
				t.Fatalf("iter %d doc %d: shapes on and off differ\nerr off %v\nerr on  %v", iter, i, errOff, errOn)
			}
			if i == 0 && errOn != nil {
				t.Fatalf("iter %d: %v", iter, errOn)
			}
		}
	}
}

// TestShapesKeysAsWritten: a key read through an escape never stands for the
// next object's: text equal to what it decodes to is another key, or not a
// key at all.
func TestShapesKeysAsWritten(t *testing.T) {
	bs := string(rune(0x5c))
	pad := strings.Repeat(" ", slabMinDoc)
	for _, pair := range [][2]string{
		{`{"` + bs + bs + `":1}`, `{"` + bs + `":1,"y":2}`}, // key \, then " read as an escape
		{`{"x` + bs + `"":1}`, `{"x"":1}`},                  // key x", then x" and a stray quote
		{`{"` + bs + `n":1}`, "{\"\n\":1}"},                 // key \n (a newline), then a raw newline
		{`{"` + bs + `u0041":1}`, `{"A":1}`},                // the same key two ways: both read
		{`{"a` + bs + `"b":1,"c":2}`, `{"a"b":1,"c":2}`},    // key a"b, then a and garbage
	} {
		doc := []byte("[" + pair[0] + "," + pair[1] + "," + pair[1] + "]" + pad)
		off, errOff := parseShapes(doc, false)
		on, errOn := parseShapes(doc, true)
		if fmt.Sprint(errOff) != fmt.Sprint(errOn) || off != on {
			t.Errorf("%s then %s: shapes on and off differ\noff %v %s\non  %v %s", pair[0], pair[1], errOff, off, errOn, on)
		}
	}
}

// TestShapesUsed: the rows of a data file are built from the shape (else the
// test above compares the general path with itself).
func TestShapesUsed(t *testing.T) {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; b.Len() < slabMinDoc; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"a":%d,"b":"x","c":null}`, i)
	}
	b.WriteByte(']')
	p := parser{s: []byte(b.String()), left: math.MaxInt64, end: b.Len(), sl: new(slabs)}
	if _, err := p.value(0); err != nil {
		t.Fatal(err)
	}
	if sh := p.shapes[2]; !sh.distinct || strings.Join(sh.keys, ",") != "a,b,c" {
		t.Fatalf("shape at depth 2 (the rows) = %q distinct %v, want a,b,c distinct", sh.keys, sh.distinct)
	}
}

// TestShapesBounded: an object of more than maxShapeKeys keys is not kept as
// a shape, so what a parse holds for shapes stays small whatever the input.
func TestShapesBounded(t *testing.T) {
	var row strings.Builder
	row.WriteByte('{')
	for i := 0; i <= maxShapeKeys; i++ {
		if i > 0 {
			row.WriteByte(',')
		}
		fmt.Fprintf(&row, `"k%d":%d`, i, i)
	}
	row.WriteByte('}')
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; b.Len() < slabMinDoc; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(row.String())
	}
	b.WriteByte(']')
	p := parser{s: []byte(b.String()), left: math.MaxInt64, end: b.Len(), sl: new(slabs)}
	if _, err := p.value(0); err != nil {
		t.Fatal(err)
	}
	for d, sh := range p.shapes {
		if len(sh.keys) > maxShapeKeys {
			t.Fatalf("depth %d keeps a shape of %d keys, over %d", d, len(sh.keys), maxShapeKeys)
		}
	}
}
