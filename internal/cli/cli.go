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

type Env struct {
	Out, Err io.Writer

	OutEnv, ErrEnv ui.Env

	JSON bool
}

func (e *Env) Report() *ui.Report {
	return &ui.Report{Out: e.Out, Err: e.Err, OutEnv: e.OutEnv, ErrEnv: e.ErrEnv}
}

const AnnotationJSON = "hoard/json"

const AnnotationNoStore = "hoard/nostore"

const FlagNameJSON = "json"

func JSONCapable(cmd *cobra.Command) *cobra.Command { return annotate(cmd, AnnotationJSON) }

func NoStore(cmd *cobra.Command) *cobra.Command { return annotate(cmd, AnnotationNoStore) }

func annotate(cmd *cobra.Command, key string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[key] = "true"
	return cmd
}

func Has(cmd *cobra.Command, key string) bool {
	return cmd != nil && cmd.Annotations[key] == "true"
}

var ErrUsage = errors.New("usage")

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func (e *usageError) Is(target error) bool { return target == ErrUsage }

func Usagef(format string, a ...any) error {
	return &usageError{msg: fmt.Sprintf(format, a...)}
}

func CheckJSON(cmd *cobra.Command, requested bool) error {
	if requested && !Has(cmd, AnnotationJSON) {
		return Usagef("%s has no JSON output", cmd.CommandPath())
	}
	return nil
}

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

func writeRootHelp(root *cobra.Command, w io.Writer, env ui.Env, tagline string) {
	bold := env.Bold()

	var b strings.Builder
	b.WriteString(bold(tagline) + "\n\n")
	b.WriteString(bold("Usage:") + "\n")
	b.WriteString("  " + root.UseLine() + "\n")

	t := ui.Table{Env: env, Cols: []ui.Col{

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

	if rows := rowsInGroup(root, ""); len(rows) > 0 {
		t.AddSpacer()
		t.AddStyled(bold, ui.C("Other commands:"))
		for _, r := range rows {
			t.Add(ui.C("  "+r[0]), ui.C(r[1]))
		}
	}

	b.WriteString(t.Render())

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

func writeCommandHelp(cmd *cobra.Command, w io.Writer, env ui.Env) {
	bold := env.Bold()

	var b strings.Builder
	b.WriteString(bold("Usage:") + "\n")
	if cmd.Example != "" {

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

	if f := globalFlagUsages(cmd, cmd.InheritedFlags(), env); f != "" {
		b.WriteString("\n" + bold("Global flags:") + "\n" + f)
	}

	fmt.Fprint(w, b.String())
}

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
