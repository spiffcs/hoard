package command

import (
	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func NewCmdVacuum(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "vacuum",
		GroupID: groupCollection,
		Short:   "Delete orphaned printings nothing holds or watches",
		Example: "hoard vacuum",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return runVacuum(a.store, a.env)
		},
	}
}

func runVacuum(st *store.Store, env *cli.Env) error {
	removed, err := st.VacuumPrintings()
	if err != nil {
		return err
	}
	r := env.Report()
	if removed == 0 {
		r.Success("No orphaned printings; nothing to remove.")
		return nil
	}
	r.Success("Removed %s orphaned printings and their history; database compacted.", ui.Count(removed))
	return nil
}
