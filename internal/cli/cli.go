// Package cli is the support layer between cobra and hoard.
//
// Cobra owns parsing, dispatch, completion and typo suggestions. This package
// supplies the three things it has no opinion about: where a command writes
// (Env), what a usage failure looks like by the time it reaches main, and how
// help is laid out — hoard renders through ui.Table at the terminal's real
// width, which cobra's text templates cannot do.
//
// Commands close over what they need at construction time, the way crane's
// NewCmdXxx constructors do, because the store cannot exist until --db has been
// parsed and the tree has to exist before that to parse --db at all.
package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/spiffcs/hoard/internal/ui"
)

// Env is what every command writes through.
//
// Out and Err are injected rather than assumed so a command can be tested by
// handing it a buffer, and so the renderer's width and color decisions are made
// once, at the boundary, against the stream actually being written to — not
// sniffed from os.Stdout by whatever code happens to be rendering.
type Env struct {
	Out, Err io.Writer
	// OutEnv and ErrEnv are resolved against Out and Err respectively. One Env
	// for both streams is what let a table meant for a test buffer read the
	// real terminal's width.
	OutEnv, ErrEnv ui.Env
	// JSON reports whether --json was given. It is only ever true for a command
	// that declared JSONCapable; the check happens once, in the root's
	// PersistentPreRunE, so a script asking for JSON can never be handed a
	// table instead.
	JSON bool
}

// Report builds the prose reporter bound to this Env's streams.
//
// ui.NewReport detects the real os streams, which is correct for the binary and
// wrong for a test holding a buffer — a command that reached for it directly
// wrote past whatever the test injected, which is why so few of them could
// assert on their own output. Env already carries both streams and both
// resolved renderers, so it is the thing that should hand out the reporter.
func (e *Env) Report() *ui.Report {
	return &ui.Report{Out: e.Out, Err: e.Err, OutEnv: e.OutEnv, ErrEnv: e.ErrEnv}
}

// AnnotationJSON marks a command whose Run honors Env.JSON. Absent means --json
// is refused, which is the safe direction: output that silently ignored the
// flag is indistinguishable, to a parser, from output that honored it.
const AnnotationJSON = "hoard/json"

// AnnotationNoStore marks a command that must answer without opening the
// database — version, and anything else worth having when the store is missing
// or locked.
const AnnotationNoStore = "hoard/nostore"

// FlagNameJSON is the global flag AnnotationJSON governs.
//
// The name lives here rather than only in the package that registers the flag
// because this package is what refuses it: the help renderer has to hide the
// flag on exactly the commands CheckJSON would reject, and a second spelling
// of "json" is how those two would drift into disagreeing about which flag
// they are talking about.
const FlagNameJSON = "json"

// JSONCapable declares that cmd honors --json.
func JSONCapable(cmd *cobra.Command) *cobra.Command { return annotate(cmd, AnnotationJSON) }

// NoStore declares that cmd runs without a database.
func NoStore(cmd *cobra.Command) *cobra.Command { return annotate(cmd, AnnotationNoStore) }

func annotate(cmd *cobra.Command, key string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[key] = "true"
	return cmd
}

// Has reports whether cmd carries an annotation.
func Has(cmd *cobra.Command, key string) bool {
	return cmd != nil && cmd.Annotations[key] == "true"
}

// ErrUsage marks a failure the user can fix themselves — a bad flag, a missing
// argument. The message already says what to do, so main prints it and stops
// there rather than following it with the whole command list.
var ErrUsage = errors.New("usage")

// usageError carries its own text so the sentinel's name never reaches the
// user: wrapping with %w would render as "usage: choose exactly one of ...".
// Matching through Is keeps errors.Is working without that prefix.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func (e *usageError) Is(target error) bool { return target == ErrUsage }

// Usagef builds a usage error, so a command reporting a wrong argument count
// speaks in the same voice cobra's own parse failures do.
func Usagef(format string, a ...any) error {
	return &usageError{msg: fmt.Sprintf(format, a...)}
}

