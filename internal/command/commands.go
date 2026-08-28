package command

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/compendium"
	"github.com/spiffcs/hoard/internal/store"
)

type app struct {
	store *store.Store
	env   *cli.Env

	dbPath string

	confirmMove     func(string) bool
	buildCompendium func(context.Context, string, compendium.Options) error
}

const tagline = "a terminal collection tracker for Magic: The Gathering"

const (
	groupCollection = "collection"
	groupBinder     = "binder"
	groupDeck       = "deck"
	groupInterop    = "interop"
)

func commandGroups() []*cobra.Group {
	return []*cobra.Group{
		{ID: groupCollection, Title: "Collection commands:"},
		{ID: groupBinder, Title: "Binder commands:"},
		{ID: groupDeck, Title: "Deck commands:"},
		{ID: groupInterop, Title: "Interop commands:"},
	}
}

func rootCommand(a *app) *cobra.Command {

	cobra.EnableCommandSorting = false

	var versionFlag bool

	root := &cobra.Command{
		Use:   "hoard [command]",
		Short: tagline,

		RunE: func(c *cobra.Command, _ []string) error {
			if versionFlag {
				printVersion(a.env.Out, a.env.OutEnv, cachedStatus(c.Context()))
				return nil
			}
			return cmdBrowse(c.Context(), a.store, a.env.JSON)
		},
		SilenceUsage:  true,
		SilenceErrors: true,

		DisableFlagsInUseLine: true,
	}
	root.Flags().BoolVarP(&versionFlag, "version", "v", false, "print this build's version and legal notices")
	cli.JSONCapable(root)
	root.AddGroup(commandGroups()...)

	root.AddCommand(

		NewCmdAdd(a),

		NewCmdUpdatePrices(a),

		NewCmdMovers(a),

		NewCmdBackfillPrices(a),

		NewCmdUnpriced(a),

		NewCmdRefused(a),

		NewCmdGuessed(a),

		NewCmdRepairFinishes(a),

		NewCmdVacuum(a),

		NewCmdMarket(a),

		NewCmdReport(a),

		NewCmdWatch(a),

		NewCmdCatalog(a),

		NewCmdBinder(a),

		NewCmdMove(a),

		NewCmdDeck(a),

		NewCmdFolder(a),

		NewCmdExport(a),

		NewCmdImport(a),

		NewCmdMerge(a),

		NewCmdDemo(a),

		NewCmdCompendium(a),

		NewCmdSchema(a),

		NewCmdVersion(a),

		NewCmdUpdate(a),
	)

	return root
}
