package command

// The watch command: standing price thresholds, checked against stored
// prices only — the cron pairing is `hoard update-prices && hoard watch`.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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

// watchFlags is what `watch add` was asked for, before any of it is checked
// against the others.
type watchFlags struct {
	under, over      float64
	drop, rise       string // a percentage: "10%" or "10"
	minMove          float64
	since            string
	foil, minMoveSet bool
}

// parsePercent reads a movement's size off a flag, naming the flag in whatever
// the units boundary refuses. store.ParsePercent holds the rule, because the
// import file spells a movement the same way and one conversion decided once
// is what keeps a factor of a hundred from appearing between them.
func parsePercent(flag, raw string) (float64, error) {
	pct, err := store.ParsePercent(flag, raw)
	if err != nil {
		return 0, cli.Usagef("--%s %v", flag, err)
	}
	return pct, nil
}

// watchAdd stands the watches one card was asked for.
//
// Both dollar bounds at once is a band — alert outside $1 to $5 — and a band
// is two watches, not one: the store keys on (card, finish, op), so under and
// over are separate rows with separate ids. That is not an implementation
// detail to be hidden, because every other watch command already deals in one
// direction at a time; list shows two lines and rm takes one id. It is one
// card, though, so the bounds go to the action layer together and the card is
// resolved once — see action.WatchAddOptions.
//
// A movement and a dollar line cannot be asked for in one command. They are
// two different questions about one printing — "tell me when it moves 10%" and
// "tell me when it crosses $30" — and each deserves its own line, so that
// watch rm can take one away without the other.
func watchAdd(ctx context.Context, st *store.Store, env *cli.Env, words []string, fl watchFlags) error {
	name := strings.TrimSpace(strings.Join(words, " "))
	if name == "" {
		return cli.Usagef("watch add needs a card name, e.g. hoard watch add Sol Ring --under 2")
	}
	// A zero or negative bound is an unset one: prices are positive, so
	// there is no threshold either flag could be asking for down there.
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
		// Days at the boundary, so the store keeps an integer and movers'
		// vocabulary does not have to move packages to be reused here.
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
	// A band only means anything with a gap in the middle. Reversed or
	// equal, the two halves cover the whole number line: nothing downstream
	// can catch that, because each row is individually valid and each one
	// fires, so the user gets alerts forever from a command that looked
	// like it worked.
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
			// Some of the writes stood. Say so, or a retry reads as though
			// the whole command did nothing.
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

// watchPrintingNotes says which printing the watch landed on and why it was
// that one.
//
// A bare card name is not a printing, and the answer to one is Scryfall's
// rather than hoard's: `watch add Bitterblossom` stood a watch on 2x2/69 while
// the owner held uma/85, printed a line that read like a success, and would
// have alerted about a card nobody owns. WatchAdd now prefers a held printing;
// this is the half that makes the outcome legible either way, because in a
// transcript a silent right answer and a silent wrong one look identical —
// which is precisely how the original went unnoticed.
//
// Nothing is said when the collection holds exactly the printing asked for and
// nothing else: the confirmation line already names it, and a note on the one
// case that needs no warning is how a reader learns to skip the notes.
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
		// The held printing was asked for by id and something else came back,
		// which resolve only does when Scryfall no longer knows that id and the
		// name retry answered instead. Rare — and exactly the substitution the
		// old silence hid, so it is the loudest line here.
		say("Scryfall did not answer for the printing you hold (%s), so this watch "+
			"follows its own choice of printing instead.", heldPrintings(res.Held, ""))
	case len(res.Held) > 1:
		// The winning count goes on the line that states the rule. Without it
		// the reader is told a tie-break was applied and shown only the losers,
		// which is the one arrangement that makes a correct pick look arbitrary.
		say("You hold %d printings of %s; watching the one you hold most of (%s).",
			len(res.Held), res.Card.Name, ui.Qty(heldAny(res.Held, res.Card.ID)))
		say("Also held: %s.", heldPrintings(res.Held, res.Card.ID))
		say("To watch one of those instead, name it by set and collector " +
			"number through hoard watch import.")
	case heldCopies(res.Held, res.Card.ID) == 0:
		say("Watching a printing you hold, though no copy of it is %s.", res.Finish)
	}
}

