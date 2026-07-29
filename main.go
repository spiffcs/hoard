// Command hoard catalogs valuable Magic: The Gathering cards in a local SQLite
// database. Loose cards are added by their Scryfall page URL; whole decks are
// imported from a deck-list link (or a pasted/exported text list). The tool
// records how many of each card you own (across the collection and every deck)
// and their current market prices.
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/cphillips918/hoard/internal/decksource"
	"github.com/cphillips918/hoard/internal/mtgjson"
	"github.com/cphillips918/hoard/internal/scan"
	"github.com/cphillips918/hoard/internal/scryfall"
	"github.com/cphillips918/hoard/internal/store"
	"github.com/cphillips918/hoard/internal/tui"
	"github.com/cphillips918/hoard/internal/ui"
)

const usage = `hoard — catalog valuable MTG cards and decks in SQLite

Usage:
  hoard [--db PATH] <command> [args]

Collection commands:
  add                                              Add cards interactively by name
  list                                             List loose cards by value, with total
  update-prices                                    Refresh prices (Scryfall updates daily)
  unpriced                                         Cards counting as $0.00, and why
  repair-finishes                                  Fix cards stored as a finish they lack
  arbitrage [--min N] [--limit N]                  Where vendors disagree on price
  summary                                          Value of collection + each deck

Deck commands:
  deck add <archidekt-url>                         Import/refresh a deck from a link
  deck add --file <path> [--name NAME] [--source S]  Import a text/exported decklist
  deck list                                        List decks by value, with card counts
  deck show <name>                                 Show a deck's cards by value
  deck remove <name>                               Delete a deck

A deck <name> can be any part of its name, as long as it matches one deck.

The database lives in a per-user data directory by default (e.g. on macOS
~/Library/Application Support/hoard/hoard.db, on Linux $XDG_DATA_HOME/hoard/hoard.db)
— so it's the same hoard from any directory. Override with --db or $HOARD_DB.
Moxfield's API is Cloudflare-blocked; export that deck to text and use 'deck add --file'.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	rest, dbFlag, err := extractDBFlag(args)
	if err != nil {
		fmt.Fprint(os.Stderr, usage)
		return err
	}

	if len(rest) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("no command given")
	}
	cmd, cmdArgs := rest[0], rest[1:]

	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Print(usage)
		return nil
	}

	// --db wins over $HOARD_DB, which in turn wins over the per-user default.
	dbPath := dbFlag
	if dbPath == "" {
		if dbPath, err = defaultDBPath(); err != nil {
			return err
		}
	}

	// Note whether we're about to create the database, so we can tell the user
	// where it lives the first time.
	_, statErr := os.Stat(dbPath)
	newDB := os.IsNotExist(statErr)

	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if newDB {
		fmt.Fprintf(os.Stderr, "Initialized hoard database at %s\n", dbPath)
	}

	ctx := context.Background()
	switch cmd {
	case "add":
		return cmdAdd(ctx, st, cmdArgs)
	case "list":
		return cmdList(st)
	case "update-prices":
		return cmdUpdatePrices(ctx, st)
	case "unpriced":
		return cmdUnpriced(st)
	case "repair-finishes":
		return cmdRepairFinishes(ctx, st)
	case "arbitrage":
		return cmdArbitrage(ctx, st, cmdArgs)
	case "summary":
		return cmdSummary(st)
	case "deck":
		return cmdDeck(ctx, st, cmdArgs)
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// extractDBFlag pulls the global --db flag out of args wherever it appears and
// returns the arguments with it removed.
//
// The standard library's flag package stops parsing at the first positional, so
// a --db written after the command name would be handed on to the subcommand
// instead — and the subcommands that take no flags of their own (list, summary,
// update-prices) would ignore it silently and open the default database. That
// failure is invisible: you get a perfectly good report about the wrong hoard.
// Extracting the flag up front means `hoard --db X summary` and
// `hoard summary --db X` are the same command.
//
// Only --db is global; every other flag is left in place for the subcommand.
func extractDBFlag(args []string) (rest []string, db string, err error) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// By convention everything after a bare "--" is positional.
		if arg == "--" {
			rest = append(rest, args[i:]...)
			break
		}

		name, value, hasValue := strings.Cut(arg, "=")
		if name != "-db" && name != "--db" {
			rest = append(rest, arg)
			continue
		}

		if !hasValue {
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("flag needs an argument: %s", arg)
			}
			i++
			value = args[i]
		}
		if value == "" {
			return nil, "", fmt.Errorf("flag needs an argument: %s", arg)
		}
		db = value // a repeated --db keeps the last, as the flag package does
	}
	return rest, db, nil
}

// defaultDBPath resolves where the hoard database lives when --db is not given.
// Precedence: $HOARD_DB, else a per-user application-data directory so the same
// hoard is used regardless of the current working directory.
func defaultDBPath() (string, error) {
	if p := os.Getenv("HOARD_DB"); p != "" {
		return p, nil
	}
	dir, err := dataDir()
	if err != nil {
		return "", fmt.Errorf("locating data directory (set --db or $HOARD_DB): %w", err)
	}
	return filepath.Join(dir, "hoard", "hoard.db"), nil
}

// dataDir returns the platform's per-user data directory: the conventional
// location on macOS/Windows and the XDG Base Directory spec elsewhere. Unlike a
// cache directory, this holds data that must not be evicted — the hoard is not
// re-downloadable.
func dataDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support"), nil
	case "windows":
		if p := os.Getenv("AppData"); p != "" {
			return p, nil
		}
	default: // linux, bsd, etc.
		if p := os.Getenv("XDG_DATA_HOME"); p != "" {
			return p, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

// parsePositionals parses args, allowing flags and positional arguments to be
// interleaved in any order (the standard library's flag package otherwise stops
// at the first positional). It returns the collected positional arguments.
func parsePositionals(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positionals, nil
		}
		positionals = append(positionals, rest[0])
		args = rest[1:]
	}
}

// resolveCard parses a Scryfall URL and fetches the card from the API.
func resolveCard(ctx context.Context, url string) (*scryfall.Card, error) {
	set, number, err := scryfall.ParseCardURL(url)
	if err != nil {
		return nil, err
	}
	return scryfall.FetchCard(ctx, set, number)
}

// --- Collection commands ---

func cmdAdd(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	foil := fs.Bool("foil", false, "add the card as foil (URL form only)")
	qty := fs.Int("qty", 1, "quantity to add (URL form only)")
	pos, err := parsePositionals(fs, args)
	if err != nil {
		return err
	}
	if *qty < 1 {
		return fmt.Errorf("--qty must be at least 1")
	}

	// A Scryfall URL takes the fast non-interactive path; anything else (a card
	// name, or no argument) launches the interactive picker.
	if len(pos) == 1 && looksLikeURL(pos[0]) {
		return addByURL(ctx, st, pos[0], *foil, *qty)
	}
	return addByName(ctx, st, strings.Join(pos, " "))
}

// looksLikeURL reports whether an argument is a Scryfall card link rather than a
// card name.
func looksLikeURL(arg string) bool {
	return strings.Contains(arg, "://") || strings.Contains(arg, "scryfall.com")
}

func addByURL(ctx context.Context, st *store.Store, url string, foil bool, qty int) error {
	card, err := resolveCard(ctx, url)
	if err != nil {
		return err
	}
	if err := st.AddCard(*card, foil, qty); err != nil {
		return err
	}
	finish := "normal"
	price := card.PriceUSD
	if foil {
		finish = "foil"
		price = card.PriceUSDFoil
	}
	fmt.Printf("Added %d× %s (%s/%s) as %s — %s\n",
		qty, card.Name, card.Set, card.CollectorNumber, finish, ui.MoneyPtr(price))
	return nil
}

func addByName(ctx context.Context, st *store.Store, name string) error {
	if !stdinIsTTY() {
		return fmt.Errorf("adding by name needs an interactive terminal; " +
			"pass a Scryfall URL instead (e.g. hoard add https://scryfall.com/card/uma/7/...)")
	}
	// Each confirmed card is persisted immediately; the session loops until the
	// user exits.
	add := func(res tui.Result) error {
		return st.AddCardFinish(res.Card, res.Finish, res.Qty)
	}
	return tui.Run(ctx, tui.NewScryfallSearcher(), add, helperScanner{}, name)
}

// helperScanner drives the native camera helper for the add TUI's scan action
// (ctrl+o). On platforms without the helper its calls return errors that the TUI
// surfaces as a banner, so the session continues.
type helperScanner struct{}

func (helperScanner) Devices(ctx context.Context) ([]scan.Device, error) {
	return scan.ListDevices(ctx)
}

func (helperScanner) Open(ctx context.Context, deviceID string) (tui.ScanSession, error) {
	// The preview rotation the user last settled on is replayed into the helper,
	// so a phone that previews sideways is corrected once rather than every run.
	prefs := loadScanPrefs()
	s, err := scan.Open(ctx, deviceID, prefs.Rotation)
	if err != nil {
		return nil, err
	}
	return &persistingSession{Session: s, rotation: prefs.Rotation}, nil
}

// persistingSession watches a live session's events for rotation changes and
// saves the last one, so a correction made mid-session survives into the next
// run. It sits here rather than in the TUI because writing preference files
// isn't the TUI's job.
type persistingSession struct {
	*scan.Session
	events   chan scan.Event
	once     sync.Once
	rotation int
}

func (p *persistingSession) Events() <-chan scan.Event {
	p.once.Do(func() {
		p.events = make(chan scan.Event, 8)
		go func() {
			defer close(p.events)
			for ev := range p.Session.Events() {
				if ev.Kind == scan.EventRotation || ev.Kind == scan.EventClosed {
					if ev.Rotation != p.rotation {
						p.rotation = ev.Rotation
						saveScanPrefs(scanPrefs{Rotation: ev.Rotation})
					}
				}
				p.events <- ev
			}
		}()
	})
	return p.events
}

// scanPrefs holds the small amount of scan state worth surviving between runs.
type scanPrefs struct {
	// Rotation is extra clockwise preview rotation in degrees (0/90/180/270).
	Rotation int `json:"rotation"`
}

// defaultScanRotation corrects the sideways preview a portrait-held iPhone
// produces out of the box. Continuity Camera hands over a landscape frame and
// the rotation coordinator often can't tell how the phone is being held, so the
// image arrives turned a quarter-turn counter-clockwise; this turns it back.
// Overridden the moment the user adjusts it with ←/→ in the capture window.
const defaultScanRotation = 90

// priceCacheDir is where downloaded MTGJSON bundles are kept.
//
// Unlike the database and scan prefs, these belong in the cache directory
// rather than beside the hoard: they are re-downloadable, so losing them to
// eviction costs nothing but a fetch. An empty string disables caching, which
// only makes the downloads repeat.
func priceCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "hoard", "mtgjson")
}

// scanPrefsPath is where scan preferences live — beside the database, so they
// follow the same per-user location.
func scanPrefsPath() (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hoard", "scan.json"), nil
}

// loadScanPrefs reads saved scan preferences, falling back to the defaults if
// they're missing or unreadable — a preferences file is never worth failing a
// scan over. A saved rotation of 0 is honoured; only an absent file gets the
// default.
func loadScanPrefs() scanPrefs {
	p := scanPrefs{Rotation: defaultScanRotation}
	path, err := scanPrefsPath()
	if err != nil {
		return p
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return scanPrefs{Rotation: defaultScanRotation}
	}
	return p
}

// saveScanPrefs persists scan preferences, ignoring failures for the same reason
// loadScanPrefs ignores them.
func saveScanPrefs(p scanPrefs) {
	path, err := scanPrefsPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(p)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// stdinIsTTY reports whether stdin is an interactive terminal (a character
// device), which the TUI requires.
func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// collectionByValue returns a copy ordered most-valuable-first, the same way
// `deck list` and `summary` rank decks. Ties fall back to name so a collection
// whose prices have never been fetched still lists predictably.
func collectionByValue(rows []store.CollectionRow) []store.CollectionRow {
	sorted := slices.Clone(rows)
	slices.SortFunc(sorted, func(a, b store.CollectionRow) int {
		if c := cmp.Compare(b.Value, a.Value); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return sorted
}

func cmdList(st *store.Store) error {
	rows, err := st.ListCollectionByFinish()
	if err != nil {
		return err
	}
	env := ui.Detect(os.Stdout)
	if len(rows) == 0 {
		fmt.Println(env.Dim()("Collection is empty. Add a card with: hoard add <scryfall-url>"))
		return nil
	}

	// One row per finish held, matching deck show. The pivoted alternative needs
	// a NORMAL/FOIL pair and a USD/USD FOIL pair to say the same thing, and
	// prints a price for a finish you do not own on every row.
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 12},
			{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 5, Style: env.Dim()},
			{Title: "QTY", Align: ui.Right},
			{Title: "PRICE", Align: ui.Right, Priority: 6, Style: env.Dim()},
			{Title: "VALUE", Align: ui.Right},
		},
	}

	var total float64
	sources := map[string]bool{}
	for _, r := range collectionByValue(rows) {
		total += r.Value
		if r.AltSource != "" {
			sources[r.AltSource] = true
		}
		finish := r.Finish
		if finish == "normal" {
			finish = "-"
		}
		t.Add(
			ui.C(r.Name), ui.C(r.SetCode+"/"+r.CollectorNumber), ui.C(finish),
			ui.C(ui.Count(r.Quantity)), ui.C(ui.MoneyPtr(r.Price())),
			ui.C(estimated(ui.Money(r.Value), r.AltSource)))
	}
	t.AddSpacer()
	t.AddStyled(env.Bold(), ui.C("TOTAL"), ui.C(""), ui.C(""), ui.C(""), ui.C(""),
		ui.C(ui.Money(total)))

	if _, err := t.WriteTo(os.Stdout); err != nil {
		return err
	}
	printEstimateNote(env, sources)
	return nil
}

// estimated marks a value Scryfall could not price, so an estimate from another
// vendor never passes for a Scryfall figure.
func estimated(s, altSource string) string {
	if altSource == "" {
		return s
	}
	return s + "*"
}

// printEstimateNote explains the asterisks, naming the vendors involved. Silent
// when every price came from Scryfall.
func printEstimateNote(env ui.Env, sources map[string]bool) {
	if len(sources) == 0 {
		return
	}
	fmt.Println(env.Dim()(fmt.Sprintf(
		"* estimated: Scryfall has no price for this printing; from %s via MTGJSON",
		strings.Join(slices.Sorted(maps.Keys(sources)), ", "))))
}

// cmdRepairFinishes corrects entries recorded in a finish that does not exist.
//
// A decklist with no foil marker imports as "normal", but plenty of printings
// are foil-only: precon commanders and Duel Decks reprints among them. Such an
// entry asks for a price that cannot exist, so the card sits at $0.00 forever
// and no amount of price fetching will help. Scryfall knows which finishes a
// printing comes in; hoard fetches that on every price refresh and has been
// discarding it.
func cmdRepairFinishes(ctx context.Context, st *store.Store) error {
	ids, err := st.AllCatalogIDs()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Println("Catalog is empty; nothing to repair.")
		return nil
	}

	idents := make([]scryfall.Identifier, len(ids))
	for i, id := range ids {
		idents[i] = scryfall.Identifier{ID: id}
	}
	found, _, err := scryfall.FetchCollection(ctx, idents)
	if err != nil {
		return err
	}
	available := make(map[string][]string, len(found))
	for _, c := range found {
		available[c.ID] = c.Finishes
	}

	fixed, ambiguous, err := st.RepairFinishes(available)
	if err != nil {
		return err
	}

	env := ui.Detect(os.Stdout)
	if len(fixed) == 0 && len(ambiguous) == 0 {
		fmt.Println(env.Dim()("Every card is recorded in a finish it actually comes in."))
		return nil
	}

	if len(fixed) > 0 {
		t := ui.Table{
			Env:    env,
			Header: true,
			Cols: []ui.Col{
				{Title: "NAME", Align: ui.Left, Flex: true, Min: 16},
				{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
				{Title: "QTY", Align: ui.Right},
				{Title: "WAS", Align: ui.Left, Style: env.Dim()},
				{Title: "NOW", Align: ui.Left},
				{Title: "IN", Align: ui.Left, Flex: true, Min: 10, Max: 30,
					Priority: 5, Style: env.Dim()},
			},
		}
		for _, f := range fixed {
			t.Add(ui.C(f.Name), ui.C(f.SetCode+"/"+f.CollectorNumber),
				ui.C(ui.Count(f.Quantity)), ui.C(f.From), ui.C(f.To), ui.C(f.Container))
		}
		if _, err := t.WriteTo(os.Stdout); err != nil {
			return err
		}
		fmt.Println(env.Dim()(fmt.Sprintf(
			"\nCorrected %s entries. Run hoard update-prices to value them.", ui.Count(len(fixed)))))
	}
	for _, a := range ambiguous {
		fmt.Println(env.Dim()(fmt.Sprintf(
			"  left alone: %s (%s/%s) is recorded as %s but comes in %s",
			a.Name, a.SetCode, a.CollectorNumber, a.From, a.To)))
	}
	return nil
}

// cmdUnpriced lists what is contributing nothing to your totals.
//
// A card with no price is valued at zero, which is indistinguishable on a table
// from a card genuinely worth nothing. This is how you tell the difference, and
// how you find out which deck's total is understated.
func cmdUnpriced(st *store.Store) error {
	rows, err := st.Unpriced()
	if err != nil {
		return err
	}
	env := ui.Detect(os.Stdout)
	if len(rows) == 0 {
		fmt.Println(env.Dim()("Every card you own has a price."))
		return nil
	}

	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 20},
			{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Style: env.Dim()},
			{Title: "COPIES", Align: ui.Right},
			// Capped and dropped first: deck names run long, and without a
			// ceiling this column would squeeze the card name to its minimum.
			{Title: "HELD IN", Align: ui.Left, Flex: true, Min: 10, Max: 34,
				Priority: 5, Style: env.Dim()},
		},
	}
	var copies int
	for _, r := range rows {
		copies += r.Copies
		finish := r.Finish
		if finish == "normal" {
			finish = "-"
		}
		t.Add(ui.C(r.Name), ui.C(r.SetCode+"/"+r.CollectorNumber), ui.C(finish),
			ui.C(ui.Count(r.Copies)), ui.C(r.HeldIn))
	}
	if _, err := t.WriteTo(os.Stdout); err != nil {
		return err
	}
	// Two different cures, and the less obvious one is usually the answer: a
	// card stored in a finish its printing does not come in can never be
	// priced, however many times you refresh.
	fmt.Println(env.Dim()(fmt.Sprintf(
		"\n%s copies across %s cards count as $0.00.\n"+
			"Try: hoard repair-finishes, then hoard update-prices",
		ui.Count(copies), ui.Count(len(rows)))))
	return nil
}

func cmdUpdatePrices(ctx context.Context, st *store.Store) error {
	ids, err := st.AllCatalogIDs()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Println("Catalog is empty; nothing to update.")
		return nil
	}

	idents := make([]scryfall.Identifier, len(ids))
	for i, id := range ids {
		idents[i] = scryfall.Identifier{ID: id}
	}
	found, notFound, err := scryfall.FetchCollection(ctx, idents)
	if err != nil {
		return err
	}
	if err := st.UpdatePrices(found); err != nil {
		return err
	}
	fmt.Printf("Updated prices for %d of %d cards.\n", len(found), len(ids))
	if len(notFound) > 0 {
		fmt.Printf("  %d cards could not be re-fetched from Scryfall.\n", len(notFound))
	}
	// Scryfall's results are already committed above, so a failure in the
	// fallback pass costs nothing that was just fetched.
	return fillPriceGaps(ctx, st)
}

// cardRef is a printing needing an MTGJSON id, and the set whose file holds it.
type cardRef struct {
	ScryfallID string
	SetCode    string
}

// resolveMTGJSONIDs maps Scryfall IDs to MTGJSON UUIDs, downloading only the set
// files it must and remembering everything it learns.
//
// Resolving one id costs a whole set-file download, the answer never changes,
// and the download cache is pruned nightly. Writing results back to the catalog
// is what stops a collection-wide price read from re-fetching most of the
// catalog's set files every day it runs.
func resolveMTGJSONIDs(ctx context.Context, st *store.Store, need []cardRef) (map[string]string, error) {
	known, err := st.KnownMTGJSONUUIDs()
	if err != nil {
		return nil, err
	}
	bySet := map[string][]string{}
	for _, r := range need {
		if _, ok := known[r.ScryfallID]; !ok {
			bySet[r.SetCode] = append(bySet[r.SetCode], r.ScryfallID)
		}
	}
	if len(bySet) == 0 {
		return known, nil
	}
	mtgjson.CacheDir = priceCacheDir()
	if len(bySet) > 3 {
		fmt.Printf("  resolving card ids from %d sets (once only)...\n", len(bySet))
	}

	learned := make(map[string]string)
	for setCode, sids := range bySet {
		ids, err := mtgjson.SetIdentifiers(ctx, setCode)
		if err != nil {
			// Scryfall and MTGJSON disagree on some promo sets. Skip the set
			// rather than abandon every other card.
			fmt.Fprintf(os.Stderr, "  skipping set %s: %v\n", setCode, err)
			continue
		}
		for _, sid := range sids {
			if uuid, ok := ids[sid]; ok {
				known[sid] = uuid
				learned[sid] = uuid
			}
		}
	}
	if err := st.SaveMTGJSONUUIDs(learned); err != nil {
		return nil, err
	}
	return known, nil
}

// fillPriceGaps looks up prices Scryfall does not have.
//
// Scryfall's USD figures come from TCGplayer alone, so a printing TCGplayer has
// no record of is simply unpriced there. The Modern Horizons 3 ripple foils are
// the case that prompted this: no usd_foil at all, which left a whole deck
// valued at a fraction of its worth. MTGJSON aggregates other vendors.
//
// Nothing is downloaded unless there is a gap to fill.
func fillPriceGaps(ctx context.Context, st *store.Store) error {
	gaps, err := st.UnpricedByOwnedFinish()
	if err != nil {
		return err
	}
	if len(gaps) == 0 {
		return nil
	}
	mtgjson.CacheDir = priceCacheDir()
	fmt.Printf("  %d cards have no price for a finish you own; checking MTGJSON...\n", len(gaps))

	need := make([]cardRef, len(gaps))
	for i, g := range gaps {
		need[i] = cardRef{ScryfallID: g.ScryfallID, SetCode: g.SetCode}
	}
	uuids, err := resolveMTGJSONIDs(ctx, st, need)
	if err != nil {
		return err
	}

	want := make(map[string]bool, len(gaps))
	byUUID := make(map[string]string, len(gaps))
	for _, g := range gaps {
		if uuid, ok := uuids[g.ScryfallID]; ok {
			want[uuid] = true
			byUUID[uuid] = g.ScryfallID
		}
	}
	prices, err := mtgjson.TodayPrices(ctx, want)
	if err != nil {
		return fmt.Errorf("mtgjson prices: %w", err)
	}

	alts := make([]store.AltPrice, 0, len(prices))
	sources := map[string]bool{}
	for uuid, p := range prices {
		alts = append(alts, store.AltPrice{
			ScryfallID:    byUUID[uuid],
			MTGJSONUUID:   uuid,
			PriceUSD:      p.USD,
			PriceUSDFoil:  p.Foil,
			SourceUSD:     p.USDSource,
			SourceUSDFoil: p.FoilSource,
		})
		sources[p.USDSource] = true
		sources[p.FoilSource] = true
	}
	delete(sources, "") // a finish this card had no vendor for
	if err := st.UpsertAltPrices(alts); err != nil {
		return err
	}

	// Count gaps actually closed, not rows written. MTGJSON often prices a card
	// only in the finish you don't own — a foil price for a card you hold in
	// non-foil — which stores a perfectly good row that closes nothing. Counting
	// writes would report those as fills and quietly overstate the result.
	remaining, err := st.UnpricedByOwnedFinish()
	if err != nil {
		return err
	}
	filled := len(gaps) - len(remaining)
	if filled <= 0 {
		fmt.Println("  no other source could price them either.")
		return nil
	}
	fmt.Printf("  filled %d from %s.\n", filled, strings.Join(slices.Sorted(maps.Keys(sources)), ", "))
	if len(remaining) > 0 {
		fmt.Printf("  %d still unpriced anywhere.\n", len(remaining))
	}
	return nil
}

func cmdSummary(st *store.Store) error {
	coll, err := st.CollectionTotals()
	if err != nil {
		return err
	}
	decks, err := st.ListDecks()
	if err != nil {
		return err
	}
	_, err = summaryTable(ui.Detect(os.Stdout), coll, decks).WriteTo(os.Stdout)
	return err
}

// barCells is the width of the summary's share-bar column.
const barCells = 10

// decksByValue returns a copy of decks ordered most-valuable-first.
//
// Both deck listings rank by value rather than name: what you want from either
// is "where is the money", and the store's ORDER BY ct.name is only a stable
// base to sort from. Ties fall back to name so that a hoard whose prices have
// never been fetched — every deck at $0.00 — still lists in a predictable
// order instead of an arbitrary one.
func decksByValue(decks []store.DeckSummary) []store.DeckSummary {
	sorted := slices.Clone(decks)
	slices.SortFunc(sorted, func(a, b store.DeckSummary) int {
		if c := cmp.Compare(b.Value, a.Value); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return sorted
}

// summaryTable lays out the hoard as two labelled sections — the loose
// collection and the decks — rather than a flat list distinguished by a
// repeated "Deck: " prefix.
//
// It is pure so the whole layout can be tested at any terminal width without a
// database.
func summaryTable(env ui.Env, coll store.CollectionTotals, decks []store.DeckSummary) ui.Table {
	sorted := decksByValue(decks)

	var deckCopies int
	var deckValue float64
	for _, d := range sorted {
		deckCopies += d.TotalCopies
		deckValue += d.Value
	}
	grand := coll.Value + deckValue

	// Shares are fractions of the grand total, so the two section bars tile the
	// column and calibrate the deck bars below them.
	share := func(v float64) string {
		if !env.Bars || grand <= 0 {
			return ""
		}
		return ui.Bar(v/grand, barCells)
	}

	t := ui.Table{
		Env: env,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 12},
			{Title: "CARDS", Align: ui.Right, Priority: 2},
			{Title: "VALUE", Align: ui.Right},
			{Align: ui.Left, Min: 6, Max: barCells, Priority: 3, Style: env.Dim()},
		},
	}

	// The section rows are bold throughout, bars included: the two of them tile
	// the bar column and so double as the scale legend for the deck rows below.
	t.AddStyled(env.Bold(),
		ui.C("COLLECTION"), ui.C(ui.Count(coll.TotalCopies)), ui.C(ui.Money(coll.Value)),
		ui.C(share(coll.Value)))
	t.AddStyled(env.Bold(),
		ui.C(fmt.Sprintf("DECKS · %d", len(sorted))), ui.C(ui.Count(deckCopies)),
		ui.C(ui.Money(deckValue)), ui.C(share(deckValue)))

	if len(sorted) > 0 {
		t.AddSpacer()
		for _, d := range sorted {
			// The indent is part of the name cell, so every column to its right
			// stays aligned with the section rows above.
			t.Add(ui.C("  "+d.Name), ui.C(ui.Count(d.TotalCopies)), ui.C(ui.Money(d.Value)),
				ui.C(share(d.Value)))
		}
	}

	t.AddSpacer()
	// The total's bar cell is left empty: a full bar there is ink for no information.
	t.AddStyled(env.Bold(),
		ui.C("TOTAL"), ui.C(ui.Count(coll.TotalCopies+deckCopies)), ui.C(ui.Money(grand)), ui.C(""))

	return t
}

// --- Deck commands ---

func cmdDeck(ctx context.Context, st *store.Store, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("deck requires a subcommand: add|list|show|remove")
	}
	sub, subArgs := args[0], args[1:]
	switch sub {
	case "add":
		return cmdDeckAdd(ctx, st, subArgs)
	case "list":
		return cmdDeckList(st)
	case "show":
		return cmdDeckShow(st, subArgs)
	case "remove":
		return cmdDeckRemove(st, subArgs)
	default:
		return fmt.Errorf("unknown deck subcommand %q (want add|list|show|remove)", sub)
	}
}

func cmdDeckAdd(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("deck add", flag.ContinueOnError)
	file := fs.String("file", "", "import from a text/exported decklist file instead of a URL")
	name := fs.String("name", "", "deck name (defaults to the file name for --file imports)")
	source := fs.String("source", "", "provider label for text imports (e.g. moxfield)")
	pos, err := parsePositionals(fs, args)
	if err != nil {
		return err
	}

	var deck *decksource.Deck
	if *file != "" {
		deck, err = importTextDeck(*file, *name, *source)
	} else if len(pos) == 1 {
		deck, err = decksource.Fetch(ctx, pos[0])
	} else {
		return fmt.Errorf("deck add needs either a deck URL or --file <path>")
	}
	if err != nil {
		return err
	}

	// Resolve every entry's identifier to a catalog card in bulk.
	idents := make([]scryfall.Identifier, len(deck.Entries))
	for i, e := range deck.Entries {
		idents[i] = e.Ident
	}
	found, _, err := scryfall.FetchCollection(ctx, idents)
	if err != nil {
		return err
	}
	if err := st.UpsertCatalogCards(found); err != nil {
		return err
	}

	// Finishes, keyed by the id the resolver hands back, so an entry can be
	// checked against the finishes its printing actually comes in.
	finishes := make(map[string][]string, len(found))
	for _, c := range found {
		finishes[c.ID] = c.Finishes
	}

	resolver := newResolver(found)
	var entries []store.Entry
	var unresolved []string
	var refinished int
	for _, e := range deck.Entries {
		id, ok := resolver.lookup(e.Ident)
		if !ok {
			unresolved = append(unresolved, identLabel(e.Ident))
			continue
		}
		// A decklist line with no *F* marker parses as non-foil, but precon
		// commanders and Duel Decks reprints are frequently foil-only. Storing
		// the finish the list claimed would ask for a price that cannot exist,
		// leaving the card at $0.00 no matter how often prices are refreshed.
		finish := e.Finish
		if corrected, changed := store.CorrectFinish(finish, finishes[id]); changed {
			finish = corrected
			refinished++
		}
		entries = append(entries, store.Entry{
			ScryfallID: id,
			Finish:     finish,
			Board:      e.Board,
			Quantity:   e.Quantity,
		})
	}

	id, err := st.UpsertDeck(store.DeckMeta{
		Name:      deck.Name,
		Source:    deck.Source,
		SourceID:  deck.SourceID,
		SourceURL: deck.SourceURL,
		Format:    deck.Format,
	}, entries)
	if err != nil {
		return err
	}

	fmt.Printf("Imported deck #%d %q (%s): %d cards resolved.\n",
		id, deck.Name, deck.Source, len(entries))
	if refinished > 0 {
		fmt.Printf("  %d recorded as foil: the list said otherwise but the printing has no non-foil.\n",
			refinished)
	}
	if len(unresolved) > 0 {
		fmt.Printf("  %d cards could not be resolved and were skipped:\n", len(unresolved))
		for _, u := range unresolved {
			fmt.Printf("    - %s\n", u)
		}
	}

	// Price what Scryfall could not, now rather than on some later
	// update-prices, so a freshly imported deck is worth what it is worth. This
	// only downloads when the import actually left a gap.
	return fillPriceGaps(ctx, st)
}

func importTextDeck(path, name, source string) (*decksource.Deck, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if name == "" {
		base := filepath.Base(path)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return decksource.ParseText(name, "", "", source, f)
}

func cmdDeckList(st *store.Store) error {
	decks, err := st.ListDecks()
	if err != nil {
		return err
	}
	env := ui.Detect(os.Stdout)
	if len(decks) == 0 {
		fmt.Println(env.Dim()("No decks yet. Import one with: hoard deck add <archidekt-url>"))
		return nil
	}

	// NAME is deliberately not flexible here: this is the reference view where
	// deck names are shown in full, so `summary` is free to truncate them.
	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "NAME", Align: ui.Left},
			{Title: "CARDS", Align: ui.Right},
			{Title: "VALUE", Align: ui.Right},
		},
	}
	for _, d := range decksByValue(decks) {
		t.Add(ui.C(d.Name), ui.C(ui.Count(d.TotalCopies)), ui.C(ui.Money(d.Value)))
	}
	_, err = t.WriteTo(os.Stdout)
	return err
}

// entriesByValue returns a copy of a deck's entries ordered most-valuable-first,
// matching `list`, `deck list` and `summary`.
//
// The store returns them grouped by board, which this flattens: the BOARD column
// still says which board each card belongs to, and "what is expensive in this
// deck" is the question `deck show` is usually asked. Ties fall back to name so
// an unpriced deck still lists predictably.
func entriesByValue(entries []store.EntryView) []store.EntryView {
	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, func(a, b store.EntryView) int {
		if c := cmp.Compare(b.Value(), a.Value()); c != 0 {
			return c
		}
		return strings.Compare(a.Card.Name, b.Card.Name)
	})
	return sorted
}

func cmdDeckShow(st *store.Store, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("deck show requires a deck id or name")
	}
	deck, err := st.DeckByRef(args[0])
	if err != nil {
		return err
	}
	entries, err := st.DeckEntries(deck.ID)
	if err != nil {
		return err
	}

	env := ui.Detect(os.Stdout)
	header := deck.Name
	if deck.Format != "" {
		header += " — " + deck.Format
	}
	fmt.Println(env.Bold()(header))
	if deck.SourceURL != "" {
		fmt.Println(env.Dim()(deck.SourceURL))
	}

	t := ui.Table{
		Env:    env,
		Header: true,
		Cols: []ui.Col{
			{Title: "BOARD", Align: ui.Left, Style: env.Dim()},
			{Title: "QTY", Align: ui.Right},
			{Title: "NAME", Align: ui.Left, Flex: true, Min: 12},
			{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
			{Title: "FINISH", Align: ui.Left, Priority: 5, Style: env.Dim()},
			// PRICE and VALUE are the same number on a singleton, which is most
			// deck rows, but not on the ones held in multiples: a 29x basic land
			// is worth 29 times what its price column says, and VALUE is what
			// TOTAL sums. PRICE is the one that drops first when space is tight.
			{Title: "PRICE", Align: ui.Right, Priority: 6, Style: env.Dim()},
			{Title: "VALUE", Align: ui.Right},
			// Naming the source outright is clearer than the asterisk used in
			// `list`, since there is room for a column here. It is the first
			// thing dropped, being the least load-bearing.
			{Title: "SOURCE", Align: ui.Left, Priority: 7, Style: env.Dim()},
		},
	}
	var total float64
	for _, e := range entriesByValue(entries) {
		total += e.Value()
		finish := e.Finish
		if finish == "normal" {
			finish = "-"
		}
		source := e.Card.AltSource
		if source == "" && e.Price() != nil {
			source = "scryfall"
		}
		t.Add(ui.C(e.Board), ui.C(ui.Count(e.Quantity)), ui.C(e.Card.Name),
			ui.C(e.Card.SetCode+"/"+e.Card.CollectorNumber), ui.C(finish),
			ui.C(ui.MoneyPtr(e.Price())), ui.C(ui.Money(e.Value())), ui.C(source))
	}
	t.AddSpacer()
	t.AddStyled(env.Bold(), ui.C("TOTAL"), ui.C(""), ui.C(""), ui.C(""), ui.C(""),
		ui.C(""), ui.C(ui.Money(total)), ui.C(""))

	_, err = t.WriteTo(os.Stdout)
	return err
}

func cmdDeckRemove(st *store.Store, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("deck remove requires a deck id or name")
	}
	deck, err := st.DeckByRef(args[0])
	if err != nil {
		return err
	}
	if _, err := st.RemoveContainer(deck.ID); err != nil {
		return err
	}
	fmt.Printf("Removed deck #%d %q\n", deck.ID, deck.Name)
	return nil
}

// --- Identifier resolution helpers ---

// resolver maps deck-import identifiers back to canonical Scryfall IDs using the
// cards returned by the bulk collection lookup.
type resolver struct {
	byID   map[string]string // scryfall id -> itself (confirms it resolved)
	bySN   map[string]string // "set/number" -> scryfall id
	byName map[string]string // lower(name) -> scryfall id
}

func newResolver(cards []scryfall.Card) resolver {
	r := resolver{
		byID:   make(map[string]string),
		bySN:   make(map[string]string),
		byName: make(map[string]string),
	}
	for _, c := range cards {
		r.byID[c.ID] = c.ID
		r.bySN[strings.ToLower(c.Set)+"/"+c.CollectorNumber] = c.ID
		r.byName[strings.ToLower(c.Name)] = c.ID
	}
	return r
}

func (r resolver) lookup(id scryfall.Identifier) (string, bool) {
	switch {
	case id.ID != "":
		v, ok := r.byID[id.ID]
		return v, ok
	case id.Set != "" && id.CollectorNumber != "":
		v, ok := r.bySN[strings.ToLower(id.Set)+"/"+id.CollectorNumber]
		return v, ok
	case id.Name != "":
		v, ok := r.byName[strings.ToLower(id.Name)]
		return v, ok
	}
	return "", false
}

func identLabel(id scryfall.Identifier) string {
	switch {
	case id.Name != "":
		return id.Name
	case id.Set != "" && id.CollectorNumber != "":
		return id.Set + "/" + id.CollectorNumber
	default:
		return id.ID
	}
}

// --- Valuation ---

// collectionLineValue is the worth of one loose-collection row across both
// finishes. Money formatting itself lives in internal/ui.
func collectionLineValue(c store.CollectionCard) float64 {
	var v float64
	if c.PriceUSD != nil {
		v += float64(c.QtyNormal) * *c.PriceUSD
	}
	if c.PriceUSDFoil != nil {
		v += float64(c.QtyFoil) * *c.PriceUSDFoil
	}
	return v
}
