package command

// `hoard guessed`: the audit queue the hands-free scanner's nonfoil default
// creates.

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/store"
)

// NewCmdGuessed builds `hoard guessed`.
func NewCmdGuessed(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "guessed",
		GroupID: groupCollection,
		Short:   "Scanned finishes nothing on the card chose",
		Example: "hoard guessed",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return runGuessed(a.store, a.env)
		},
	}
}

// runGuessed lists every scanned holding still standing on a guessed finish —
// the audit queue the hands-free default creates. A scan with no legible
// marker commits nonfoil rather than stopping the session; this is where
// those rows wait for a human to look at the physical card.
func runGuessed(st *store.Store, env *cli.Env) error {
	rows, err := st.GuessedFinishes()
	if err != nil {
		return err
	}
	dim := env.OutEnv.Dim()
	if len(rows) == 0 {
		fmt.Fprintln(env.Out, dim("No guessed finishes — every scanned row was evidence-backed or has been checked."))
		return nil
	}
	noun := "rows"
	if len(rows) == 1 {
		noun = "row"
	}
	fmt.Fprintf(env.Out, "%d scanned %s committed without finish evidence:\n\n", len(rows), noun)
	for _, r := range rows {
		fmt.Fprintf(env.Out, "  %s (%s/%s) %s · guessed %s\n", r.Name,
			strings.ToUpper(r.Set), r.Number, r.Finish, r.GuessedAt)
	}
	fmt.Fprintln(env.Out)
	fmt.Fprintln(env.Out, dim("Check the card; fix a wrong one in browse (enter → finish), which clears it here."))
	return nil
}