// heldPrintings lists held printings as "uma/85 ×4", skipping the one already
// named on the confirmation line.
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

// heldCopies is how many copies of one printing are held in the watched
// finish. Zero with the printing still in the list means it is held in some
// other finish — a foil watch on a card owned only in non-foil, most often.
func heldCopies(held []store.HeldPrinting, id string) int {
	for _, h := range held {
		if h.ScryfallID == id {
			return h.Copies
		}
	}
	return 0
}

// heldAny is heldCopies across every finish, which is the number the "also
// held" list shows and therefore the one the chosen row has to be comparable
// against.
func heldAny(held []store.HeldPrinting, id string) int {
	for _, h := range held {
		if h.ScryfallID == id {
			return h.AnyCopies
		}
	}
	return 0
}

// watchRule phrases one bound the way the row will read it back.
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

// watchPercentNotes says the two things a percent watch can only be honest
// about at the moment it is set.
//
// A movement is read from one vendor's series for both of its ends, so a
// printing that vendor does not price can never answer — and that is silence,
// which is indistinguishable from "nothing moved" unless it is said here. The
// floor is the other: ten percent of a fifty cent card is a nickel, and a
// watch set without a floor on a card that cheap is the shape of the incident
// this feature came from.
func watchPercentNotes(st *store.Store, env *cli.Env, res action.WatchAddResult, fl watchFlags, windowDays int) {
	dim := env.OutEnv.Dim()
	// The anchor's lower bound includes the watch's own creation, so a watch
	// set today measures from today's price however far the card has already
	// fallen. That is the right reading of "tell me when this moves" — the
	// alternative fires the moment it is created, reporting history as news —
	// but it is not what someone watching a card mid-slide expects, and the
	// only honest place to say so is here.
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
	if res.Finish == "foil" {
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
	// A lone dash is stdin, spelled the way hoard add --file spells it, so a
	// watch list can be piped from wherever it was generated. WatchImport
	// has always documented its Display as a path or stdin; only this line
	// never delivered the second half.
	data, display, err := readPathOrStdin(pos[0])
	if err != nil {
		return err
	}
	pr := stderrPrinter()
	res, err := action.WatchImport(ctx, addDeps(st), pr.Fn(),
		action.WatchImportOptions{Data: data, Display: display})
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
	// The rows that named only a card. Said because the alternative is what
	// `watch add` used to do silently: point the alert at whichever printing
	// Scryfall calls the card's newest, which may be one nobody owns.
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
	// Before the empty-list hint, because "no watches" is an answer a script
	// needs as much as a person does, and an empty rows array says it in the
	// shape the caller already knows how to read.
	if cmdEnv.JSON {
		return hoardjson.Write(out, hoardjson.FromWatchList(watches))
	}
	if len(watches) == 0 {
		fmt.Fprintln(out, env.Dim()("No watches. Add one: hoard watch add <name> --under N | --over N"))
		return nil
	}
	// No ID column, and that is safe for a reason worth writing down because it
	// is not obvious: watch rm takes an id, so dropping the column looks like
	// it strands anyone whose name fragment is ambiguous. It does not, because
	// WatchByRef's ambiguity error prints the ids itself —
	//
	//	"Ancient Tomb" matches 2 watches:
	//	  12: Ancient Tomb foil over $149.86
	//	  13: Ancient Tomb foil under $122.62
	//	use the id
	//
	// — so the id is offered at the moment it is needed, by the command that
	// needs it, and only then. A column carrying it on every row of every
	// listing was the worse-targeted answer to a question most rows never ask.
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 12},
			{Title: "SET/NUM", Align: ui.Left, Priority: 3, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "WATCH", Align: ui.Left},
			// The anchor is the least important of the three numbers and goes
			// first on a narrow terminal. Folding it into WATCH instead was
			// rejected: that cell already holds the widest variable content in
			// the table, and the flex budget belongs to NAME, which is the
			// column a reader scans.
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
		// The state is the store's word, spaced out for reading. One direction
		// only: the machine vocabulary is what the schema publishes, and this
		// column is the rendering of it rather than a second opinion.
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

// watchCell renders the WATCH column: the rule, in its own units.
//
// The floor is shown only when it is doing something. A watch whose min-move
// is silently suppressing alerts has to say so on its own row, or the one
// confusing thing this feature can do — "it moved 12%, why was I not told?" —
// has no visible cause anywhere.
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
		// The finish is only worth saying when it is the notable one, as the
		// tables do with their dimmed '-' for nonfoil.
		finish := ""
		if w.Finish == "foil" {
			finish = " foil"
		}
		fmt.Fprintf(out, "%s\n", watchAlert(env, w, finish))
	}
	if len(fired) == 0 {
		fmt.Fprintln(out, env.Dim()(fmt.Sprintf("%s watches checked, nothing crossed.", ui.Count(checked))))
		return nil
	}
	fmt.Fprintln(out, env.Dim()(fmt.Sprintf("%s watches checked, %s fired.",
		ui.Count(checked), ui.Count(len(fired)))))
	return errWatchFired
}

