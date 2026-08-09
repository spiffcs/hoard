package command

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/ui"
)

// renderHelp writes one command's help at a fixed terminal shape, through the
// real tree.
func renderHelp(w io.Writer, env ui.Env, args ...string) {
	root := rootCommand(&app{env: &cli.Env{Out: w, Err: w, OutEnv: env, ErrEnv: env}})
	root.SetOut(w)
	root.SetErr(w)
	cli.InstallHelp(root, tagline, func(io.Writer) ui.Env { return env })
	root.SetArgs(append(args, "--help"))
	_ = root.Execute()
}

// Help is generated from the command tree, so a command that exists is a
// command that appears — the hand-written table this replaced was a separate
// var that nothing checked against the dispatch switch.
//
// It responds to the terminal's width: at 60 columns every line fits, with
// names and descriptions truncated rather than run off the edge.
func TestUsageFitsANarrowTerminal(t *testing.T) {
	var b bytes.Buffer
	renderHelp(&b, ui.Env{Width: 60, Clamp: true})
	for line := range strings.SplitSeq(b.String(), "\n") {
		if n := len([]rune(line)); n > 60 {
			t.Errorf("line %d wide: %q", n, line)
		}
	}
}

// The root help now lists commands one line each rather than spelling out every
// form of every command, which ran to 44 rows. Every command must still be
// present, and the text must survive piping intact.
func TestUsagePipedListsEveryCommand(t *testing.T) {
	var b bytes.Buffer
	renderHelp(&b, ui.Env{Width: 100})
	out := b.String()

	for _, want := range []string{
		"hoard: catalog valuable MTG cards and decks in SQLite",
		"Browse the hoard",
		"Collection commands:", "Binder commands:", "Deck commands:", "Interop commands:",
		"add", "update-prices", "movers", "backfill-prices", "unpriced", "guessed",
		"repair-finishes", "vacuum", "market", "report", "watch", "catalog", "artindex",
		"binder", "deck", "export", "import", "version",
		"completion", // cobra's, and worth advertising
	} {
		if !strings.Contains(out, want) {
			t.Errorf("root help lost %q", want)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Error("piped usage carries escapes")
	}
	if strings.Contains(out, "…") {
		t.Error("piped usage truncated something")
	}
}

// The forms the old root table carried now live in each command's own help,
// which is the trade the two-level layout makes. If they were not reachable
// there, the information would simply be gone.
func TestPerCommandHelpCarriesTheForms(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"add"}, []string{
			"hoard add <scryfall-url> [--foil] [--qty N]",
			"hoard add --file LIST | - [--binder B] [--again]",
		}},
		{[]string{"deck"}, []string{
			"hoard deck add --file <path> [--name NAME] [--source S]",
			"hoard deck repin <name> <set>",
		}},
		{[]string{"import"}, []string{
			"hoard import FILE [--binder B | --preserve-binders]",
		}},
		{[]string{"export"}, []string{
			"[--format csv|json|moxfield|archidekt]",
		}},
		// A ported command shows its real flags, which the legacy ones cannot.
		{[]string{"movers"}, []string{"--since", "--limit", "how far back to compare"}},
		// A group lists its subcommands.
		{[]string{"binder"}, []string{"rename", "Rename a binder", "rm", "Remove an empty binder"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var b bytes.Buffer
			renderHelp(&b, ui.Env{Width: 100}, tc.args...)
			for _, want := range tc.want {
				if !strings.Contains(b.String(), want) {
					t.Errorf("help for %q missing %q; got:\n%s", tc.args, want, b.String())
				}
			}
		})
	}
}