// CheckJSON enforces the --json policy for the command about to run.
func CheckJSON(cmd *cobra.Command, requested bool) error {
	if requested && !Has(cmd, AnnotationJSON) {
		return Usagef("%s has no JSON output", cmd.CommandPath())
	}
	return nil
}

// InstallHelp replaces cobra's text templates with hoard's table renderer, for
// root and everything under it.
//
// envFor resolves the terminal shape of whichever stream help is being written
// to, so `hoard --help | less` gets the piped rendering and a terminal gets the
// clamped one. Tests pass a fixed resolver.
//
// One deliberate departure from cobra's conventions, worth knowing before
// writing a command: hoard puts a command's alternate *usage forms* in
// cobra.Command.Example, one per line, and writeCommandHelp renders them under
// "Usage:". Cobra's own meaning for that field is worked examples — crane fills
// it with shell transcripts, prompts and comments included. hoard's reading is
// what lets each command carry the forms the root help used to list for
// everything at once, but it does mean the field says something here that it
// does not say elsewhere.
func InstallHelp(root *cobra.Command, tagline string, envFor func(io.Writer) ui.Env) {
	render := func(cmd *cobra.Command, w io.Writer) {
		env := envFor(w)
		if cmd == root {
			writeRootHelp(cmd, w, env, tagline)
			return
		}
		writeCommandHelp(cmd, w, env)
	}
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		render(cmd, cmd.OutOrStdout())
	})
	root.SetUsageFunc(func(cmd *cobra.Command) error {
		render(cmd, cmd.ErrOrStderr())
		return nil
	})
}

// writeRootHelp lists the commands, grouped, one line each.
//
// One line each is a deliberate change from the hand-written table this
// replaced, which spelled out every form of every command and ran to 44 rows.
// The forms now live in each command's own help, where they are read at the
// moment they matter; the root's job is to answer "what can this thing do".
func writeRootHelp(root *cobra.Command, w io.Writer, env ui.Env, tagline string) {
	bold := env.Bold()

	var b strings.Builder
	b.WriteString(bold(tagline) + "\n\n")
	b.WriteString(bold("Usage:") + "\n")
	b.WriteString("  " + root.UseLine() + "\n")

	t := ui.Table{Env: env, Cols: []ui.Col{
		// Min lets the name column shrink on a narrow terminal; without one the
		// layout engine can only give up and let rows run past the edge.
		{Align: ui.Left, Min: 16},
		{Align: ui.Left, Flex: true, Min: 16},
	}}
	t.AddSpacer()
	t.Add(ui.C("  hoard"), ui.C("Browse the hoard"))

	for _, g := range root.Groups() {
		rows := rowsInGroup(root, g.ID)
		if len(rows) == 0 {
			continue
		}
		t.AddSpacer()
		t.AddStyled(bold, ui.C(g.Title))
		for _, r := range rows {
			t.Add(ui.C("  "+r[0]), ui.C(r[1]))
		}
	}

	// The ungrouped remainder is what cobra adds for itself: help, completion.
	if rows := rowsInGroup(root, ""); len(rows) > 0 {
		t.AddSpacer()
		t.AddStyled(bold, ui.C("Other commands:"))
		for _, r := range rows {
			t.Add(ui.C("  "+r[0]), ui.C(r[1]))
		}
	}

	b.WriteString(t.Render())

	// Root's own flags, then the ones every command inherits. Without this the
	// only global hoard has — --db, the one safe way to point the binary at a
	// scratch database — was reachable from nothing but the source: the root
	// help wrote a command table and stopped, so a flag registered on root was
	// documented nowhere at all.
	if f := flagUsages(root.LocalNonPersistentFlags(), env); f != "" {
		b.WriteString("\n" + bold("Flags:") + "\n" + f)
	}
	if f := globalFlagUsages(root, root.PersistentFlags(), env); f != "" {
		b.WriteString("\n" + bold("Global flags:") + "\n" + f)
	}

	b.WriteString("\n" + env.Dim()(`Run "hoard CMD --help" for its forms and flags.`) + "\n")
	fmt.Fprint(w, b.String())
}

