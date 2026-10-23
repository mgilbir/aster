package aster_test

import (
	"math"
	"regexp"
	"runtime"
	"strconv"

	"github.com/mgilbir/aster"
	"github.com/mgilbir/aster/internal/jsval"
	"github.com/mgilbir/aster/internal/oracle"
	"github.com/mgilbir/aster/internal/svgdiff"
)

// The two classes of difference ADR 0013 documents as the platform's and not
// the engine's, which TestVegaTypeConfusionMatchesNode counts and logs
// instead of failing on, each only where the mutant shows it is that class:
//
//   - Label placement on a sliver. The label transform decides on a bitmap
//     of the marks it avoids, set wherever node-canvas's cairo puts any alpha;
//     cairo's antialiasing of a thin line sets pixels the engine's
//     rasterizer does not, and the other way round, which moves a label now
//     and then. A difference is this class when every site of it is the
//     placement of a text item (its text-anchor, transform or opacity), and
//     the mutant agrees with node once its label transforms avoid nothing:
//     the bitmap of the avoided marks made the difference, and nothing else
//     did.
//   - macOS's Math.pow. node on macOS takes pow from Apple's libm, the engine
//     glibc's (internal/jsmath); they disagree in the last place for about
//     0.2% of arguments. On darwin only, a difference is this class when
//     every site of it is an attribute holding one number, adjacent
//     doubles on the two sides. Elsewhere glibc is node's pow too, and such
//     a difference fails.
//
// Anything else, or a difference with more sites than the comparison lists,
// fails the test as before.

const (
	knownLabelSliver = "label placement over avoided marks (ADR 0013)"
	knownMacPow      = "a number one unit in the last place apart (macOS Math.pow, ADR 0013)"
)

// knownPlatformDifference returns the class of the difference sites, all of
// them, between the engine's SVG of mutant and node's, when it is one of the
// documented platform classes and the mutant confirms it; "" otherwise.
func knownPlatformDifference(o *oracle.Oracle, c *aster.Converter, mutant []byte, sites []string, all bool) string {
	if !all || len(sites) == 0 {
		return ""
	}
	if labelPlacementOnly(sites) && agreesWithoutAvoidMarks(o, c, mutant) {
		return knownLabelSliver
	}
	if runtime.GOOS == "darwin" && lastPlaceOnly(sites) {
		return knownMacPow
	}
	return ""
}

var labelSite = regexp.MustCompile(`/g\.mark-text[^/]*/text: (attribute|missing attribute|unexpected attribute) (text-anchor|transform|opacity)=`)

// labelPlacementOnly reports whether every site is where a text item is
// placed: its anchor, position or visibility.
func labelPlacementOnly(sites []string) bool {
	for _, s := range sites {
		if !labelSite.MatchString(s) {
			return false
		}
	}
	return true
}

// agreesWithoutAvoidMarks renders mutant with every label transform's
// avoidMarks removed, in the engine and in node, and reports whether the two
// SVGs are equal. A mutant with no label transform that avoids marks, or
// either side failing, does not agree.
func agreesWithoutAvoidMarks(o *oracle.Oracle, c *aster.Converter, mutant []byte) bool {
	spec, err := jsval.ParseJSON(mutant)
	if err != nil {
		return false
	}
	removed := false
	// Every object of the specification, without recursion: a mutant can
	// nest as deep as the JSON parser allows.
	stack := []jsval.Value{spec}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch {
		case v.IsArr():
			stack = append(stack, v.Items()...)
		case v.IsObj():
			obj := v.ObjValue()
			if t := obj.Lookup("type"); t.IsStr() && t.StrValue() == "label" && !obj.Lookup("avoidMarks").IsUndefined() {
				obj.Delete("avoidMarks")
				removed = true
			}
			for i := 0; i < obj.Len(); i++ {
				stack = append(stack, obj.ValueAt(i))
			}
		}
	}
	if !removed {
		return false
	}
	variant := jsval.AppendJSON(nil, spec)
	want, err := o.SVG(false, variant)
	if err != nil || want.Err != "" {
		return false
	}
	got, err := c.VegaToSVG(variant, asUpstream)
	if err != nil {
		return false
	}
	d, err := svgdiff.Compare([]byte(got), []byte(want.SVG), svgTolerance)
	return err == nil && d.Equal
}

var numberSite = regexp.MustCompile(`: attribute [\w:-]+="([^"]*)", want "([^"]*)"$`)

// lastPlaceOnly reports whether every site is an attribute whose values on
// the two sides are each one number, and adjacent doubles.
func lastPlaceOnly(sites []string) bool {
	for _, s := range sites {
		m := numberSite.FindStringSubmatch(s)
		if m == nil {
			return false
		}
		a, err1 := strconv.ParseFloat(m[1], 64)
		b, err2 := strconv.ParseFloat(m[2], 64)
		if err1 != nil || err2 != nil || a == b || math.Nextafter(a, b) != b {
			return false
		}
	}
	return true
}
