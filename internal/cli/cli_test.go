package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/ui"
)

// pipeEnv is the shape a piped stream gets: fixed width, no color, nothing
// truncated. Assertions below compare plain text because of it.
func pipeEnv(io.Writer) ui.Env { return ui.Env{Width: 80} }

func newRoot() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root := &cobra.Command{
		Use:           "hoard [command]",
		Short:         "hoard: catalog valuable MTG cards and decks in SQLite",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.AddGroup(&cobra.Group{ID: "collection", Title: "Collection commands:"})
	InstallHelp(root, "hoard: catalog valuable MTG cards and decks in SQLite", pipeEnv)
	return root, out, errOut
}

// The terminator has to protect every argument after it, not just the first.
//
// This is the bug that motivated the whole restructuring: the hand-rolled
// parser let flag.Parse consume the -- and then re-parsed the tail with flags
// live again, so `hoard add -- "Sol Ring" --foil` set foil anyway and a card
// legitimately named "-1/-1" could not be passed at all. pflag handles it; the
// test stays because the behavior is what matters, not who implements it.
func TestTerminatorProtectsEveryFollowingArgument(t *testing.T) {
	var got []string
	var foil bool

	root, _, _ := newRoot()
	add := &cobra.Command{
		Use:  "add",
		RunE: func(_ *cobra.Command, args []string) error { got = args; return nil },
	}
	add.Flags().BoolVar(&foil, "foil", false, "")
	root.AddCommand(add)

	root.SetArgs([]string{"add", "--", "Sol Ring", "--foil"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if foil {
		t.Error("--foil after -- was parsed as a flag; the terminator did not hold")
	}
	if want := []string{"Sol Ring", "--foil"}; !equal(got, want) {
		t.Errorf("positionals = %q, want %q", got, want)
	}
}

func TestTerminatorAllowsFlagShapedCardNames(t *testing.T) {
	var got []string

	root, _, _ := newRoot()
	add := &cobra.Command{
		Use:  "add",
		RunE: func(_ *cobra.Command, args []string) error { got = args; return nil },
	}
	add.Flags().Float64("over", 0, "")
	root.AddCommand(add)

	// "-1/-1" is a real card name; before the port this was unpassable.
	root.SetArgs([]string{"add", "--", "Ach! Hans", "-1/-1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if want := []string{"Ach! Hans", "-1/-1"}; !equal(got, want) {
		t.Errorf("positionals = %q, want %q", got, want)
	}
}

// Flags and positionals interleave in any order — `hoard add "Sol Ring" --foil`
// is what people type, and the standard library's flag package stops at the
// first positional.
func TestFlagsAndPositionalsInterleave(t *testing.T) {
	var got []string
	var foil bool
	var qty int

	root, _, _ := newRoot()
	add := &cobra.Command{
		Use:  "add",
		RunE: func(_ *cobra.Command, args []string) error { got = args; return nil },
	}
	add.Flags().BoolVar(&foil, "foil", false, "")
	add.Flags().IntVar(&qty, "qty", 1, "")
	root.AddCommand(add)

	root.SetArgs([]string{"add", "Sol Ring", "--foil", "Black Lotus", "--qty", "3"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !foil || qty != 3 {
		t.Errorf("foil=%v qty=%d, want true and 3", foil, qty)
	}
	if want := []string{"Sol Ring", "Black Lotus"}; !equal(got, want) {
		t.Errorf("positionals = %q, want %q", got, want)
	}
}

// Asking for help is not a failure, and it renders through hoard's table
// engine rather than cobra's text template.
func TestHelpSucceedsAndUsesTheHoardRenderer(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			ran := false
			root, out, _ := newRoot()
			report := &cobra.Command{
				Use:     "report",
				GroupID: "collection",
				Short:   "Dated valuation",
				Example: "hoard report [--top N] [--csv]",
				RunE:    func(*cobra.Command, []string) error { ran = true; return nil },
			}
			report.Flags().Int("top", 10, "holdings to itemize")
			root.AddCommand(report)

			root.SetArgs([]string{"report", arg})
			if err := root.Execute(); err != nil {
				t.Fatalf("help returned an error: %v", err)
			}
			if ran {
				t.Error("RunE executed for a help request")
			}
			for _, want := range []string{"Usage:", "hoard report [--top N] [--csv]", "--top", "holdings to itemize"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("help missing %q; got:\n%s", want, out.String())
				}
			}
		})
	}
}

// --json is a property of the command, not a list maintained in three places.
func TestJSONPolicyComesFromTheAnnotation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		capable bool
		wantErr bool
	}{
		{"movers", true, false},
		{"binder", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: tc.name}
			if tc.capable {
				JSONCapable(cmd)
			}

			err := CheckJSON(cmd, true)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("--json rejected on a command that supports it: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected --json to be rejected")
			}
			if !errors.Is(err, ErrUsage) {
				t.Errorf("rejection is not ErrUsage: %v", err)
			}
			if !strings.Contains(err.Error(), "no JSON output") {
				t.Errorf("unhelpful message: %v", err)
			}
		})
	}
}

