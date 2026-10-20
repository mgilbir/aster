package raster

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"weak"

	"github.com/mgilbir/aster/internal/fonts/liberation"
	"github.com/mgilbir/aster/internal/testutil"
	"github.com/mgilbir/aster/internal/text"
)

// retainFaces is how many system faces the test cycles through: more than
// the text package keeps loaded (64).
const retainFaces = 72

// TestShaperReleasesDroppedFaces is C27 (audit 2026-10-18): the shaper keeps
// nothing of a system face the Measurer has dropped past its bound, so the
// bound holds for PNG output too. It cycles through more system faces than
// the bound, then checks that the first one is no longer reachable.
//
// System fonts are indexed once per process from the home directory's
// fonts, so the test runs itself again with HOME pointing at a directory
// holding a collection of differently named faces.
func TestShaperReleasesDroppedFaces(t *testing.T) {
	if os.Getenv("ASTER_RETAIN_CHILD") == "" {
		if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
			t.Skip("system font directories under HOME are known on linux and darwin")
		}
		home := t.TempDir()
		dir := filepath.Join(home, ".fonts")
		if runtime.GOOS == "darwin" {
			dir = filepath.Join(home, "Library", "Fonts")
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		faces := make([][]testutil.NameRecord, retainFaces)
		for i := range faces {
			faces[i] = []testutil.NameRecord{{Platform: 3, Encoding: 1, Language: 0x409, ID: 1, Value: fmt.Sprintf("Retain Test %d", i)}}
		}
		if err := os.WriteFile(filepath.Join(dir, "retain.ttc"), testutil.FontCollection(liberation.SansRegular, faces), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestShaperReleasesDroppedFaces$", "-test.v")
		cmd.Env = append(os.Environ(), "HOME="+home, "ASTER_RETAIN_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	m, err := text.New(text.WithSystemFonts(), text.WithExactAdvances())
	if err != nil {
		t.Fatal(err)
	}
	s := NewShaperFromMeasurer(m)
	first := shapeRetain(t, s, 0)
	for i := 1; i < retainFaces; i++ {
		shapeRetain(t, s, i)
	}
	for range 3 {
		runtime.GC()
	}
	if first.Value() != nil {
		t.Fatal("a system face the Measurer dropped is still reachable through the shaper: the 64-face bound does not hold for PNG")
	}
	// The shaper and its Measurer live on, as a converter's do.
	runtime.KeepAlive(s)
}

// shapeRetain shapes text in system family i and returns a weak pointer to
// the face it was drawn in.
func shapeRetain(t *testing.T, s Shaper, i int) weak.Pointer[text.Face] {
	t.Helper()
	g := s.Shape("Hi", FontRequest{Families: []string{fmt.Sprintf("Retain Test %d", i)}, Weight: 400}, 12)
	if len(g) == 0 {
		t.Fatal("nothing shaped")
	}
	f, ok := TextFace(g[0].Face)
	if !ok || f.Family != fmt.Sprintf("Retain Test %d", i) {
		t.Fatalf("family %d not drawn in its system face", i)
	}
	return weak.Make(f)
}