func rowsInGroup(root *cobra.Command, id string) [][2]string {
	var rows [][2]string
	for _, sub := range root.Commands() {
		if sub.GroupID == id && sub.IsAvailableCommand() {
			rows = append(rows, [2]string{sub.Name(), sub.Short})
		}
	}
	return rows
}

// writeCommandHelp is one command's own page: its forms, what it does, its
// subcommands, its flags.
func writeCommandHelp(cmd *cobra.Command, w io.Writer, env ui.Env) {
	bold := env.Bold()

	var b strings.Builder
	b.WriteString(bold("Usage:") + "\n")
	if cmd.Example != "" {
		// Example holds this command's alternate invocations, one per line —
		// the forms the old root table used to carry for everything at once.
		for line := range strings.SplitSeq(strings.TrimRight(cmd.Example, "\n"), "\n") {
			b.WriteString("  " + strings.TrimSpace(line) + "\n")
		}
	} else {
		b.WriteString("  " + cmd.UseLine() + "\n")
	}

	switch {
	case cmd.Long != "":
		b.WriteString("\n" + strings.TrimRight(cmd.Long, "\n") + "\n")
	case cmd.Short != "":
		b.WriteString("\n" + cmd.Short + "\n")
	}

	if subs := availableSubs(cmd); len(subs) > 0 {
		b.WriteString("\n" + bold("Subcommands:") + "\n")
		t := ui.Table{Env: env, Cols: []ui.Col{
			{Align: ui.Left, Min: 10},
			{Align: ui.Left, Flex: true, Min: 16},
		}}
		for _, sub := range subs {
			t.Add(ui.C("  "+sub.Name()), ui.C(sub.Short))
		}
		b.WriteString(t.Render())
	}

	if f := flagUsages(cmd.LocalFlags(), env); f != "" {
		b.WriteString("\n" + bold("Flags:") + "\n" + f)
	}
	// LocalFlags deliberately excludes what a parent declared, which is right
	// for the section above and is why --json and --db appeared on no page in
	// the binary despite working on every one of them. They get their own
	// heading rather than being folded in, because "these work here" and
	// "these work everywhere" are different facts about a flag.
	if f := globalFlagUsages(cmd, cmd.InheritedFlags(), env); f != "" {
		b.WriteString("\n" + bold("Global flags:") + "\n" + f)
	}

	fmt.Fprint(w, b.String())
}

// globalFlagUsages renders the inherited flags cmd will actually accept.
//
// --json is refused outright by CheckJSON on any command that did not declare
// AnnotationJSON, so listing it under every command would leave help as the
// only place in hoard claiming `hoard binder rename --json` is a thing. The
// filter is what lets the section stay literally true: every flag printed on a
// page is a flag that page's command accepts.
//
// Discoverability does not pay for that, because root itself is JSONCapable —
// `hoard --help`, which is where a reader goes to learn what exists at all,
// still advertises --json. The per-command pages only repeat it where it works.
func globalFlagUsages(cmd *cobra.Command, flags *pflag.FlagSet, env ui.Env) string {
	shown := pflag.NewFlagSet("global", pflag.ContinueOnError)
	flags.VisitAll(func(f *pflag.Flag) {
		if f.Name == FlagNameJSON && !Has(cmd, AnnotationJSON) {
			return
		}
		shown.AddFlag(f)
	})
	return flagUsages(shown, env)
}

// flagUsages lays a flag block out at the terminal's real width.
//
// pflag's unwrapped FlagUsages is the one thing on a help page that ignores the
// width everything else was fitted to: --db's description alone renders 89
// columns wide, so a 60-column terminal got a help screen whose tables all fit
// and whose flags ran off the edge.
func flagUsages(flags *pflag.FlagSet, env ui.Env) string {
	return flags.FlagUsagesWrapped(env.Width)
}

func availableSubs(cmd *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.IsAvailableCommand() {
			out = append(out, sub)
		}
	}
	return out
}
