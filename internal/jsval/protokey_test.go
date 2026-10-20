package jsval

import "testing"

// A prototype answers a key longer than any member on its chain without a
// map lookup, which would hash the whole key (pluck(strings, name) reads name
// on every item: a 256 KB name made one call hash 2.5 GB). The bound must not
// hide a member: every member of each built-in chain is still found, and the
// bound is the chain's longest member exactly. (The cost itself is held by
// TestEveryBuiltinIsMetered's calibrated limit; a ratio of times is no test,
// see internal/testutil.)
func TestProtoKeyBoundHidesNoMember(t *testing.T) {
	for name, p := range map[string]*Proto{
		"Object": ObjectProto, "Array": ArrayProto, "String": StringProto, "Number": NumberProto,
		"Boolean": BooleanProto, "RegExp": RegExpProto, "Date": DateProto, "Function": functionProto,
	} {
		longest := 0
		for q := p; q != nil; q = q.parent {
			for key, m := range q.members {
				longest = max(longest, len(key))
				if !p.Has(key) {
					t.Errorf("%s.prototype: %s is a member, Has says not", name, key)
				}
				if m.Fn != "" && p.Get(key).IsUndefined() {
					t.Errorf("%s.prototype: %s is a function, Get says undefined", name, key)
				}
			}
		}
		if p.longest != longest {
			t.Errorf("%s.prototype: bound %d, longest member %d", name, p.longest, longest)
		}
	}
}
