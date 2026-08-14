package command

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/store"
)

func NewCmdGuessed(a *app) *cobra.Command {
	var checked []int64

	cmd := &cobra.Command{
		Use:     "guessed",
		GroupID: groupCollection,
		Short:   "Scanned finishes nothing on the card chose",

		Long: "Rows the hands-free scanner committed on a default rather\n" +
			"than on evidence. Check the physical card, then retire the\n" +
			"row either way: correct a wrong finish in browse (enter,\n" +
			"then finish), or confirm a right one with --checked.",
		Example: "hoard guessed\n" +
			"hoard guessed --checked 12 --checked 13",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if len(checked) > 0 {

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

	cmd.Flags().Lookup("checked").DefValue = ""

	return cli.JSONCapable(cmd)
}

func runGuessed(st *store.Store, env *cli.Env) error {
	rows, err := st.GuessedFinishes()
	if err != nil {
		return err
	}

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

	fmt.Fprintln(env.Out, dim("Check the card. Fix a wrong one in browse (enter → finish);"))
	fmt.Fprintln(env.Out, dim("confirm a right one with hoard guessed --checked <id>. Either retires it."))
	return nil
}

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
