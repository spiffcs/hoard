package command

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/spiffcs/hoard/internal/release"
	"github.com/spiffcs/hoard/internal/ui"
)

func renderVersion(env ui.Env, st release.Status) string {
	var b bytes.Buffer
	printVersion(&b, env, st)
	return b.String()
}

func currentStatus() release.Status {
	return release.Status{Current: "0.4.0", Latest: "v0.4.0", Shape: release.ShapeRelease}
}

func outdatedStatus() release.Status {
	return release.Status{Current: "0.4.0", Latest: "v0.4.1", Shape: release.ShapeRelease}
}

// The first line is the part scripts and bug reports grep. It must stay
// "hoard <version>" and must not pick up styling that survives a pipe.
func TestVersionFirstLineStaysGreppable(t *testing.T) {
	out := renderVersion(ui.Env{Width: 80}, outdatedStatus())

	first := strings.SplitN(out, "\n", 2)[0]
	if first != "hoard 0.4.0" {
		t.Errorf("first line = %q, want %q", first, "hoard 0.4.0")
	}
}

func TestVersionIsPlainWhenTheOutputIsNotATerminal(t *testing.T) {
	out := renderVersion(ui.Env{Width: 80}, outdatedStatus())

	if strings.Contains(out, "\x1b[") {
		t.Errorf("piped output carries escape sequences:\n%q", out)
	}
}

func TestVersionEmphasisesTheBuildOnATerminal(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	out := renderVersion(ui.Env{Width: 80, Color: true}, outdatedStatus())

	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("styled output carries no escape sequences at all:\n%q", out)
	}
	line := versionLine(t, out, "hoard 0.4.0")
	if !strings.Contains(line, "\x1b[") {
		t.Errorf("the version line is unstyled on a terminal: %q", line)
	}
}

func TestVersionNamesANewerReleaseWhenThereIsOne(t *testing.T) {
	out := renderVersion(ui.Env{Width: 80}, outdatedStatus())

	line := versionLine(t, out, "update:")
	if !strings.Contains(line, "v0.4.1") {
		t.Errorf("update line does not name the new version: %q", line)
	}
	if !strings.Contains(line, "hoard update") {
		t.Errorf("update line does not say what to run: %q", line)
	}
}

func TestTheUpdateLineSitsInTheSameColumnAsTheOthers(t *testing.T) {
	out := renderVersion(ui.Env{Width: 80}, outdatedStatus())

	commit := versionLine(t, out, "commit:")
	update := versionLine(t, out, "update:")
	if valueColumn(commit) != valueColumn(update) {
		t.Errorf("update: value starts at column %d but commit: starts at %d\n%s\n%s",
			valueColumn(update), valueColumn(commit), commit, update)
	}
}

func TestACurrentBuildIsToldItIsCurrent(t *testing.T) {
	out := renderVersion(ui.Env{Width: 80}, currentStatus())

	line := versionLine(t, out, "update:")
	if !strings.Contains(strings.ToLower(line), "up to date") {
		t.Errorf("a current build's update line = %q, want it to say up to date", line)
	}
}

func TestADevBuildGetsNoUpdateLine(t *testing.T) {
	st := release.Status{Current: "dev-7d48d2bc4a69", Latest: "v0.4.1", Shape: release.ShapeDev}
	out := renderVersion(ui.Env{Width: 80}, st)

	if strings.Contains(out, "update:") {
		t.Errorf("a dev build was told it is out of date:\n%s", out)
	}
}

func TestVersionStillCarriesTheLegalNotices(t *testing.T) {
	out := renderVersion(ui.Env{Width: 80}, outdatedStatus())

	for _, want := range []string{"Fan Content", "Scryfall"} {
		if !strings.Contains(out, want) {
			t.Errorf("version output no longer mentions %q:\n%s", want, out)
		}
	}
}

type versionDoc struct {
	SchemaVersion string `json:"schemaVersion"`
	Kind          string `json:"kind"`
	Version       *struct {
		Version  string `json:"version"`
		Commit   string `json:"commit"`
		Built    string `json:"built"`
		Go       string `json:"go"`
		Platform string `json:"platform"`
		Update   *struct {
			Latest string `json:"latest"`
		} `json:"update"`
	} `json:"version"`
}

func TestVersionEmitsAVersionedDocument(t *testing.T) {
	out, err := execCmd(context.Background(), nil, []string{"version"}, true)
	if err != nil {
		t.Fatalf("hoard version --json: %v", err)
	}
	var doc versionDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON (%v): %q", err, out)
	}
	if doc.Kind != "version" {
		t.Errorf("kind = %q, want %q", doc.Kind, "version")
	}
	if doc.Version == nil {
		t.Fatalf("document carries no version payload: %s", out)
	}
	if doc.Version.Version == "" {
		t.Error("version payload has no version")
	}
	if doc.Version.Platform == "" {
		t.Error("version payload has no platform")
	}
}

func versionLine(t *testing.T, out, contains string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, contains) {
			return l
		}
	}
	t.Fatalf("no line containing %q in:\n%s", contains, out)
	return ""
}

func valueColumn(line string) int {
	i := strings.Index(line, ":")
	if i < 0 {
		return -1
	}
	rest := line[i+1:]
	return i + 1 + (len(rest) - len(strings.TrimLeft(rest, " ")))
}