// The sentinel's name must never reach the user: wrapping ErrUsage with %w
// would render as "usage: choose exactly one of --under N or --over N".
func TestUsageErrorsCarryOnlyTheirMessage(t *testing.T) {
	err := Usagef("choose exactly one of --under N or --over N")
	if got := err.Error(); got != "choose exactly one of --under N or --over N" {
		t.Errorf("Error() = %q, want the message alone", got)
	}
	if !errors.Is(err, ErrUsage) {
		t.Error("Usagef result does not match ErrUsage")
	}
}

// The root help is generated from the tree, so a command that exists is a
// command that appears — the table it replaced was a separate var that nothing
// checked against the dispatch switch.
func TestRootHelpIsGeneratedFromTheTree(t *testing.T) {
	root, out, _ := newRoot()
	root.AddCommand(
		&cobra.Command{Use: "movers", GroupID: "collection", Short: "Biggest risers", Run: func(*cobra.Command, []string) {}},
		&cobra.Command{Use: "report", GroupID: "collection", Short: "Dated valuation", Run: func(*cobra.Command, []string) {}},
		&cobra.Command{Use: "secret", GroupID: "collection", Short: "hidden", Hidden: true, Run: func(*cobra.Command, []string) {}},
	)

	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		"hoard: catalog valuable MTG cards and decks in SQLite",
		"Collection commands:",
		"movers", "Biggest risers",
		"report", "Dated valuation",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("root help missing %q; got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "secret") {
		t.Error("a hidden command was advertised")
	}
}

// Help is width-responsive because it renders through ui.Table — the reason
// this package keeps its own renderer instead of cobra's text template.
func TestHelpFitsANarrowTerminal(t *testing.T) {
	out := &bytes.Buffer{}
	root := &cobra.Command{Use: "hoard [command]", SilenceUsage: true}
	root.SetOut(out)
	root.AddGroup(&cobra.Group{ID: "collection", Title: "Collection commands:"})
	InstallHelp(root, "hoard: catalog valuable MTG cards and decks in SQLite",
		func(io.Writer) ui.Env { return ui.Env{Width: 60, Clamp: true} })
	root.AddCommand(&cobra.Command{
		Use: "backfill-prices", GroupID: "collection",
		Short: "Load 90 days of past prices from MTGJSON, which is a long description",
		Run:   func(*cobra.Command, []string) {},
	})

	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for line := range strings.SplitSeq(out.String(), "\n") {
		if n := len([]rune(line)); n > 60 {
			t.Errorf("line %d wide: %q", n, line)
		}
	}
}

