package command

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

var errWatchFired = fmt.Errorf("a watch fired")

type watchFlags struct {
	under, over      float64
	drop, rise       string
	minMove          float64
	since            string
	foil, minMoveSet bool
}

func parsePercent(flag, raw string) (float64, error) {
	pct, err := store.ParsePercent(flag, raw)
	if err != nil {
		return 0, cli.Usagef("--%s %v", flag, err)
	}
	return pct, nil
}

func watchAdd(ctx context.Context, st *store.Store, env *cli.Env, words []string, fl watchFlags) error {
	name := strings.TrimSpace(strings.Join(words, " "))
	if name == "" {
		return cli.Usagef("watch add needs a card name, e.g. hoard watch add Sol Ring --under 2")
	}

	var bounds []action.WatchBound
	if fl.under > 0 {
		bounds = append(bounds, action.WatchBound{Op: "under", Threshold: fl.under})
	}
	if fl.over > 0 {
		bounds = append(bounds, action.WatchBound{Op: "over", Threshold: fl.over})
	}
	windowDays := store.DefaultWindowDays
	if fl.since != "" {
		w, err := parseWindow(fl.since)
		if err != nil {
			return err
		}

		if windowDays = int(w / (24 * time.Hour)); windowDays < 1 {
			return cli.Usagef("--since %s is less than a day; a movement needs a window to move in", fl.since)
		}
	}
	for _, m := range []struct{ flag, raw string }{{"drop", fl.drop}, {"rise", fl.rise}} {
		if m.raw == "" {
			continue
		}
		pct, err := parsePercent(m.flag, m.raw)
		if err != nil {
			return err
		}
		bounds = append(bounds, action.WatchBound{
			Op: m.flag, Pct: pct, MinMove: fl.minMove, WindowDays: windowDays})
	}
	if len(bounds) == 0 {
		return cli.Usagef("watch add needs a threshold or a movement: " +
			"--under N, --over N, --drop 10%%, --rise 10%%")
	}
	if (fl.drop != "" || fl.rise != "") && (fl.under > 0 || fl.over > 0) {
		return cli.Usagef("a movement and a dollar line are two different questions " +
			"about one card: give each its own watch add, so watch rm can take one without the other")
	}

	if fl.under > 0 && fl.over > 0 && fl.under >= fl.over {
		return cli.Usagef("a band alerts outside it, so --under must be below --over: "+
			"under %s and over %s leaves no gap between them and every price is one or the other",
			ui.Money(fl.under), ui.Money(fl.over))
	}

	pr := stderrPrinter()
	res, err := action.WatchAdd(ctx, addDeps(st), pr.Fn(),
		action.WatchAddOptions{Name: name, Foil: fl.foil, Bounds: bounds})
	pr.Close()
	if err != nil {
		if res.Stood > 0 {

			return fmt.Errorf("the %s watch stood; the %s watch did not: %w",
				bounds[res.Stood-1].Op, bounds[res.Stood].Op, err)
		}
		return err
	}

	parts := make([]string, 0, len(bounds))
	for _, b := range bounds {
		parts = append(parts, watchRule(b))
	}
	fmt.Fprintf(env.Out, "Watching %s (%s) %s: %s.\n",
		res.Card.Name, ui.Printing(res.Card.Set, res.Card.CollectorNumber),
		res.Finish, strings.Join(parts, ", "))
	watchPrintingNotes(env, res)
	if len(bounds) > 1 {
		fmt.Fprintln(env.Out, env.OutEnv.Dim()(
			"Two watches, one per direction: list and rm take them one at a time."))
	}
	if fl.drop != "" || fl.rise != "" {
		watchPercentNotes(st, env, res, fl, windowDays)
	}
	fmt.Fprintln(env.Out, env.OutEnv.Dim()("Checks read stored prices: hoard update-prices && hoard watch"))
	return nil
}

