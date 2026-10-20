package guides

import (
	"testing"

	"github.com/mgilbir/aster/internal/jsval"
)

// A "__proto__" key of a user encode block (JSON.parse makes it an own
// member) is `dst.__proto__ = v` in extend: v's members become inherited,
// listed after dst's own, and do not replace them. The guide renders; it
// does not fail.
func TestExtendProtoKeyIsPrototype(t *testing.T) {
	src, err := jsval.ParseJSON([]byte(`{"a": {"value": 1}, "__proto__": {"a": {"value": 9}, "b": {"value": 2}}}`))
	if err != nil {
		t.Fatal(err)
	}
	dst := jsval.NewObject(0)
	dst.Set("c", jsval.Obj(jsval.ObjectOf("value", jsval.Num(3))))
	var thrown error
	func() {
		defer catch(&thrown)
		extend(dst, src)
	}()
	if thrown != nil {
		t.Fatalf("extend threw: %v", thrown)
	}
	got := string(jsval.AppendJSON(nil, jsval.Obj(dst)))
	if want := `{"c":{"value":3},"a":{"value":1},"b":{"value":2}}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
