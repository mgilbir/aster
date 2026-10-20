package vega

import "github.com/mgilbir/aster/internal/jsval"

// Code extracted from vega-crossfilter 5.1.3 build/vega-crossfilter.js (Bitmaps).

// bitmapsMembers are the members of the object vega-crossfilter's Bitmaps()
// returns, in order, with the source text String() gives each function.
var bitmapsMembers = [...]struct{ name, src string }{
	{"data", "() => data"},
	{"seen", "() => seen = lengthen(seen, data.length)"},
	{"add", "add(array) {\n      for (let i = 0, j = data.length, n = array.length, t; i < n; ++i) {\n        t = array[i];\n        t._index = j++;\n        data.push(t);\n      }\n    }"},
	{"remove", "remove(num, map) {\n      // map: index -> boolean (true => remove)\n      const n = data.length,\n        copy = Array(n - num),\n        reindex = data; // reuse old data array for index map\n      let t, i, j;\n\n      // seek forward to first removal\n      for (i = 0; !map[i] && i < n; ++i) {\n        copy[i] = data[i];\n        reindex[i] = i;\n      }\n\n      // condense arrays\n      for (j = i; i < n; ++i) {\n        t = data[i];\n        if (!map[i]) {\n          reindex[i] = j;\n          curr[j] = curr[i];\n          prev[j] = prev[i];\n          copy[j] = t;\n          t._index = j++;\n        } else {\n          reindex[i] = -1;\n        }\n        curr[i] = 0; // clear unused bits\n      }\n      data = copy;\n      return reindex;\n    }"},
	{"size", "() => data.length"},
	{"curr", "() => curr"},
	{"prev", "() => prev"},
	{"reset", "k => prev[k] = curr[k]"},
	{"all", "() => width < 0x101 ? 0xff : width < 0x10001 ? 0xffff : 0xffffffff"},
	{"set", "set(k, one) {\n      curr[k] |= one;\n    }"},
	{"clear", "clear(k, one) {\n      curr[k] &= ~one;\n    }"},
	{"resize", "resize(n, m) {\n      const k = curr.length;\n      if (n > k || m > width) {\n        width = Math.max(m, width);\n        curr = array(n, width, curr);\n        prev = array(n, width);\n      }\n    }"},
}

// newBitmapsObject is the object Bitmaps() returns as an expression reads it
// through the crossfilter's signal: its functions (which an expression can
// print but not call) and, once the operator has run, its mask.
func newBitmapsObject() *jsval.Object {
	o := jsval.NewObject(len(bitmapsMembers) + 1)
	for _, m := range bitmapsMembers {
		o.Set(m.name, jsval.HostFunction(m.src, nil))
	}
	return o
}
