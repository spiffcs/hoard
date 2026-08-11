package command

// `hoard guessed`: the audit queue the hands-free scanner's nonfoil default
// creates.

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/store"
)

// NewCmdGuessed builds `hoard guessed`.
func NewCmdGuessed(a *app) *cobra.Command {
	var checked []int64

	cmd := &cobra.Command{
		Use:     "guessed",
		GroupID: groupCollection,
		Short:   "Scanned finishes nothing on the card chose",
		// Wrapped to fit 60 columns, the narrowest terminal hoard's help
		// claims to render in: writeCommandHelp copies Long through
		// verbatim.
		Long: "Rows the hands-free scanner committed on a default rather\n" +
			"than on evidence. Check the physical card, then retire the\n" +
			"row either way: correct a wrong finish in browse (enter,\n" +
			"then finish), or confirm a right one with --checked.",
		Example: "hoard guessed\n" +
			"hoard guessed --checked 12 --checked 13",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if len(checked) > 0 {
				// --checked retires rows and reports what it did, including
				// which ids named nothing. That is an outcome, not a queue,
				// and the document model has no kind for it — so rather than
				// print prose from a command whose help advertises --json,
				// say which half of the command the flag belongs to.
				if a.env.JSON {
					return cli.Usagef("hoard guessed --checked has no JSON output; " +
						"retire without --json, then hoard guessed --json for what remains")
				}
				return runGuessedChecked(a.store, a.env, checked)
			}
			return runGuessed(a.store, a.env)
		},
	}
	cmd.Flags().Int64SliceVar(&checked, "checked", nil,
		"retire guesses by `id`: you checked the card and the finish was right")
	// The first slice flag in the tree, and pflag's defaults for one read
	// badly in help: the placeholder is its Go type name, and an empty slice
	// prints as a default worth stating. The back-quoted word above replaces
	// the first; clearing DefValue drops the second.
	cmd.Flags().Lookup("checked").DefValue = ""
	// The listing is JSONCapable because the ids it prints are the arguments
	// --checked takes: a worklist whose only machine-readable form is a table
	// can be read but not worked through.
	return cli.JSONCapable(cmd)
}

// runGuessed lists every scanned holding still standing on a guessed finish —
// the audit queue the hands-free default creates. A scan with no legible
// marker commits nonfoil rather than stopping the session; this is where
// those rows wait for a human to look at the physical card.
//
// Each row leads with its id because a row has to be addressable to be
// retired, and the natural key cannot address one: the log is per commit, so
// two copies of one printing sit here as two rows that are identical in every
// column. The id is what --checked takes.
func runGuessed(st *store.Store, env *cli.Env) error {
	rows, err := st.GuessedFinishes()
	if err != nil {
		return err
	}
	// Before the empty check, not after: an empty queue is a real answer for a
	// script working the list down, and the prose branch below says the same
	// thing in a sentence a consumer cannot read.
	if env.JSON {
		return hoardjson.Write(env.Out, hoardjson.FromGuessed(rows))
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
		fmt.Fprintf(env.Out, "  %s %s (%s/%s) %s · guessed %s\n", dim(fmt.Sprintf("#%d", r.ID)),
			r.Name, strings.ToUpper(r.Set), r.Number, r.Finish, r.GuessedAt)
	}
	fmt.Fprintln(env.Out)
	// Both ways out, because for most of these rows the scanner was right and
	// the correction path never fires. Naming only the correction is what made
	// the list unable to shrink.
	fmt.Fprintln(env.Out, dim("Check the card. Fix a wrong one in browse (enter → finish);"))
	fmt.Fprintln(env.Out, dim("confirm a right one with hoard guessed --checked <id>. Either retires it."))
	return nil
}

// runGuessedChecked retires the named guesses: the cards were checked and the
// scanner's default was right.
//
// An id that names nothing is reported rather than passed over, because the
// ids come off a listing the user is reading and a typo that silently
// succeeded would leave them believing a row was retired when it is still
// queued. It warns rather than failing: retiring the same row twice is a
// repeated command, not a mistake worth a non-zero exit.
func runGuessedChecked(st *store.Store, env *cli.Env, ids []int64) error {
	var retired int
	var missing []int64
	for _, id := range ids {
		ok, err := st.ConfirmFinishGuess(id)
		if err != nil {
			return err
		}
		if ok {
			retired++
			continue
		}
		missing = append(missing, id)
	}
	r := env.Report()
	noun := "guesses"
	if retired == 1 {
		noun = "guess"
	}
	r.Result("Retired %d %s: the finish was right as scanned.", retired, noun)
	for _, id := range missing {
		r.Warn("No guess #%d — already retired, or never recorded.", id)
	}
	return nil
}
