package release

import (
	"strings"
	"testing"
)

func TestShapeOfDistinguishesTheThreeInstallShapes(t *testing.T) {
	cases := []struct {
		name             string
		ldflags, mainVer string
		fromVCS          bool
		want             Shape
	}{
		{"goreleaser stamps the version", "0.4.0", "", false, ShapeRelease},
		{"go install records a module version", "", "v0.4.0", false, ShapeGoInstall},
		{"a source build records devel", "", "(devel)", false, ShapeDev},
		{"nothing known at all", "", "", false, ShapeDev},

		// Go stamps a local build from a tagged checkout with a real-looking
		// module version, so the VCS mark is the only thing separating it from
		// a go install build.
		{"a local build of a tagged checkout", "", "v0.4.0+dirty", true, ShapeDev},
		{"a local build between tags", "", "v0.4.1-0.20260827221005-d7f76ab9c89c", true, ShapeDev},
	}
	for _, c := range cases {
		if got := ShapeOf(c.ldflags, c.mainVer, c.fromVCS); got != c.want {
			t.Errorf("%s: ShapeOf(%q, %q, %v) = %v, want %v",
				c.name, c.ldflags, c.mainVer, c.fromVCS, got, c.want)
		}
	}
}

func TestAdviceForAReleaseBuildNamesTheInstallScript(t *testing.T) {
	got := Advice(ShapeRelease, "v0.4.1")
	if !strings.Contains(got, "install.sh") {
		t.Errorf("release advice does not mention install.sh:\n%s", got)
	}
	if strings.Contains(got, "go install") {
		t.Errorf("release advice tells the user to use Go, which they may not have:\n%s", got)
	}
}

func TestAdviceForAGoInstallBuildNamesGoInstall(t *testing.T) {
	got := Advice(ShapeGoInstall, "v0.4.1")
	if !strings.Contains(got, "go install github.com/spiffcs/hoard/cmd/hoard@") {
		t.Errorf("go install advice does not name the module path:\n%s", got)
	}
	if strings.Contains(got, "install.sh") {
		t.Errorf("go install advice sends the user to install.sh, "+
			"which would leave two hoards on PATH:\n%s", got)
	}
}

func TestADevBuildIsNeverToldItIsOutOfDate(t *testing.T) {
	if got := Advice(ShapeDev, "v0.4.1"); got != "" {
		t.Errorf("Advice(ShapeDev) = %q, want empty: a dev build is ahead of the release", got)
	}
}

func TestReleaseURLPointsAtTheTag(t *testing.T) {
	useBase(t, "https://github.com/spiffcs/hoard")

	want := "https://github.com/spiffcs/hoard/releases/tag/v0.4.1"
	if got := ReleaseURL("v0.4.1"); got != want {
		t.Errorf("ReleaseURL = %q, want %q", got, want)
	}
}
