package command

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/ui"
)

func renderHelp(w io.Writer, env ui.Env, args ...string) {
	root, _ := buildRoot(
		&app{env: &cli.Env{Out: w, Err: w, OutEnv: env, ErrEnv: env}},
		func(io.Writer) ui.Env { return env },
	)
	root.SetArgs(append(args, "--help"))
	_ = root.Execute()
}

func helpPaths(t *testing.T) [][]string {
	t.Helper()
	env := ui.Env{Width: 60, Clamp: true}
	root, _ := buildRoot(
		&app{env: &cli.Env{Out: io.Discard, Err: io.Discard, OutEnv: env, ErrEnv: env}},
		func(io.Writer) ui.Env { return env },
	)

	paths := [][]string{nil}
	var walk func(*cobra.Command, []string)
	walk = func(parent *cobra.Command, prefix []string) {
		for _, sub := range parent.Commands() {
			if !sub.IsAvailableCommand() || sub.Name() == "completion" {
				continue
			}
			path := append(append([]string(nil), prefix...), sub.Name())
			paths = append(paths, path)
			walk(sub, path)
		}
	}
	walk(root, nil)
	return paths
}

func TestUsageFitsANarrowTerminal(t *testing.T) {
	for _, path := range helpPaths(t) {
		name := "hoard"
		if len(path) > 0 {
			name = strings.Join(path, " ")
		}
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			renderHelp(&b, ui.Env{Width: 60, Clamp: true}, path...)
			for line := range strings.SplitSeq(b.String(), "\n") {
				if n := len([]rune(line)); n > 60 {
					t.Errorf("line %d wide: %q", n, line)
				}
			}
		})
	}
}

func TestUsagePipedListsEveryCommand(t *testing.T) {
	var b bytes.Buffer
	renderHelp(&b, ui.Env{Width: 100})
	out := b.String()

	for _, want := range []string{
		"a terminal collection tracker for Magic: The Gathering",
		"Browse the hoard",
		"Collection commands:", "Binder commands:", "Deck commands:", "Interop commands:",
		"add", "update-prices", "movers", "backfill-prices", "unpriced", "guessed",
		"misfinished", "vacuum", "market", "report", "watch", "catalog",
		"binder", "deck", "export", "import", "merge", "compendium", "schema", "version",
		"update",
		"completion",
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

func TestGlobalFlagsAppearInHelp(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		want    []string
		notWant []string
	}{

		{"root", nil, []string{"Global flags:", "--db", "--json"}, nil},

		{"export", []string{"export"}, []string{"Global flags:", "--db", "--json"}, nil},

		{"binder", []string{"binder"}, []string{"Global flags:", "--db", "--json"}, nil},
		{"binder list", []string{"binder", "list"}, []string{"Global flags:", "--db", "--json"}, nil},

		{"add", []string{"add"}, []string{"Global flags:", "--db"}, []string{"--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			renderHelp(&b, ui.Env{Width: 100}, tc.args...)
			for _, want := range tc.want {
				if !strings.Contains(b.String(), want) {
					t.Errorf("help for %q missing %q; got:\n%s", tc.args, want, b.String())
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(b.String(), notWant) {
					t.Errorf("help for %q advertises %q, which it rejects; got:\n%s",
						tc.args, notWant, b.String())
				}
			}
		})
	}
}

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
			"[--format csv|json|text|moxfield|archidekt|manabox]",
		}},

		{[]string{"movers"}, []string{"--since", "--limit", "how far back to compare"}},

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
