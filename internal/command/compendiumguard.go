package command

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/browse"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/store"
)

func refuseIfCompendium(cmd *cobra.Command, st *store.Store) error {
	if st == nil || !st.CompendiumMode() || !cli.Has(cmd, cli.AnnotationMutating) {
		return nil
	}
	return fmt.Errorf(
		"%s writes to the database, and this one is a compendium, not your collection.\n"+
			"Point --db or $HOARD_DB at your own hoard, or rebuild the compendium with the generator",
		cmd.CommandPath())
}

func readOnlyIfCompendium(st *store.Store) browse.Option {
	if st == nil || !st.CompendiumMode() {
		return func(*browse.Model) {}
	}
	return browse.WithReadOnly()
}
