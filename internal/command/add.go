package command

// The add flow: turn a Scryfall URL, a typed name, or a camera scan into rows
// in the binder.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tui"
	"github.com/spiffcs/hoard/internal/ui"
)

// addOpts are `hoard add`'s flags, gathered rather than passed one by one.
//
// foilSet and qtySet record whether the user typed the flag, not what it
// holds. Both have values a user might mean on purpose — --qty 1 is the
// default's number and --foil false is its state — so the value cannot say
// whether it was asked for, and the paths that cannot honour them have to
// refuse rather than guess.
type addOpts struct {
	foil      bool
	foilSet   bool
	qty       int
	qtySet    bool
	file      string
	binderRef string
	again     bool
}

// NewCmdAdd builds `hoard add`, the one command with three shapes behind a
// single name: a URL adds non-interactively, --file takes a whole list, and
// anything else opens the picker.
func NewCmdAdd(a *app) *cobra.Command {
	var o addOpts

	cmd := &cobra.Command{
		Use:     "add [NAME... | URL]",
		GroupID: groupCollection,
		Short:   "Add cards by name, link, or list",
		Example: "hoard add\n" +
			"hoard add <scryfall-url> [--foil] [--qty N]\n" +
			"hoard add --file LIST | - [--binder B] [--again]",
		// A card name is any number of words, so the positionals cannot be
		// counted — they are joined and handed to the picker.
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
	return cmd
}

func runAdd(ctx context.Context, st *store.Store, env *cli.Env, args []string, o addOpts) error {
	if o.qty < 1 {
		return cli.Usagef("--qty must be at least 1")
	}

	// Piped input with nothing else asked for is the list path spelled
	// without flags: `pbpaste | hoard add` should just work.
	if o.file == "" && len(args) == 0 && !stdinIsTTY() {
		o.file = "-"
	}
	if o.file != "" {
		if len(args) > 0 {
			return cli.Usagef("--file takes the whole list; drop the card name arguments")
		}
		// Before addFromList, so the piped form above refuses without first
		// swallowing the pipe: a list read and then thrown away is a worse
		// answer than one never read.
		if err := refusePerCopyFlags(o,
			"a list names a quantity and a finish on every line"); err != nil {
			return err
		}
		return addFromList(ctx, st, env, o.file, o.binderRef, o.again)
	}

	// A Scryfall URL takes the fast non-interactive path; anything else (a card
	// name, or no argument) launches the interactive picker.
	if len(args) == 1 && looksLikeURL(args[0]) {
		return addByURL(ctx, st, env, args[0], o.foil, o.qty, o.binderRef)
	}
	if o.binderRef != "" {
		// Silently ignoring the flag would file cards somewhere the user did
		// not say; the interactive picker owns that choice.
		return cli.Usagef("--binder applies to URL and --file adds; " +
			"the interactive picker chooses destinations itself")
	}
	// The same objection, for the two flags the comment above was written
	// over the top of. Before addByName, so the answer is the flag the user
	// got wrong rather than a complaint about the terminal.
	if err := refusePerCopyFlags(o,
		"the interactive picker asks for both, per card"); err != nil {
		return err
	}
	return addByName(ctx, st, strings.Join(args, " "))
}

// refusePerCopyFlags rejects --qty and --foil on the paths that cannot honour
// them, naming the ones the user actually typed.
//
// Both describe one copy of one printing, which only the URL form has: a list
// carries a count and a finish per line, and the picker asks per card. They
// were marked "(URL form only)" in their usage strings and then dropped in
// silence on the other two paths — add --file l.txt --qty 4 --foil reported
// "Added 1 cards" — which records a quantity and a finish the user did not
// say. That is the defect the --binder refusal beside this call was written
// to prevent, left standing over the top of two flags doing it.
//
// This deliberately breaks invocations that ran green before. They were not
// doing what they said, so the break is the fix arriving rather than a cost
// of it.
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
	// One flag applies, two apply. A refusal that cannot conjugate its own
	// subject reads as a template rather than as hoard talking.
	verb := "applies"
	if len(named) > 1 {
		verb = "apply"
	}
	return cli.Usagef("%s %s to the URL form of add; %s",
		strings.Join(named, " and "), verb, because)
}

