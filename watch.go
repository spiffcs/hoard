package main

// The watch command: standing price thresholds, checked against stored
// prices only — the cron pairing is `hoard update-prices && hoard watch`.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// errWatchFired marks a check where at least one watch crossed its
// threshold, mapped to exit code 3 so a cron wrapper can `hoard watch ||
// notify` without parsing output. The alert itself is the command's stdout;
// this is only the exit status.
var errWatchFired = fmt.Errorf("a watch fired")

func cmdWatch(ctx context.Context, st *store.Store, args []string, jsonOut bool) error {
	if len(args) > 0 {
		sub, rest := args[0], args[1:]
		// The subcommands write or configure; only the bare check emits JSON.
		if jsonOut {
			return fmt.Errorf("watch %s has no JSON output; --json works on the bare check: hoard watch --json", sub)
		}
		switch sub {
		case "add":
			return watchAdd(ctx, st, rest)
		case "list":
			return watchList(st)
		case "rm":
			return watchRemove(st, rest)
		default:
			return fmt.Errorf("unknown watch subcommand %q (want add, list, or rm)", sub)
		}
	}
	return watchCheck(st, jsonOut)
}

func watchAdd(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("watch add", flag.ContinueOnError)
	under := fs.Float64("under", 0, "alert when the price falls below this")
	over := fs.Float64("over", 0, "alert when the price rises above this")
	foil := fs.Bool("foil", false, "watch the foil price")
	words, err := parsePositionals(fs, args)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(strings.Join(words, " "))
	if name == "" {
		return fmt.Errorf("watch add needs a card name, e.g. hoard watch add Sol Ring --under 2")
	}
	if (*under > 0) == (*over > 0) {
		return fmt.Errorf("choose exactly one of --under N or --over N")
	}
	op, threshold := "under", *under
	if *over > 0 {
		op, threshold = "over", *over
	}
	pr := stderrPrinter()
	res, err := action.WatchAdd(ctx, addDeps(st), pr.Fn(),
		action.WatchAddOptions{Name: name, Foil: *foil, Op: op, Threshold: threshold})
	pr.Close()
	if err != nil {
		return err
	}
	env := ui.Detect(os.Stdout)
	fmt.Printf("Watching %s (%s) %s: %s %s.\n",
		res.Card.Name, ui.Printing(res.Card.Set, res.Card.CollectorNumber),
		res.Finish, op, ui.Money(threshold))
	fmt.Println(env.Dim()("Checks read stored prices: hoard update-prices && hoard watch"))
	return nil
}

func watchList(st *store.Store) error {
	watches, err := action.Deps{Store: st}.WatchList()
	if err != nil {
		return err
	}
	env := ui.Detect(os.Stdout)
	if len(watches) == 0 {
		fmt.Println(env.Dim()("No watches. Add one: hoard watch add <name> --under N | --over N"))
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
	fmt.Print(t.Render())
	return nil
}

func watchRemove(st *store.Store, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("watch rm takes one watch (an id or a name fragment)")
	}
	w, err := action.Deps{Store: st}.WatchRemove(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("Removed the %s %s watch on %s.\n", w.Op, ui.Money(w.Threshold), w.Display)
	return nil
}

func watchCheck(st *store.Store, jsonOut bool) error {
	fired, checked, err := action.Deps{Store: st}.WatchCheck()
	if err != nil {
		return err
	}
	if jsonOut {
		if err := hoardjson.Write(os.Stdout, hoardjson.FromWatchCheck(checked, fired)); err != nil {
			return err
		}
		if len(fired) > 0 {
			return errWatchFired
		}
		return nil
	}

	env := ui.Detect(os.Stdout)
	if checked == 0 {
		fmt.Println(env.Dim()("No watches to check. Add one: hoard watch add <name> --under N | --over N"))
		return nil
	}
	for _, w := range fired {
		// The finish is only worth saying when it is the notable one, as the
		// tables do with their dimmed '-' for nonfoil.
		finish := ""
		if w.Finish == "foil" {
			finish = " foil"
		}
		fmt.Printf("%s (%s)%s is %s, crossed %s %s\n",
			w.Name, ui.Printing(w.SetCode, w.CollectorNumber), finish,
			env.Bold()(ui.Money(*w.PriceUSD)), w.Op, ui.Money(w.Threshold))
	}
	if len(fired) == 0 {
		fmt.Println(env.Dim()(fmt.Sprintf("%s watches checked, nothing crossed.", ui.Count(checked))))
		return nil
	}
	fmt.Println(env.Dim()(fmt.Sprintf("%s watches checked, %s fired.",
		ui.Count(checked), ui.Count(len(fired)))))
	return errWatchFired
}