// A flag a parent declared works on the child, so the child's help has to say
// so. cobra's LocalFlags is defined to exclude inherited flags, which is why
// hoard shipped a --json and a --db that worked everywhere and were printed
// nowhere.
//
// The section is filtered by the same annotation CheckJSON enforces: a command
// that would be told "no JSON output" must not have been told, one screen
// earlier, that --json was among its flags.
func TestInheritedFlagsAppearOnlyWhereTheyWork(t *testing.T) {
	for _, capable := range []bool{true, false} {
		name := map[bool]string{true: "json-capable", false: "plain"}[capable]
		t.Run(name, func(t *testing.T) {
			root, out, _ := newRoot()
			root.PersistentFlags().String("db", "", "the hoard database to use")
			root.PersistentFlags().Bool(FlagNameJSON, false, "emit JSON instead of tables")

			sub := &cobra.Command{Use: "report", Run: func(*cobra.Command, []string) {}}
			if capable {
				JSONCapable(sub)
			}
			root.AddCommand(sub)

			root.SetArgs([]string{"report", "--help"})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			got := out.String()

			for _, want := range []string{"Global flags:", "--db", "the hoard database to use"} {
				if !strings.Contains(got, want) {
					t.Errorf("command help missing %q; got:\n%s", want, got)
				}
			}
			if has := strings.Contains(got, "--json"); has != capable {
				t.Errorf("--json listed = %v on a command whose JSONCapable = %v; got:\n%s",
					has, capable, got)
			}
		})
	}
}

// The root's own flags were invisible for the same reason by a different route:
// writeRootHelp emitted a command table and then stopped, so nothing registered
// on root was documented at all.
func TestRootHelpListsItsOwnAndItsGlobalFlags(t *testing.T) {
	root, out, _ := newRoot()
	root.Flags().BoolP("version", "v", false, "print this build's version")
	root.PersistentFlags().String("db", "", "the hoard database to use")
	JSONCapable(root)
	root.PersistentFlags().Bool(FlagNameJSON, false, "emit JSON instead of tables")

	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		"Flags:", "-v, --version", "print this build's version",
		"Global flags:", "--db", "--json", "emit JSON instead of tables",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("root help missing %q; got:\n%s", want, got)
		}
	}
	// --version is root's own, not persistent; listing it under both headings
	// would say it works on every subcommand, which it does not.
	if n := strings.Count(got, "--version"); n != 1 {
		t.Errorf("--version listed %d times, want once; got:\n%s", n, got)
	}
}

// The flag block is the one part of a help page cobra renders as plain text,
// and pflag's default FlagUsages does not wrap: a long description ran past
// the edge of a terminal every other section had been fitted to.
func TestFlagUsagesWrapToTheTerminal(t *testing.T) {
	out := &bytes.Buffer{}
	root := &cobra.Command{Use: "hoard [command]", SilenceUsage: true}
	root.SetOut(out)
	InstallHelp(root, "hoard: catalog valuable MTG cards and decks in SQLite",
		func(io.Writer) ui.Env { return ui.Env{Width: 60, Clamp: true} })
	root.PersistentFlags().String("db", "",
		"the hoard database to use (default $HOARD_DB, else the per-user data dir)")
	sub := &cobra.Command{Use: "export", Run: func(*cobra.Command, []string) {}}
	sub.Flags().String("format", "csv",
		"output format: csv (canonical), json, moxfield, or archidekt")
	root.AddCommand(sub)

	for _, args := range [][]string{{"--help"}, {"export", "--help"}} {
		out.Reset()
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("execute %q: %v", args, err)
		}
		for line := range strings.SplitSeq(out.String(), "\n") {
			if n := len([]rune(line)); n > 60 {
				t.Errorf("%q: line %d wide: %q", args, n, line)
			}
		}
	}
}

// Cobra suggests a near-miss instead of dumping the command list — one of the
// things adopting it bought outright.
func TestUnknownCommandSuggestsTheNearMiss(t *testing.T) {
	root, _, _ := newRoot()
	root.AddCommand(&cobra.Command{Use: "movers", GroupID: "collection", Run: func(*cobra.Command, []string) {}})

	root.SetArgs([]string{"mover"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error for an unknown command")
	}
	if !strings.Contains(err.Error(), "movers") {
		t.Errorf("no suggestion in %q", err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
