package command

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/ui"
)

func NewCmdCatalog(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "catalog",
		GroupID: groupCollection,
		Short:   "The local copy of Scryfall's card data",
		Example: "hoard catalog [status|update]",
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runCatalogStatus(c.Context(), a.env)
		},
	}
	cmd.AddCommand(
		&cobra.Command{
			Use: "status", Short: "Whether the catalog is present and current",
			Args: cobra.NoArgs,
			RunE: func(c *cobra.Command, _ []string) error {
				return runCatalogStatus(c.Context(), a.env)
			},
		},
		&cobra.Command{
			Use: "update", Short: "Download or rebuild the catalog from Scryfall",
			Args: cobra.NoArgs,
			RunE: func(c *cobra.Command, _ []string) error {
				return runCatalogUpdate(c.Context(), a.env)
			},
		},
	)
	return cmd
}

func runCatalogStatus(ctx context.Context, env *cli.Env) error {
	cat := openCatalog()
	if cat == nil {
		return fmt.Errorf("no writable cache directory for the catalog")
	}
	defer cat.Close()

	st := cat.CheckStatus(ctx)
	dim := env.OutEnv.Dim()

	if st.Empty() {
		fmt.Fprintln(env.Out, dim("No catalog yet. Build it with: hoard catalog update"))
		return nil
	}
	fmt.Fprintf(env.Out, "%s cards · %s · %s\n",
		ui.Count(st.Cards), ui.Bytes(st.Bytes), cat.Path())
	fmt.Fprintln(env.Out, dim(fmt.Sprintf("Built %s from Scryfall's %s bundle.",
		st.Built.Local().Format("2 Jan 15:04"),
		st.SourceUpdated.Local().Format("2 Jan 15:04"))))

	switch {
	case !st.Checked:

		fmt.Fprintln(env.Out, dim("Scryfall not consulted this run."))
	case st.Stale:
		fmt.Fprintf(env.Out, "A newer bundle is available (%s). Update with: hoard catalog update\n",
			st.Remote.Local().Format("2 Jan 15:04"))
	default:
		fmt.Fprintln(env.Out, dim("Up to date with Scryfall."))
	}
	return nil
}

func runCatalogUpdate(ctx context.Context, env *cli.Env) error {
	cat := openCatalog()
	if cat == nil {
		return fmt.Errorf("no writable cache directory for the catalog")
	}
	defer cat.Close()

	pr := stderrPrinter()
	res, err := action.CatalogUpdate(ctx, action.Deps{Catalog: cat}, pr.Fn())
	pr.Close()
	if err != nil {
		return err
	}
	env.Report().Progress("Catalog ready: %s cards, %s on disk, built in %s.",
		ui.Count(res.Cards), ui.Bytes(res.Bytes), res.Took)
	return nil
}
