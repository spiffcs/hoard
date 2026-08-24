package command

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
)

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

func NewCmdBinder(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "binder",
		GroupID: groupBinder,
		Short:   "Organize the loose collection into labelled parts",
		Long: "Organize the loose collection into labelled parts.\n\n" +
			"An excluded binder is a wantlist. Its cards are still\n" +
			"priced, still show up in movers, and can still carry a\n" +
			"watch. They just do not count toward what your\n" +
			"collection is worth:\n\n" +
			"  hoard binder new Want\n" +
			"  hoard binder exclude Want\n" +
			"  hoard add CARD-URL --binder Want\n\n" +
			"To move one card out of Want once you own it, open\n" +
			"the browser: enter for the card's detail, up into the\n" +
			"row for the copy you hold, right to its last field,\n" +
			"then enter to name the binder it moves to.\n\n" +
			"To move a lot of them at once, pipe an export through\n" +
			"move:\n\n" +
			"  hoard export --binder Want --json | hoard move --to Binder",
		Example: "hoard binder [list|new|rename|rm|exclude|include]",
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
		&cobra.Command{
			Use: "exclude BINDER", Short: "Stop a binder counting toward your collection",
			Example: "hoard binder exclude Want",
			Args:    cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				return binderCounted(a.store, a.env, args, false)
			},
		},
		&cobra.Command{
			Use: "include BINDER", Short: "Count a binder toward your collection again",
			Example: "hoard binder include Want",
			Args:    cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				return binderCounted(a.store, a.env, args, true)
			},
		},
	)
	return cli.JSONCapable(cmd)
}
