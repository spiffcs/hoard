package command

// The watch command: standing price thresholds, checked against stored
// prices only — the cron pairing is `hoard update-prices && hoard watch`.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// errWatchFired marks a check where at least one watch crossed its
// threshold, mapped to exit code 3 so a cron wrapper can `hoard watch ||
// notify` without parsing output. The alert itself is the command's stdout;
// this is only the exit status.
var errWatchFired = fmt.Errorf("a watch fired")

func watchAdd(ctx context.Context, st *store.Store, env *cli.Env, words []string, under, over float64, foil bool) error {
	name := strings.TrimSpace(strings.Join(words, " "))
	if name == "" {
		return cli.Usagef("watch add needs a card name, e.g. hoard watch add Sol Ring --under 2")
	}
	if (under > 0) == (over > 0) {
		return cli.Usagef("choose exactly one of --under N or --over N")
	}
	op, threshold := "under", under
	if over > 0 {
		op, threshold = "over", over
	}
	pr := stderrPrinter()
	res, err := action.WatchAdd(ctx, addDeps(st), pr.Fn(),
		action.WatchAddOptions{Name: name, Foil: foil, Op: op, Threshold: threshold})
	pr.Close()
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "Watching %s (%s) %s: %s %s.\n",
		res.Card.Name, ui.Printing(res.Card.Set, res.Card.CollectorNumber),
		res.Finish, op, ui.Money(threshold))
	fmt.Fprintln(env.Out, env.OutEnv.Dim()("Checks read stored prices: hoard update-prices && hoard watch"))
	return nil
}

func watchImport(ctx context.Context, st *store.Store, env *cli.Env, pos []string) error {
	if len(pos) != 1 {
		return cli.Usagef("watch import needs exactly one file (CSV or JSON)")
	}
	data, err := os.ReadFile(pos[0])
	if err != nil {
		return err
	}
	pr := stderrPrinter()
	res, err := action.WatchImport(ctx, addDeps(st), pr.Fn(),
		action.WatchImportOptions{Data: data, Display: pos[0]})
	pr.Close()
	// A partial import still stood its watches; the report renders before
	// the exit code says "done, mostly". Any other error did not finish.
	if err != nil && !errors.Is(err, action.ErrPartial) {
		return err
	}

	r := &ui.Report{Out: env.Out, Err: env.Err, OutEnv: env.OutEnv, ErrEnv: env.ErrEnv}
	r.Result("Imported %d watches: %d new, %d adjusted.", res.Created+res.Updated, res.Created, res.Updated)
	if res.Refinished > 0 {
		r.Detail("%d watch the foil price: the file said otherwise but the printing has no non-foil.",
			res.Refinished)
	}
	if len(res.Unresolved) > 0 {
		r.Detail("%d cards could not be resolved and were skipped:", len(res.Unresolved))
		for _, u := range res.Unresolved {
			r.Item(u)
		}
	}
	r.Hint("Checks read stored prices: hoard update-prices && hoard watch")
	return err
}

func watchList(st *store.Store, cmdEnv *cli.Env) error {
	watches, err := action.Deps{Store: st}.WatchList()
	if err != nil {
		return err
	}
	out, env := cmdEnv.Out, cmdEnv.OutEnv
	if len(watches) == 0 {
		fmt.Fprintln(out, env.Dim()("No watches. Add one: hoard watch add <name> --under N | --over N"))
		return nil
	}
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "ID", Align: ui.Right, Priority: 2, Style: env.Dim()},
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 12},
			{Title: "SET/NUM", Align: ui.Left, Priority: 3, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "WATCH", Align: ui.Left},
			{Title: "PRICE", Align: ui.Right},
			{Title: "STATE", Align: ui.Left, Style: env.Dim()},
		},
	}
	for _, w := range watches {
		price, state := "", "unpriced"
		if w.PriceUSD != nil {
			price = ui.Money(*w.PriceUSD)
			if w.Met() {
				state = "met"
			} else {
				state = "waiting"
			}
		}
		t.Add(ui.C(fmt.Sprintf("%d", w.ID)), ui.C(w.Name),
			ui.C(ui.Printing(w.SetCode, w.CollectorNumber)), ui.C(ui.Finish(w.Finish)),
			ui.C(fmt.Sprintf("%s %s", w.Op, ui.Money(w.Threshold))),
			ui.C(price), ui.C(state))
	}
	fmt.Fprint(out, t.Render())
	return nil
}

