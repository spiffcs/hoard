package command

import (
	"context"
	"errors"
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tui"
	"github.com/spiffcs/hoard/internal/ui"
)

type addOpts struct {
	foil      bool
	foilSet   bool
	qty       int
	qtySet    bool
	file      string
	binderRef string
	again     bool
}

func NewCmdAdd(a *app) *cobra.Command {
	var o addOpts

	cmd := &cobra.Command{
		Use:     "add [NAME... | URL]",
		GroupID: groupCollection,
		Short:   "Add cards by name, link, or list",
		Example: "hoard add\n" +
			"hoard add <scryfall-url> [--foil] [--qty N]\n" +
			"hoard add --file LIST | - [--binder B] [--again]",

		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			o.qtySet = c.Flags().Changed("qty")
			o.foilSet = c.Flags().Changed("foil")
			return runAdd(c.Context(), a.store, a.env, args, o)
		},
	}
	cmd.Flags().BoolVar(&o.foil, "foil", false, "add the card as foil (URL form only)")
	cmd.Flags().IntVar(&o.qty, "qty", 1, "quantity to add (URL form only)")
	cmd.Flags().StringVar(&o.file, "file", "",
		"add a pasted/exported card list (a path, or - for stdin)")
	cmd.Flags().StringVar(&o.binderRef, "binder", "",
		"destination binder for URL and list adds (id, name, or unique fragment)")
	cmd.Flags().BoolVar(&o.again, "again", false,
		"add a list this hoard has already added, doubling its cards on purpose")
	return cli.Mutating(cmd)
}

func runAdd(ctx context.Context, st *store.Store, env *cli.Env, args []string, o addOpts) error {
	if o.qty < 1 {
		return cli.Usagef("--qty must be at least 1")
	}

	if o.file == "" && len(args) == 0 && !stdinIsTTY() {
		o.file = "-"
	}
	if o.file != "" {
		if len(args) > 0 {
			return cli.Usagef("--file takes the whole list; drop the card name arguments")
		}

		if err := refusePerCopyFlags(o,
			"a list names a quantity and a finish on every line"); err != nil {
			return err
		}
		return addFromList(ctx, st, env, o.file, o.binderRef, o.again)
	}

	if len(args) == 1 && looksLikeURL(args[0]) {
		return addByURL(ctx, st, env, args[0], o.foil, o.qty, o.binderRef)
	}
	if o.binderRef != "" {

		return cli.Usagef("--binder applies to URL and --file adds; " +
			"the interactive picker chooses destinations itself")
	}

	if err := refusePerCopyFlags(o,
		"the interactive picker asks for both, per card"); err != nil {
		return err
	}
	return addByName(ctx, st, strings.Join(args, " "))
}

func refusePerCopyFlags(o addOpts, because string) error {
	var named []string
	if o.qtySet {
		named = append(named, "--qty")
	}
	if o.foilSet {
		named = append(named, "--foil")
	}
	if len(named) == 0 {
		return nil
	}

	verb := "applies"
	if len(named) > 1 {
		verb = "apply"
	}
	return cli.Usagef("%s %s to the URL form of add; %s",
		strings.Join(named, " and "), verb, because)
}

func looksLikeURL(arg string) bool {
	return strings.Contains(arg, "://") || strings.Contains(arg, "scryfall.com")
}

func addByURL(ctx context.Context, st *store.Store, env *cli.Env, url string, foil bool, qty int, binderRef string) error {
	pr := stderrPrinter()
	res, err := action.AddCardByURL(ctx, addDeps(st), pr.Fn(),
		action.AddCardByURLOptions{URL: url, Foil: foil, Qty: qty, BinderRef: binderRef})
	pr.Close()
	if err != nil {
		return err
	}
	env.Report().Success("Added %d× %s (%s/%s) as %s into %s · %s",
		qty, res.Card.Name, res.Card.Set, res.Card.CollectorNumber,
		res.Finish, res.Binder, ui.MoneyPtr(res.PriceUSD))
	return nil
}

func addDeps(st *store.Store) action.Deps {
	return action.Deps{Store: st, CacheDir: pricing.DefaultCacheDir(), Resolver: cardResolver}
}

func addFromList(ctx context.Context, st *store.Store, env *cli.Env, path, binderRef string, again bool) error {
	data, display, err := readPathOrStdin(path)
	if err != nil {
		return err
	}
	return addList(ctx, st, env, data, display, binderRef, again)
}

