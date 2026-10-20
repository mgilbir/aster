package jsval

// IsObjectLike reports whether the value is a JavaScript object that can be a
// tuple: an object or an array (which ingest keeps as the tuple itself, and
// transforms write their fields into, Fields).
func (v Value) IsObjectLike() bool { k := v.Kind(); return k == KindObj || k == KindArr }