func watchPrintingNotes(env *cli.Env, res action.WatchAddResult) {
	dim := env.OutEnv.Dim()
	say := func(format string, a ...any) {
		fmt.Fprintln(env.Out, dim(fmt.Sprintf(format, a...)))
	}
	switch {
	case len(res.Held) == 0:
		say("You hold no copy of %s: this is the printing Scryfall names for the bare "+
			"name, not one of yours.", res.Card.Name)
	case !res.Owned:

		say("Scryfall did not answer for the printing you hold (%s), so this watch "+
			"follows its own choice of printing instead.", heldPrintings(res.Held, ""))
	case len(res.Held) > 1:

		say("You hold %d printings of %s; watching the one you hold most of (%s).",
			len(res.Held), res.Card.Name, ui.Qty(heldAny(res.Held, res.Card.ID)))
		say("Also held: %s.", heldPrintings(res.Held, res.Card.ID))
		say("To watch one of those instead, name it by set and collector " +
			"number through hoard watch import.")
	case heldCopies(res.Held, res.Card.ID) == 0:
		say("Watching a printing you hold, though no copy of it is %s.", res.Finish)
	}
}

func heldPrintings(held []store.HeldPrinting, skipID string) string {
	parts := make([]string, 0, len(held))
	for _, h := range held {
		if h.ScryfallID == skipID {
			continue
		}
		parts = append(parts, ui.Printing(h.SetCode, h.CollectorNumber)+" "+ui.Qty(h.AnyCopies))
	}
	return strings.Join(parts, ", ")
}

func heldCopies(held []store.HeldPrinting, id string) int {
	for _, h := range held {
		if h.ScryfallID == id {
			return h.Copies
		}
	}
	return 0
}

func heldAny(held []store.HeldPrinting, id string) int {
	for _, h := range held {
		if h.ScryfallID == id {
			return h.AnyCopies
		}
	}
	return 0
}

func watchRule(b action.WatchBound) string {
	if b.Op == "drop" || b.Op == "rise" {
		s := fmt.Sprintf("%s %s", b.Op, ui.Percent(b.Pct))
		if b.MinMove > 0 {
			s += " ≥" + ui.Money(b.MinMove)
		}
		return s
	}
	return fmt.Sprintf("%s %s", b.Op, ui.Money(b.Threshold))
}

func watchPercentNotes(st *store.Store, env *cli.Env, res action.WatchAddResult, fl watchFlags, windowDays int) {
	dim := env.OutEnv.Dim()

	fmt.Fprintln(env.Out, dim(
		"Movement is measured from today onward: a fall that already happened is not an alert."))
	ok, err := action.Deps{Store: st}.WatchAnchorable(res.Card.ID, res.Finish)
	if err == nil && !ok {
		fmt.Fprintln(env.Out, dim(fmt.Sprintf(
			"No price history yet from whichever source prices this printing, so the movement "+
				"has nothing to measure from. Run hoard update-prices; until the record "+
				"reaches back %d days the watch shows as waiting.",
			windowDays)))
	}
	price := res.Card.PriceUSD
	if res.Finish == finish.Foil {
		price = res.Card.PriceUSDFoil
	}
	if !fl.minMoveSet && price != nil && *price < 1 {
		fmt.Fprintln(env.Out, dim(fmt.Sprintf(
			"At %s, ten percent is %s. Cards this cheap change by that much routinely — "+
				"--min-move sets a floor in dollars if the alerts are too many.",
			ui.Money(*price), ui.Money(*price/10))))
	}
}

