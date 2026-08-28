package release

import (
	"fmt"
	"runtime/debug"

	"github.com/spiffcs/hoard/internal/buildinfo"
)

// Shape is how this build got here, which decides what upgrading looks like.
type Shape int

const (
	ShapeDev Shape = iota
	ShapeGoInstall
	ShapeRelease
)

const (
	modulePath       = "github.com/spiffcs/hoard/cmd/hoard"
	installScriptURL = "https://tools.aithirne.com/hoard/install.sh"
)

// Status is the running build set against the newest published release.
type Status struct {
	Current string
	Latest  string
	Shape   Shape
}

// Available reports whether there is an upgrade worth telling the user about.
func (s Status) Available() bool {
	return s.Shape != ShapeDev && Newer(s.Current, s.Latest)
}

// ShapeOf takes what it needs rather than reading it, so the mapping is
// testable without a build to inspect. A goreleaser build carries a version
// stamped by ldflags. Everything else turns on fromVCS: Go stamps a build made
// in a checkout with a real-looking module version, so only the VCS mark
// separates a local build from one `go install` fetched from the proxy.
func ShapeOf(ldflagsVersion, mainVersion string, fromVCS bool) Shape {
	switch {
	case ldflagsVersion != "":
		return ShapeRelease
	case fromVCS:
		return ShapeDev
	case mainVersion != "" && mainVersion != "(devel)":
		return ShapeGoInstall
	default:
		return ShapeDev
	}
}

func CurrentShape() Shape {
	var main string
	var fromVCS bool
	if bi, ok := debug.ReadBuildInfo(); ok {
		main = bi.Main.Version
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				fromVCS = true
				break
			}
		}
	}
	return ShapeOf(buildinfo.Version, main, fromVCS)
}

// Advice is the command that upgrades this particular build. A release build is
// never sent to `go install`, because it may have no Go toolchain; a `go
// install` build is never sent to install.sh, because that would leave a second
// hoard on PATH. A dev build gets nothing: it is ahead of the release, not
// behind it.
func Advice(s Shape, tag string) string {
	switch s {
	case ShapeRelease:
		return fmt.Sprintf("curl -sSfL %s \\\n    | sh -s -- -b \"$HOME/.local/bin\" %s",
			installScriptURL, tag)
	case ShapeGoInstall:
		return "go install " + modulePath + "@" + tag
	default:
		return ""
	}
}