// watchAlert phrases one alert.
//
// An absolute watch crossed a line and the line is the news. A movement's news
// is the movement, so the sentence has to name the anchor, the size of the
// move, and when the anchor was set — "down 10%" with nothing to measure it
// against is unfalsifiable from the alert alone, and an alert a reader cannot
// check is one they learn to skim.
//
// The prose says down and up where the row says drop and rise: the op is the
// watch's name for the rule it follows, and this is the report of what
// happened. The percentage is what gets the emphasis rather than the price,
// which inverts the absolute alert's stress on purpose — there, the price
// crossing is the event; here, the price is only context.
func watchAlert(env ui.Env, w store.WatchStatus, finish string) string {
	where := ui.Printing(w.SetCode, w.CollectorNumber)
	if !w.Percent() || w.Anchor == nil {
		return fmt.Sprintf("%s (%s)%s is %s, crossed %s %s",
			w.Name, where, finish, env.Bold()(ui.Money(*w.PriceUSD)), w.Op, ui.Money(w.Threshold))
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
		w.Name, where, finish, ui.Money(*w.PriceUSD),
		word, env.Bold()(ui.Percent(moved)), ui.Money(*w.Anchor), extreme, since)
}

// NewCmdWatch is the group whose bare form does the work: `hoard watch` checks,
// `hoard watch list` lists.
//
// The --json split that cmdWatch enforced by hand is now structural: the group
// is JSONCapable, and of its subcommands only list is — a check and a listing
// are both machine-readable questions, while add, import and rm are writes
// whose receipts are prose.
func NewCmdWatch(a *app) *cobra.Command {
	var fl watchFlags

	cmd := &cobra.Command{
		Use:     "watch",
		GroupID: groupCollection,
		Short:   "Check price watches (no network; exit 3 = fired)",
		// Wrapped to 60 columns, the narrowest terminal hoard's help claims
		// to render in: writeCommandHelp copies Long through verbatim, so a
		// Long is only ever as narrow as it was written.
		//
		// The latch is the subject because it is the one thing about this
		// command that a correct-looking supervisor loop gets wrong, and the
		// old help said "exit 3 = fired" and stopped. Both re-arm rules are
		// stated because they differ by op — see TestWatchAbsoluteLatches-
		// UntilThePriceCrossesBack and TestPercentReArmsOnFiringAlone, which
		// is where each of these sentences is demonstrated.
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
	// The baseline these measure from trails: it is the highest (or lowest)
	// price inside the window, recomputed on every check and reset whenever
	// the watch fires. A frozen baseline is already spelled --under or --over
	// against a number you worked out once.
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
		// The one subcommand that honors --json. `watch` answers what a check
		// just did; `watch list` answers what is standing, and a script that
		// misses an exit 3 has no other way to ask.
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
