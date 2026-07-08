package runtime

import "testing"

// structuredClone must handle DataView: its constructor takes a buffer, so
// the generic typed-array branch (new v.constructor(v)) would throw.
func TestStructuredCloneDataView(t *testing.T) {
	r, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()

	got, err := r.evalModule(`
		const buf = new ArrayBuffer(8);
		const view = new DataView(buf, 2, 4);
		view.setUint8(0, 42);
		const clone = structuredClone(view);
		// Mutating the clone must not affect the original (deep copy).
		clone.setUint8(0, 7);
		export default [
			clone instanceof DataView,
			clone.byteOffset,
			clone.byteLength,
			clone.getUint8(0) === 7,
			view.getUint8(0) === 42,
		].join(",");
	`)
	if err != nil {
		t.Fatalf("evalModule: %v", err)
	}
	if got != "true,2,4,true,true" {
		t.Errorf("DataView clone mismatch: %s", got)
	}
}