func watchRemove(st *store.Store, env *cli.Env, args []string) error {
	if len(args) != 1 {
		return cli.Usagef("watch rm takes one watch (an id or a name fragment)")
	}
	w, err := action.Deps{Store: st}.WatchRemove(args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "Removed the %s %s watch on %s.\n", w.Op, ui.Money(w.Threshold), w.Display)
	return nil
}

func watchCheck(st *store.Store, cmdEnv *cli.Env) error {
	fired, checked, err := action.Deps{Store: st}.WatchCheck()
	if err != nil {
		return err
	}
	out, env := cmdEnv.Out, cmdEnv.OutEnv
	if cmdEnv.JSON {
		if err := hoardjson.Write(out, hoardjson.FromWatchCheck(checked, fired)); err != nil {
			return err
		}
		if len(fired) > 0 {
			return errWatchFired
		}
		return nil
	}

	if checked == 0 {
		fmt.Fprintln(out, env.Dim()("No watches to check. Add one: hoard watch add <name> --under N | --over N"))
		return nil
	}
	for _, w := range fired {
		// The finish is only worth saying when it is the notable one, as the
		// tables do with their dimmed '-' for nonfoil.
		finish := ""
		if w.Finish == "foil" {
			finish = " foil"
		}
		fmt.Fprintf(out, "%s (%s)%s is %s, crossed %s %s\n",
			w.Name, ui.Printing(w.SetCode, w.CollectorNumber), finish,
			env.Bold()(ui.Money(*w.PriceUSD)), w.Op, ui.Money(w.Threshold))
	}
	if len(fired) == 0 {
		fmt.Fprintln(out, env.Dim()(fmt.Sprintf("%s watches checked, nothing crossed.", ui.Count(checked))))
		return nil
	}
	fmt.Fprintln(out, env.Dim()(fmt.Sprintf("%s watches checked, %s fired.",
		ui.Count(checked), ui.Count(len(fired)))))
	return errWatchFired
}

// NewCmdWatch is the group whose bare form does the work: `hoard watch` checks,
// `hoard watch list` lists.
//
// The --json split that cmdWatch enforced by hand is now structural: the group
// is JSONCapable and none of its subcommands are.
func NewCmdWatch(a *app) *cobra.Command {
	var under, over float64
	var foil bool

	cmd := &cobra.Command{
		Use:     "watch",
		GroupID: groupCollection,
		Short:   "Check price watches (no network; exit 3 = fired)",
		Example: "hoard watch\nhoard watch [add|import|list|rm]",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return watchCheck(a.store, a.env)
		},
	}

	add := &cobra.Command{
		Use:   "add NAME...",
		Short: "Alert when a price crosses a threshold",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return watchAdd(c.Context(), a.store, a.env, args, under, over, foil)
		},
	}
	add.Flags().Float64Var(&under, "under", 0, "alert when the price falls below this")
	add.Flags().Float64Var(&over, "over", 0, "alert when the price rises above this")
	add.Flags().BoolVar(&foil, "foil", false, "watch the foil price")

	cmd.AddCommand(
		add,
		&cobra.Command{
			Use: "import FILE", Short: "Import price watches in bulk (CSV or JSON)",
			Args: cobra.ExactArgs(1),
			RunE: func(c *cobra.Command, args []string) error {
				return watchImport(c.Context(), a.store, a.env, args)
			},
		},
		&cobra.Command{
			Use: "list", Short: "Your watches",
			Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error { return watchList(a.store, a.env) },
		},
		&cobra.Command{
			Use: "rm ID|NAME", Short: "Remove a watch",
			Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error { return watchRemove(a.store, a.env, args) },
		},
	)
	return cli.JSONCapable(cmd)
}