func watchImport(ctx context.Context, st *store.Store, env *cli.Env, pos []string) error {
	if len(pos) != 1 {
		return cli.Usagef("watch import needs exactly one file (CSV or JSON)")
	}

	data, display, err := readPathOrStdin(pos[0])
	if err != nil {
		return err
	}
	pr := stderrPrinter()
	res, err := action.WatchImport(ctx, addDeps(st), pr.Fn(),
		action.WatchImportOptions{Data: data, Display: display})
	pr.Close()

	if err != nil && !errors.Is(err, action.ErrPartial) {
		return err
	}

	r := &ui.Report{Out: env.Out, Err: env.Err, OutEnv: env.OutEnv, ErrEnv: env.ErrEnv}
	r.Result("Imported %d watches: %d new, %d adjusted.", res.Created+res.Updated, res.Created, res.Updated)
	if res.Refinished > 0 {
		r.Detail("%d watch the foil price: the file said otherwise but the printing has no non-foil.",
			res.Refinished)
	}

	if res.Held > 0 {
		r.Detail("%d named only a card and follow a printing you hold; "+
			"give a Set and Collector Number to pin a different one.", res.Held)
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

	if cmdEnv.JSON {
		return hoardjson.Write(out, hoardjson.FromWatchList(watches))
	}
	if len(watches) == 0 {
		fmt.Fprintln(out, env.Dim()("No watches. Add one: hoard watch add <name> --under N | --over N"))
		return nil
	}

	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 12},
			{Title: "SET/NUM", Align: ui.Left, Priority: 3, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "WATCH", Align: ui.Left},

			{Title: "ANCHOR", Align: ui.Right, Priority: 5, Style: env.Dim()},
			{Title: "PRICE", Align: ui.Right},
			{Title: "STATE", Align: ui.Left, Style: env.Dim()},
		},
	}
	for _, w := range watches {
		price := ""
		if w.PriceUSD != nil {
			price = ui.Money(*w.PriceUSD)
		}

		state := strings.ReplaceAll(w.State(), "-", " ")
		anchor := "—"
		if w.Anchor != nil {
			anchor = ui.Money(*w.Anchor)
		}
		t.Add(ui.C(w.Name),
			ui.C(ui.Printing(w.SetCode, w.CollectorNumber)), ui.C(ui.Finish(w.Finish)),
			ui.C(watchCell(w)), ui.C(anchor), ui.C(price), ui.C(state))
	}
	fmt.Fprint(out, t.Render())
	return nil
}

func watchCell(w store.WatchStatus) string {
	if !w.Percent() {
		return fmt.Sprintf("%s %s", w.Op, ui.Money(w.Threshold))
	}
	s := fmt.Sprintf("%s %s", w.Op, ui.Percent(w.Pct))
	if w.MinMove > 0 {
		s += " ≥" + ui.Money(w.MinMove)
	}
	return s
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

		label := ""
		if w.Finish == finish.Foil {
			label = " foil"
		}
		fmt.Fprintf(out, "%s\n", watchAlert(env, w, label))
	}
	if len(fired) == 0 {
		fmt.Fprintln(out, env.Dim()(fmt.Sprintf("%s watches checked, nothing crossed.", ui.Count(checked))))
		return nil
	}
	fmt.Fprintln(out, env.Dim()(fmt.Sprintf("%s watches checked, %s fired.",
		ui.Count(checked), ui.Count(len(fired)))))
	return errWatchFired
}

func watchAlert(env ui.Env, w store.WatchStatus, label string) string {
	where := ui.Printing(w.SetCode, w.CollectorNumber)
	if !w.Percent() || w.Anchor == nil {
		return fmt.Sprintf("%s (%s)%s is %s, crossed %s %s",
			w.Name, where, label, env.Bold()(ui.Money(*w.PriceUSD)), w.Op, ui.Money(w.Threshold))
	}
	word, extreme := "down", "high"
	moved := (*w.Anchor - *w.PriceUSD) / *w.Anchor
	if w.Op == "rise" {
		word, extreme = "up", "low"
		moved = (*w.PriceUSD - *w.Anchor) / *w.Anchor
	}
	since := ""
	if t, err := time.Parse(time.RFC3339, w.AnchorAt); err == nil {
		since = " of " + t.Local().Format("2 Jan")
	}
	return fmt.Sprintf("%s (%s)%s is %s, %s %s from its %s %s%s",
		w.Name, where, label, ui.Money(*w.PriceUSD),
		word, env.Bold()(ui.Percent(moved)), ui.Money(*w.Anchor), extreme, since)
}

