package command

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/release"
)

func stubRelease(t *testing.T, latest string, shape release.Shape, current string) {
	t.Helper()
	pl, ps, pc := latestRelease, installShape, currentBuild
	latestRelease = func(context.Context, time.Duration) (string, error) { return latest, nil }
	installShape = func() release.Shape { return shape }
	currentBuild = func() string { return current }
	t.Cleanup(func() { latestRelease, installShape, currentBuild = pl, ps, pc })
}

func runUpdateCmd(t *testing.T) string {
	t.Helper()
	out, err := execCmd(context.Background(), nil, []string{"update"}, false)
	if err != nil {
		t.Fatalf("hoard update: %v", err)
	}
	return out
}

func TestUpdateTellsAReleaseBuildToUseTheInstallScript(t *testing.T) {
	stubRelease(t, "v0.4.1", release.ShapeRelease, "0.4.0")

	out := runUpdateCmd(t)
	if !strings.Contains(out, "install.sh") {
		t.Errorf("a release build was not shown the install script:\n%s", out)
	}
	if !strings.Contains(out, "v0.4.1") {
		t.Errorf("output does not name the new version:\n%s", out)
	}
	if strings.Contains(out, "go install") {
		t.Errorf("a release build was told to use Go, which it may not have:\n%s", out)
	}
}

func TestUpdateTellsAGoInstallBuildToUseGoInstall(t *testing.T) {
	stubRelease(t, "v0.4.1", release.ShapeGoInstall, "v0.4.0")

	out := runUpdateCmd(t)
	if !strings.Contains(out, "go install github.com/spiffcs/hoard/cmd/hoard@") {
		t.Errorf("a go install build was not shown the go install line:\n%s", out)
	}
	if strings.Contains(out, "install.sh") {
		t.Errorf("a go install build was sent to install.sh, "+
			"which would leave two hoards on PATH:\n%s", out)
	}
}

func TestUpdateLinksTheReleaseNotes(t *testing.T) {
	stubRelease(t, "v0.4.1", release.ShapeRelease, "0.4.0")

	out := runUpdateCmd(t)
	if !strings.Contains(out, "/releases/tag/v0.4.1") {
		t.Errorf("output does not link the release notes:\n%s", out)
	}
}

func TestUpdateOnACurrentBuildSaysSoAndSucceeds(t *testing.T) {
	stubRelease(t, "v0.4.0", release.ShapeRelease, "0.4.0")

	out := runUpdateCmd(t)
	if !strings.Contains(strings.ToLower(out), "up to date") {
		t.Errorf("a current build was not told it is up to date:\n%s", out)
	}
	if strings.Contains(out, "install.sh") {
		t.Errorf("a current build was told to reinstall:\n%s", out)
	}
}

func TestUpdateOnADevBuildSaysSoAndSucceeds(t *testing.T) {
	stubRelease(t, "v0.4.1", release.ShapeDev, "dev-7d48d2bc4a69")

	out := runUpdateCmd(t)
	if strings.Contains(out, "install.sh") || strings.Contains(out, "go install") {
		t.Errorf("a dev build was told to upgrade:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "dev") {
		t.Errorf("a dev build was not told what it is:\n%s", out)
	}
}

// hoard notifies; it never touches the binary or anything beside it.
func TestUpdateWritesNothingToTheInstallDirectory(t *testing.T) {
	stubRelease(t, "v0.4.1", release.ShapeRelease, "0.4.0")

	dir := t.TempDir()
	bin := filepath.Join(dir, "hoard")
	if err := os.WriteFile(bin, []byte("pretend binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := dirState(t, dir)

	runUpdateCmd(t)

	if after := dirState(t, dir); after != before {
		t.Errorf("hoard update changed the install directory\nbefore: %s\nafter:  %s", before, after)
	}
}

func dirState(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		out = append(out, e.Name()+":"+info.Mode().String()+":"+string(b))
	}
	sort.Strings(out)
	return strings.Join(out, "|")
}