func addList(ctx context.Context, st *store.Store, env *cli.Env, data []byte, display, binderRef string, again bool) error {
	pr := stderrPrinter()
	res, err := action.AddList(ctx, addDeps(st), pr.Fn(),
		action.AddListOptions{Data: data, Display: display, BinderRef: binderRef, Again: again})
	pr.Close()

	if err != nil && !errors.Is(err, action.ErrPartial) {
		return err
	}
	r := env.Report()
	r.Result("Added %s into %s (%s resolved).",
		ui.Plural(res.Copies, "card", "cards"), res.Binder,
		ui.Plural(res.Resolved, "line", "lines"))
	if res.Refinished > 0 {
		r.Detail("%d recorded as foil: the list said otherwise but the printing has no non-foil.",
			res.Refinished)
	}
	for _, s := range res.Skipped {
		r.Detail("Skipped %s", s)
	}
	if len(res.Unresolved) > 0 {
		r.Detail("%s", unresolvedHeading(len(res.Unresolved)))
		for _, u := range res.Unresolved {
			r.Item(u)
		}
	}
	return err
}

func storeAdder(st *store.Store) tui.Adder {
	return func(res tui.Result) error {

		if res.ReplacesFinish != (finish.Finish{}) && res.ReplacesFinish != res.Finish {

			if res.ContainerID != 0 {

				_, err := st.MoveEntryFinish(store.EntryRef{
					ContainerID: res.ContainerID, ScryfallID: res.Card.ID,
					Finish: res.ReplacesFinish, Condition: store.ConditionUnknown,
					Board: store.BoardMain}, res.Finish)
				return err
			}
			_, err := st.MoveCardFinish(res.Card.ID, res.ReplacesFinish, res.Finish)
			return err
		}
		add := func() error {
			if res.ContainerID != 0 {
				return st.AddCardFinishTo(res.ContainerID, res.Card, res.Finish, res.Qty)
			}
			return st.AddCardFinish(res.Card, res.Finish, res.Qty)
		}
		if err := add(); err != nil {
			return err
		}

		if res.FinishGuessed {
			return st.RecordFinishGuess(res.ContainerID, res.Card.ID, res.Finish)
		}
		return nil
	}
}

func addByName(ctx context.Context, st *store.Store, name string) error {
	if !stdinIsTTY() {
		return fmt.Errorf("adding by name needs an interactive terminal; " +
			"pass a Scryfall URL instead (e.g. hoard add https://scryfall.com/card/uma/7/...)")
	}
	dests, err := destinations(st)
	if err != nil {
		return err
	}

	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	sum, err := tui.Run(ctx, newSearcher(cat), storeAdder(st), linkScanner{}, name, dests)
	if err != nil {
		return err
	}
	printScanSummary(sum)
	return nil
}

func printScanSummary(sum tui.Summary) {
	if len(sum.Entries) == 0 {
		return
	}
	r := ui.NewReport()
	ok := r.OutEnv.OK()
	auto, reviewed := sum.Count("auto"), sum.Count("reviewed")+sum.Count("duplicate-confirmed")
	skipped, discarded := sum.Count("skipped"), sum.Count("discarded")
	repeats := sum.Count("dropped-repeat")
	line := fmt.Sprintf("Scan session: %d auto-added, %d reviewed", auto, reviewed)
	if skipped > 0 {
		line += fmt.Sprintf(", %d skipped", skipped)
	}
	if discarded > 0 {
		line += fmt.Sprintf(", %d discarded", discarded)
	}
	if repeats > 0 {
		line += fmt.Sprintf(", %d repeat sightings dropped", repeats)
	}

	if sum.Ignored > 0 {
		line += fmt.Sprintf(", %d unreadable", sum.Ignored)
	}
	r.Result("%s", line)

	for _, e := range sum.Entries {
		switch e.Kind {
		case "auto":
			r.Detail("%s %s", ok("✓"), e.Line)
		case "reviewed", "duplicate-confirmed":
			r.Detail("+ %s", e.Line)
		case "skipped":
			r.Detail("- skipped %s", e.Line)
		case "discarded":
			r.Detail("- %s", e.Line)
		case "dropped-repeat":
			r.Detail("- %s", e.Line)
		}
	}
}

func destinations(st *store.Store) ([]tui.Destination, error) {
	binders, err := st.ListBinders()
	if err != nil {
		return nil, err
	}
	decks, err := st.ListDecks()
	if err != nil {
		return nil, err
	}
	out := make([]tui.Destination, 0, len(binders)+len(decks))
	for _, b := range binders {
		out = append(out, tui.Destination{ID: b.ID, Name: b.Name, Kind: "binder"})
	}
	for _, d := range store.DecksByValue(decks) {
		out = append(out, tui.Destination{ID: d.ID, Name: d.Name, Kind: "deck"})
	}
	return out, nil
}
