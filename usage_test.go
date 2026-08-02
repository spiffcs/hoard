package main

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/ui"
)

// The usage responds to the terminal's width: at 60 columns every line
// fits, with invocations and descriptions truncated rather than run off
// the edge.
func TestUsageFitsANarrowTerminal(t *testing.T) {
	var b strings.Builder
	printUsage(&b, ui.Env{Width: 60, Clamp: true})
	for _, line := range strings.Split(b.String(), "\n") {
		if n := len([]rune(line)); n > 60 {
			t.Errorf("line %d wide: %q", n, line)
		}
	}
}

// Piped (no clamp, no color), the full text survives: every command name,
// no escapes, no truncation ellipses.
func TestUsagePipedIsComplete(t *testing.T) {
	var b strings.Builder
	printUsage(&b, ui.Env{Width: 80})
	out := b.String()
	for _, want := range []string{
		"hoard — catalog valuable MTG cards and decks in SQLite",
		"Browse the hoard",
		"update-prices", "backfill-prices", "repair-finishes",
		"binder rename <binder> <new-name>",
		"deck add --file <path> [--name NAME] [--source S]",
		"import FILE [--binder B | --preserve-binders]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("usage lost %q", want)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Error("piped usage carries escapes")
	}
	if strings.Contains(out, "…") {
		t.Error("piped usage truncated something")
	}
}
