// Command hoard catalogs valuable Magic: The Gathering cards in a local SQLite
// database. Loose cards are added by their Scryfall page URL; whole decks are
// imported from a deck-list link (or a pasted/exported text list). The tool
// records how many of each card you own (across the collection and every deck)
// and their current market prices.
package main

import (
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
	"time"

	"github.com/spiffcs/hoard/internal/arbitrage"
	"github.com/spiffcs/hoard/internal/browse"
	"github.com/spiffcs/hoard/internal/decksource"
	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tui"
	"github.com/spiffcs/hoard/internal/ui"
)

const usage = `hoard — catalog valuable MTG cards and decks in SQLite

Usage:
  hoard [--db PATH] [command] [args]

  hoard                                            Browse the hoard (no arguments)

Browsing replaces the old list, summary, deck list and deck show commands: it
shows your binder and every deck beside their cards, filters by name or card trait, edits
quantities in place, and reaches movers, unpriced and arbitrage with 'v'. Piped
or redirected, plain 'hoard' prints the summary table instead, so 'hoard | grep'
still works.

Collection commands:
  add                                              Add cards interactively by name
  update-prices [--limit N]                        Refresh prices (Scryfall updates daily)
  movers [--since 30d] [--limit N]                 Biggest risers and sinkers you hold
  backfill-prices                                  Load 90 days of past prices from MTGJSON
  unpriced                                         Cards counting as $0.00, and why
  repair-finishes                                  Fix cards stored as a finish they lack
  arbitrage [--min N] [--limit N]                  Where vendors disagree, as three tables
  catalog [status|update]                          The local copy of Scryfall's card data

Deck commands:
  deck add <archidekt-url>                         Import/refresh a deck from a link
  deck add --file <path> [--name NAME] [--source S]  Import a text/exported decklist
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

	// No command opens the browser. It replaces what used to be four separate
	// read commands — list, summary, deck list, deck show — so the thing you
	// reach for most often is also the thing you get by typing nothing.
	cmd := ""
	var cmdArgs []string
	if len(rest) > 0 {
		cmd, cmdArgs = rest[0], rest[1:]
	}

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
	case "":
		return cmdBrowse(ctx, st)
	case "add":
		return cmdAdd(ctx, st, cmdArgs)
	case "update-prices":
		return cmdUpdatePrices(ctx, st, cmdArgs)
	case "movers":
		return cmdMovers(st, cmdArgs)
	case "backfill-prices":
		return cmdBackfillPrices(ctx, st, cmdArgs)
	case "unpriced":
		return cmdUnpriced(st)
	case "repair-finishes":
		return cmdRepairFinishes(ctx, st)
	case "arbitrage":
		return cmdArbitrage(ctx, st, cmdArgs)
	case "catalog":
		return cmdCatalog(ctx, cmdArgs)
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
// instead — and the subcommands that take no flags of their own (unpriced,
// repair-finishes) would ignore it silently and open the default database. That
// failure is invisible: you get a perfectly good report about the wrong hoard.
// Extracting the flag up front means `hoard --db X unpriced` and
// `hoard unpriced --db X` are the same command.
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
	// Lookups prefer the local catalog and fall through to Scryfall, so a name
	// completes instantly and offline where it can, and a card printed since the
	// last catalog build still resolves.
	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	return tui.Run(ctx, newSearcher(cat), add, helperScanner{}, name)
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

	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	found, _, _, err := refreshCards(ctx, cat, st, ids)
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

func cmdUpdatePrices(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("update-prices", flag.ContinueOnError)
	limit := fs.Int("limit", defaultMoverRows, "risers/sinkers to list")
	if _, err := parsePositionals(fs, args); err != nil {
		return err
	}

	ids, err := st.AllCatalogIDs()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Println("Catalog is empty; nothing to update.")
		return nil
	}

	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	// Prices are only taken from the catalog when it is current. A stale one is
	// still fine for everything else, but this command exists to refresh prices
	// and must not report success over yesterday's.
	priceSource := cat
	if !ensureCatalog(ctx, cat) {
		priceSource = nil
	}
	found, notFound, local, err := refreshCards(ctx, priceSource, st, ids)
	if err != nil {
		return err
	}
	if err := st.UpdatePrices(found); err != nil {
		return err
	}
	fmt.Printf("Updated prices for %d of %d cards.\n", len(found), len(ids))
	if local > 0 {
		fmt.Printf("  %d from the local catalog, %d from Scryfall.\n", local, len(found)-local)
	}
	if len(notFound) > 0 {
		fmt.Printf("  %d cards could not be re-fetched from Scryfall.\n", len(notFound))
	}
	// Scryfall's results are already committed above, so a failure in the
	// fallback pass costs nothing that was just fetched.
	if err := fillPriceGaps(ctx, st); err != nil {
		return err
	}

	// After the gap fill, not before: a card priced from MTGJSON this run has
	// its effective price only once that pass has committed, and recording
	// first would log the gap and call the fill a change on the next run.
	changes, err := st.RecordPrices()
	if err != nil {
		return err
	}
	fmt.Println()
	printMovers(ui.Detect(os.Stdout), changes, *limit, "since the last refresh")
	return nil
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

// gapRecheckAfter is how long a "MTGJSON has no price for this" answer is
// trusted before the card is asked about again.
//
// The answer is usually permanent — a printing no vendor stocks stays unstocked
// — but not always: MTGJSON adds vendors and vendors add stock. A week keeps the
// daily refresh free of a 50 MB scan while still noticing a card that becomes
// priceable, at the cost of showing it as unpriced for up to that long.
const gapRecheckAfter = 7 * 24 * time.Hour

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

	// Asking MTGJSON about one card costs a scan of its whole daily bundle, and
	// the cards nothing can price are largely permanent. Skip the scan when
	// every remaining gap was asked about recently — but if even one is due, do
	// the scan and re-ask about all of them, since by then the cost is paid.
	cutoff := time.Now().UTC().Add(-gapRecheckAfter).Format(time.RFC3339)
	var due int
	for _, g := range gaps {
		if g.CheckedAt == nil || *g.CheckedAt < cutoff {
			due++
		}
	}
	if due == 0 {
		fmt.Printf("  %d cards have no price for a finish you own; "+
			"MTGJSON had none when last asked.\n", len(gaps))
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
	// Note that these were asked about, so a run tomorrow does not pay for the
	// same scan to learn the same thing. Cards MTGJSON did price stop being gaps
	// and are never asked about again anyway.
	asked := make([]string, 0, len(gaps))
	for _, g := range gaps {
		asked = append(asked, g.ScryfallID)
	}
	if err := st.RecordPriceGapChecks(asked); err != nil {
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

// cmdBrowse is what `hoard` with no arguments does.
//
// At a terminal it opens the browser. Redirected or piped it prints the summary
// table instead, because a full-screen program has nothing sensible to write to
// a file and `hoard | grep` is worth keeping working. ui.Detect already reports
// the difference and already renders a pipe-safe table — no colour, no
// truncation — which is why this is a branch rather than a second command.
//
// The browser loops with the add cascade: pressing `a` quits the program with a
// request to add, we run the cascade, then come back. Two bubbletea programs
// cannot share a terminal, so they take turns rather than nesting.
func cmdBrowse(ctx context.Context, st *store.Store) error {
	if !stdoutIsTTY() {
		return writeSummary(st)
	}
	// arbitrageMin matches the CLI command's --min default, so the two views
	// agree about what is too cheap to be worth a shipping label.
	const arbitrageMin = 1.0

	for {
		again, err := browse.Run(ctx, st,
			browse.WithArbitrage(func(ctx context.Context) (arbitrage.Result, error) {
				return fetchArbitrage(ctx, st, arbitrageMin)
			}))
		if err != nil || !again {
			return err
		}
		// The cascade persists each confirmed card itself, so there is nothing
		// to hand back; the browser re-reads on the next pass.
		if err := addByName(ctx, st, ""); err != nil {
			// A failed add is not a reason to lose the browser.
			fmt.Fprintln(os.Stderr, "add:", err)
		}
	}
}

// writeSummary prints the hoard's totals, the output `hoard summary` used to
// produce. It is what a non-interactive `hoard` writes.
func writeSummary(st *store.Store) error {
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

// stdoutIsTTY reports whether output is going to an interactive terminal rather
// than a pipe or a file.
func stdoutIsTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// barCells is the width of the summary's share-bar column.
const barCells = 10

// summaryTable lays out the hoard as two labelled sections — the loose
// collection and the decks — rather than a flat list distinguished by a
// repeated "Deck: " prefix.
//
// It is pure so the whole layout can be tested at any terminal width without a
// database.
func summaryTable(env ui.Env, coll store.CollectionTotals, decks []store.DeckSummary) ui.Table {
	sorted := store.DecksByValue(decks)

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
		ui.C(strings.ToUpper(store.LooseName)), ui.C(ui.Count(coll.TotalCopies)), ui.C(ui.Money(coll.Value)),
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
		return fmt.Errorf("deck requires a subcommand: add|remove")
	}
	sub, subArgs := args[0], args[1:]
	switch sub {
	case "add":
		return cmdDeckAdd(ctx, st, subArgs)
	case "remove":
		return cmdDeckRemove(st, subArgs)
	default:
		return fmt.Errorf("unknown deck subcommand %q (want add|remove)", sub)
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