func NewCmdWatch(a *app) *cobra.Command {
	var fl watchFlags

	cmd := &cobra.Command{
		Use:     "watch",
		GroupID: groupCollection,
		Short:   "Check price watches (no network; exit 3 = fired)",

		Long: "Checks every standing watch against stored prices —\n" +
			"no network, so the cron pairing is\n" +
			"hoard update-prices && hoard watch.\n\n" +
			"Exit 3 means something crossed on this run. Exit 0\n" +
			"does not mean nothing is crossed: an alert fires once\n" +
			"per crossing and the watch then holds at \"met\", so a\n" +
			"loop reading exit codes alone will read a standing\n" +
			"alert as a threshold no longer met. Ask\n" +
			"hoard watch list --json instead — every row carries\n" +
			"its state and whether the next check would fire.\n\n" +
			"What re-arms a held watch depends on its rule:\n\n" +
			"  under, over  a check taken while the price is back\n" +
			"               outside the threshold, or re-adding\n" +
			"               the watch, which clears the state\n" +
			"               whatever the price is doing.\n" +
			"  drop, rise   firing itself. The alert re-anchors\n" +
			"               the movement to the price that fired,\n" +
			"               so the watch is armed again at that\n" +
			"               level and speaks on a further move of\n" +
			"               its own size.",
		Example: "hoard watch\nhoard watch --json\nhoard watch [add|import|list|rm]",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return watchCheck(a.store, a.env)
		},
	}

	add := &cobra.Command{
		Use:   "add NAME...",
		Short: "Alert when a price crosses a threshold or moves",
		Example: "hoard watch add Sol Ring --under 2\n" +
			"hoard watch add Sol Ring --foil --over 30\n" +
			"hoard watch add Sol Ring --under 1 --over 5\n" +
			"hoard watch add Ancient Tomb --foil --drop 10%\n" +
			"hoard watch add Ancient Tomb --rise 10% --since 60d",
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			fl.minMoveSet = c.Flags().Changed("min-move")
			return watchAdd(c.Context(), a.store, a.env, args, fl)
		},
	}
	add.Flags().Float64Var(&fl.under, "under", 0, "alert when the price falls below this")
	add.Flags().Float64Var(&fl.over, "over", 0,
		"alert when the price rises above this (with --under: a band)")

	add.Flags().StringVar(&fl.drop, "drop", "",
		"alert on a fall of this much from the window's high, e.g. 10%")
	add.Flags().StringVar(&fl.rise, "rise", "",
		"alert on a rise of this much from the window's low, e.g. 10%")
	add.Flags().Float64Var(&fl.minMove, "min-move", store.DefaultMinMove,
		"smallest movement worth alerting on, in dollars")
	add.Flags().StringVar(&fl.since, "since", "",
		"how far back a movement is measured, e.g. 30d (default 30d)")
	add.Flags().BoolVar(&fl.foil, "foil", false, "watch the foil price")

	cmd.AddCommand(
		add,
		&cobra.Command{
			Use: "import FILE|-", Short: "Import price watches in bulk (CSV or JSON)",
			Example: "hoard watch import watches.csv\n" +
				"pbpaste | hoard watch import -",
			Args: cobra.ExactArgs(1),
			RunE: func(c *cobra.Command, args []string) error {
				return watchImport(c.Context(), a.store, a.env, args)
			},
		},

		cli.JSONCapable(&cobra.Command{
			Use: "list", Short: "Your watches",
			Example: "hoard watch list\nhoard watch list --json",
			Args:    cobra.NoArgs,
			RunE:    func(*cobra.Command, []string) error { return watchList(a.store, a.env) },
		}),
		&cobra.Command{
			Use: "rm ID|NAME", Short: "Remove a watch",
			Args: cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error { return watchRemove(a.store, a.env, args) },
		},
	)
	return cli.JSONCapable(cmd)
}