// looksLikeURL reports whether an argument is a Scryfall card link rather than a
// card name.
func looksLikeURL(arg string) bool {
	return strings.Contains(arg, "://") || strings.Contains(arg, "scryfall.com")
}

func addByURL(ctx context.Context, st *store.Store, env *cli.Env, url string, foil bool, qty int, binderRef string) error {
	pr := stderrPrinter()
	res, err := action.AddByURL(ctx, addDeps(st), pr.Fn(),
		action.AddByURLOptions{URL: url, Foil: foil, Qty: qty, BinderRef: binderRef})
	pr.Close()
	if err != nil {
		return err
	}
	env.Report().Success("Added %d× %s (%s/%s) as %s into %s · %s",
		qty, res.Card.Name, res.Card.Set, res.Card.CollectorNumber,
		res.Finish, res.Binder, ui.MoneyPtr(res.PriceUSD))
	return nil
}

// addDeps is the dependency set every add path shares.
func addDeps(st *store.Store) action.Deps {
	return action.Deps{Store: st, CacheDir: pricing.DefaultCacheDir(), Resolver: cardResolver}
}

// addFromList reads a pasted or exported card list from a file or stdin.
func addFromList(ctx context.Context, st *store.Store, env *cli.Env, path, binderRef string, again bool) error {
	data, display, err := readPathOrStdin(path)
	if err != nil {
		return err
	}
	return addList(ctx, st, env, data, display, binderRef, again)
}

// addList runs the paste through the action layer and renders its report.
func addList(ctx context.Context, st *store.Store, env *cli.Env, data []byte, display, binderRef string, again bool) error {
	pr := stderrPrinter()
	res, err := action.AddList(ctx, addDeps(st), pr.Fn(),
		action.AddListOptions{Data: data, Display: display, BinderRef: binderRef, Again: again})
	pr.Close()
	// A partial paste still added what resolved; the report renders before
	// the exit code says "done, mostly". Any other error added nothing.
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

// storeAdder persists one cascade result: each confirmed card lands
// immediately, in the container the cascade asked about — the default
// binder when it never had to ask. Shared by the standalone `hoard add`
// cascade and the one embedded in browse.
func storeAdder(st *store.Store) tui.Adder {
	return func(res tui.Result) error {
		// A finish correction re-keys the row the scan just wrote instead of
		// adding beside it: the first look at a foil whose marker would not
		// read committed the nonfoil default, and a later look read it. The
		// guess it corrects is answered, so its audit row goes too.
		if res.ReplacesFinish != "" && res.ReplacesFinish != res.Finish {
			// The finish re-key clears the guess it corrects inside its own
			// transaction (see store.MoveEntryFinish), so no separate
			// ClearFinishGuess call — a second delete here would eat an
			// unrelated guess row for the same printing.
			if res.ContainerID != 0 {
				// The scan only ever corrects what it just wrote, which is
				// unassessed by construction: a camera cannot judge wear.
				_, err := st.MoveEntryFinish(res.ContainerID, res.Card.ID,
					res.ReplacesFinish, res.Finish, store.ConditionUnknown)
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
		// After the add, so a failed write never banks an audit row for a
		// card the collection does not hold.
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
	// Lookups prefer the local catalog and fall through to Scryfall, so a name
	// completes instantly and offline where it can, and a card printed since the
	// last catalog build still resolves.
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

// printScanSummary leaves the session's receipt in the terminal scrollback:
// scanning writes to the collection without a per-card confirm, so the record
// of what it did has to outlive the alternate screen.
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
	// Captures that held no card at all. Not listed individually — there is
	// nothing to name — but counted, so a run where the scanner was quietly
	// throwing things away does not read the same as a clean one.
	if sum.Ignored > 0 {
		line += fmt.Sprintf(", %d unreadable", sum.Ignored)
	}
	r.Result("%s", line)
	// The receipt already speaks the glyph vocabulary — ✓ auto-added, +
	// reviewed, - dropped — so only the ✓ gains the ok color; the piped
	// bytes stay exactly as they were.
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

// destinations lists everywhere an add can land: the binders (default first),
// then decks ranked by value — the same order the browser's left pane uses, so
// the picker reads like a place the user already knows.
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
