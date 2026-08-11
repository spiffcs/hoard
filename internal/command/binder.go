package command

// The binder commands: organizing the loose collection into labelled parts.
//
// Ported to the command tree. The hand-rolled sub-dispatch that used to open
// this file is gone: which subcommand ran, and what to say when none matched,
// is the tree's job now (see newBinderCmd in commands.go). What is left is one
// function per subcommand, each of which does only its own work.

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
)

// binderList prints the binders, as a table or as the binders document.
//
// The JSON path exists because a binder's id is an argument elsewhere: export,
// import and add all take --binder as an id, a name or a unique fragment, so
// discovering what binders there are is a prerequisite to using any of them.
// Without this, that discovery step was the one place in hoard's machine
// interface where a caller had to parse a formatted table.
func binderList(st *store.Store, env *cli.Env) error {
	binders, err := st.ListBinders()
	if err != nil {
		return err
	}
	if env.JSON {
		return hoardjson.Write(env.Out, hoardjson.FromBinders(binders))
	}
	fmt.Fprint(env.Out, report.Binders(env.OutEnv, binders))
	return nil
}

func binderNew(st *store.Store, env *cli.Env, args []string) error {
	if len(args) != 1 {
		return cli.Usagef("binder new takes one name: hoard binder new NAME")
	}
	id, err := st.CreateBinder(args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "Created binder #%d %q\n", id, strings.TrimSpace(args[0]))
	return nil
}

func binderRename(st *store.Store, env *cli.Env, args []string) error {
	if len(args) != 2 {
		return cli.Usagef("binder rename takes a binder and a new name: hoard binder rename BINDER NEW-NAME")
	}
	b, err := st.BinderByRef(args[0])
	if err != nil {
		return err
	}
	if err := st.RenameBinder(b.ID, args[1]); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "Renamed binder %q to %q\n", b.Name, strings.TrimSpace(args[1]))
	return nil
}

func binderRemove(st *store.Store, env *cli.Env, args []string) error {
	if len(args) != 1 {
		return cli.Usagef("binder rm takes one binder: hoard binder rm BINDER")
	}
	b, err := st.BinderByRef(args[0])
	if err != nil {
		return err
	}
	if err := st.DeleteBinder(b.ID); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "Removed binder %q\n", b.Name)
	return nil
}

// NewCmdBinder is a group whose bare form is a shorthand: `hoard binder` lists,
// which is what it did before the port.
//
// The group and its list subcommand are both JSONCapable because both of them
// list; the annotation does not descend, so new, rename and rm still refuse
// --json, which is right — they report an action rather than a result.
func NewCmdBinder(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "binder",
		GroupID: groupBinder,
		Short:   "Organize the loose collection into labelled parts",
		Example: "hoard binder [list|new|rename|rm]",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return binderList(a.store, a.env)
		},
	}
	cmd.AddCommand(
		cli.JSONCapable(&cobra.Command{
			Use: "list", Short: "Your binders, with counts and value",
			Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error { return binderList(a.store, a.env) },
		}),
		&cobra.Command{
			Use: "new NAME", Short: "Create a named binder",
			Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error { return binderNew(a.store, a.env, args) },
		},
		&cobra.Command{
			Use: "rename BINDER NEW-NAME", Short: "Rename a binder",
			Args: cobra.ExactArgs(2),
			RunE: func(_ *cobra.Command, args []string) error { return binderRename(a.store, a.env, args) },
		},
		&cobra.Command{
			Use: "rm BINDER", Short: "Remove an empty binder",
			Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error { return binderRemove(a.store, a.env, args) },
		},
	)
	return cli.JSONCapable(cmd)
}
