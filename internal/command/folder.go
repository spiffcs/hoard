package command

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
)

func NewCmdFolder(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "folder",
		GroupID: groupDeck,
		Short:   "Group decks into folders",
		Example: "hoard folder [list|new|rename|rm]",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return folderList(a.store, a.env)
		},
	}
	cmd.AddCommand(
		&cobra.Command{
			Use: "list", Short: "Your deck folders, with counts and value",
			Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error { return folderList(a.store, a.env) },
		},
		cli.Mutating(&cobra.Command{
			Use: "new NAME", Short: "Create a deck folder",
			Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error { return folderNew(a.store, a.env, args) },
		}),
		cli.Mutating(&cobra.Command{
			Use: "rename FOLDER NEW-NAME", Short: "Rename a deck folder",
			Args: cobra.ExactArgs(2),
			RunE: func(_ *cobra.Command, args []string) error { return folderRename(a.store, a.env, args) },
		}),
		cli.Mutating(&cobra.Command{
			Use: "rm FOLDER", Short: "Remove a folder, returning its decks to the top level",
			Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error { return folderRemove(a.store, a.env, args) },
		}),
	)
	return cmd
}

func folderList(st *store.Store, env *cli.Env) error {
	folders, err := st.ListFolders()
	if err != nil {
		return err
	}
	fmt.Fprint(env.Out, report.Binders(env.OutEnv, folders))
	return nil
}

func folderNew(st *store.Store, env *cli.Env, args []string) error {
	id, err := st.CreateFolder(args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "Created folder #%d %q\n", id, strings.TrimSpace(args[0]))
	return nil
}

func folderRename(st *store.Store, env *cli.Env, args []string) error {
	f, err := st.FolderByRef(args[0])
	if err != nil {
		return err
	}
	if err := st.RenameFolder(f.ID, args[1]); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "Renamed folder %q to %q\n", f.Name, strings.TrimSpace(args[1]))
	return nil
}

func folderRemove(st *store.Store, env *cli.Env, args []string) error {
	f, err := st.FolderByRef(args[0])
	if err != nil {
		return err
	}
	if err := st.RemoveFolder(f.ID); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "Removed folder %q\n", f.Name)
	return nil
}

func deckMove(st *store.Store, env *cli.Env, args []string) error {
	d, err := st.DeckByRef(args[0])
	if err != nil {
		return err
	}
	if len(args) == 1 {
		if err := st.MoveDeckToFolder(d.ID, 0); err != nil {
			return err
		}
		fmt.Fprintf(env.Out, "Moved deck %q to the top level\n", d.Name)
		return nil
	}
	f, err := st.FolderByRef(args[1])
	if err != nil {
		return err
	}
	if err := st.MoveDeckToFolder(d.ID, f.ID); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "Moved deck %q into %q\n", d.Name, f.Name)
	return nil
}

func newDeckMoveCmd(a *app) *cobra.Command {
	return cli.Mutating(&cobra.Command{
		Use:   "move DECK [FOLDER]",
		Short: "Put a deck in a folder, or omit FOLDER to move it back out",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			return deckMove(a.store, a.env, args)
		},
	})
}
