package aster_test

import "testing"

// The rules that pick the documented platform classes out of a difference's
// sites (vgknown_oracle_test.go) take only what the class looks like: label
// placement of text items, and single numbers one double apart. Any other
// site keeps a difference a failure.
func TestKnownPlatformSiteRules(t *testing.T) {
	const label = "/#document[0]/svg.marks[0]/g[0]/g.mark-group.role-frame.root[0]/g[1]/g[4]/g.mark-text.role-mark[616]/text"
	sliver := []string{
		label + `: attribute text-anchor="end", want "start"`,
		label + `: attribute transform="translate(476.5,166.04347826086948)", want "translate(483.5,166.04347826086948)"`,
		label + `: attribute opacity="1", want "0"`,
		label + `: missing attribute opacity="0"`,
	}
	if !labelPlacementOnly(sliver) {
		t.Error("label placement sites not recognised")
	}
	for _, other := range []string{
		label + `: attribute font-size="8px", want "9px"`,
		label + `: text "a", want "b"`,
		"/#document[0]/svg.marks[0]/g[0]/g.mark-symbol.role-mark[3]/path: attribute transform=\"translate(1,2)\", want \"translate(1,3)\"",
		"/#document[0]/svg.marks[0]/g[0]/g.mark-group.role-frame.root[0]: 3 children, want 4",
	} {
		if labelPlacementOnly(append(append([]string{}, sliver...), other)) {
			t.Errorf("label placement taken with %q", other)
		}
	}

	const rule = "/#document[0]/svg.marks[0]/g[0]/g.mark-rule.role-mark[0]/line"
	if !lastPlaceOnly([]string{rule + `: attribute stroke-width="7.386311749264596e-9", want "7.386311749264595e-9"`}) {
		t.Error("adjacent doubles not recognised")
	}
	for _, other := range []string{
		rule + `: attribute stroke-width="7.386311749264597e-9", want "7.386311749264595e-9"`, // two apart
		rule + `: attribute stroke-width="1", want "1"`,
		rule + `: attribute d="M0,1L2,3", want "M0,1L2,4"`,
		rule + `: attribute stroke="red", want "blue"`,
		rule + `: missing attribute stroke-width="1"`,
		label + `: attribute text-anchor="end", want "start"`,
	} {
		if lastPlaceOnly([]string{other}) {
			t.Errorf("last place taken for %q", other)
		}
	}
}
