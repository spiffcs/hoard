// Command mtg catalogs valuable Magic: The Gathering cards in a local SQLite
// database. Loose cards are added by their Scryfall page URL; whole decks are
// imported from a deck-list link (or a pasted/exported text list). The tool
// records how many of each card you own (across the collection and every deck)
// and their current market prices.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/cphillips918/mtg_index/internal/decksource"
	"github.com/cphillips918/mtg_index/internal/scryfall"
	"github.com/cphillips918/mtg_index/internal/store"
	"github.com/cphillips918/mtg_index/internal/tui"
)

const usage = `mtg — catalog valuable MTG cards and decks in SQLite

Usage:
  mtg [--db PATH] <command> [args]

Collection commands:
  add <scryfall-url> [--foil] [--qty N]            Add a loose card by URL
  add <card name>                                  Add a loose card interactively (TUI)
  list                                             List loose cards and total value
  set-qty <scryfall-url> [--normal N] [--foil N]   Set exact loose quantities
  remove <scryfall-url>                            Remove a loose card
  update-prices                                    Refresh prices for all cards
  summary                                          Value of collection + each deck

Deck commands:
  deck add <archidekt-url>                         Import/refresh a deck from a link
  deck add --file <path> [--name NAME] [--source S]  Import a text/exported decklist
  deck list                                        List decks with card counts + value
  deck show <id|name>                              Show a deck's cards by board
  deck remove <id|name>                            Delete a deck

The database path defaults to ./mtg_index.db (override with --db or $MTG_INDEX_DB).
Moxfield's API is Cloudflare-blocked; export that deck to text and use 'deck add --file'.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("mtg", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dbPath := fs.String("db", defaultDBPath(), "path to the SQLite database file")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	if err := fs.Parse(args); err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return fmt.Errorf("no command given")
	}
	cmd, cmdArgs := rest[0], rest[1:]

	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Print(usage)
		return nil
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	switch cmd {
	case "add":
		return cmdAdd(ctx, st, cmdArgs)
	case "list":
		return cmdList(st)
	case "update-prices":
		return cmdUpdatePrices(ctx, st)
	case "set-qty":
		return cmdSetQty(st, cmdArgs)
	case "remove":
		return cmdRemove(st, cmdArgs)
	case "summary":
		return cmdSummary(st)
	case "deck":
		return cmdDeck(ctx, st, cmdArgs)
	default:
		fs.Usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// defaultDBPath returns $MTG_INDEX_DB if set, else ./mtg_index.db.
func defaultDBPath() string {
	if p := os.Getenv("MTG_INDEX_DB"); p != "" {
		return p
	}
	return "mtg_index.db"
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
		qty, card.Name, card.Set, card.CollectorNumber, finish, formatPrice(price))
	return nil
}

func addByName(ctx context.Context, st *store.Store, name string) error {
	if !stdinIsTTY() {
		return fmt.Errorf("adding by name needs an interactive terminal; " +
			"pass a Scryfall URL instead (e.g. mtg add https://scryfall.com/card/uma/7/...)")
	}
	res, err := tui.Run(ctx, tui.NewScryfallSearcher(), name)
	if err != nil {
		return err
	}
	if res == nil {
		fmt.Println("Cancelled; nothing added.")
		return nil
	}
	if err := st.AddCardFinish(res.Card, res.Finish, res.Qty); err != nil {
		return err
	}
	price := res.Card.PriceUSD
	if res.Finish == "foil" || res.Finish == "etched" {
		price = res.Card.PriceUSDFoil
	}
	fmt.Printf("Added %d× %s (%s/%s) as %s — %s\n",
		res.Qty, res.Card.Name, res.Card.Set, res.Card.CollectorNumber, res.Finish, formatPrice(price))
	return nil
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

func cmdList(st *store.Store) error {
	cards, err := st.ListCollection()
	if err != nil {
		return err
	}
	if len(cards) == 0 {
		fmt.Println("Collection is empty. Add a card with: mtg add <scryfall-url>")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSET/NUM\tNORMAL\tFOIL\tUSD\tUSD FOIL\tVALUE")

	var total float64
	for _, c := range cards {
		value := collectionLineValue(c)
		total += value
		fmt.Fprintf(tw, "%s\t%s/%s\t%d\t%d\t%s\t%s\t%s\n",
			c.Name, c.SetCode, c.CollectorNumber, c.QtyNormal, c.QtyFoil,
			formatPrice(c.PriceUSD), formatPrice(c.PriceUSDFoil), formatUSD(value))
	}
	fmt.Fprintf(tw, "\t\t\t\t\tTOTAL\t%s\n", formatUSD(total))
	return tw.Flush()
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
	return nil
}

func cmdSetQty(st *store.Store, args []string) error {
	fs := flag.NewFlagSet("set-qty", flag.ContinueOnError)
	normal := fs.Int("normal", -1, "exact normal (non-foil) quantity")
	foil := fs.Int("foil", -1, "exact foil quantity")
	pos, err := parsePositionals(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("set-qty requires exactly one Scryfall URL")
	}
	if *normal < 0 && *foil < 0 {
		return fmt.Errorf("set at least one of --normal or --foil")
	}

	set, number, err := scryfall.ParseCardURL(pos[0])
	if err != nil {
		return err
	}
	existing, err := st.FindCollectionCard(set, number)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("card %s/%s is not in your collection", set, number)
	}

	// Load current counts so an unspecified flag leaves that finish unchanged.
	cur, err := currentCollectionQty(st, existing.ScryfallID)
	if err != nil {
		return err
	}
	newNormal, newFoil := cur.normal, cur.foil
	if *normal >= 0 {
		newNormal = *normal
	}
	if *foil >= 0 {
		newFoil = *foil
	}

	if _, err := st.SetCollectionQuantities(existing.ScryfallID, newNormal, newFoil); err != nil {
		return err
	}
	fmt.Printf("Set %s (%s/%s) to normal=%d foil=%d\n",
		existing.Name, existing.SetCode, existing.CollectorNumber, newNormal, newFoil)
	return nil
}

type qtyPair struct{ normal, foil int }

func currentCollectionQty(st *store.Store, scryfallID string) (qtyPair, error) {
	cards, err := st.ListCollection()
	if err != nil {
		return qtyPair{}, err
	}
	for _, c := range cards {
		if c.ScryfallID == scryfallID {
			return qtyPair{c.QtyNormal, c.QtyFoil}, nil
		}
	}
	return qtyPair{}, nil
}

func cmdRemove(st *store.Store, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("remove requires exactly one Scryfall URL")
	}
	set, number, err := scryfall.ParseCardURL(args[0])
	if err != nil {
		return err
	}
	existing, err := st.FindCollectionCard(set, number)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("card %s/%s is not in your collection", set, number)
	}
	if _, err := st.RemoveFromCollection(existing.ScryfallID); err != nil {
		return err
	}
	fmt.Printf("Removed %s (%s/%s)\n", existing.Name, existing.SetCode, existing.CollectorNumber)
	return nil
}

func cmdSummary(st *store.Store) error {
	collVal, err := st.CollectionValue()
	if err != nil {
		return err
	}
	decks, err := st.ListDecks()
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "BUCKET\tCARDS\tVALUE")
	fmt.Fprintf(tw, "Collection (loose)\t\t%s\n", formatUSD(collVal))
	grand := collVal
	for _, d := range decks {
		fmt.Fprintf(tw, "Deck: %s\t%d\t%s\n", d.Name, d.TotalCopies, formatUSD(d.Value))
		grand += d.Value
	}
	fmt.Fprintf(tw, "\t\t\n")
	fmt.Fprintf(tw, "GRAND TOTAL\t\t%s\n", formatUSD(grand))
	return tw.Flush()
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

	resolver := newResolver(found)
	var entries []store.Entry
	var unresolved []string
	for _, e := range deck.Entries {
		id, ok := resolver.lookup(e.Ident)
		if !ok {
			unresolved = append(unresolved, identLabel(e.Ident))
			continue
		}
		entries = append(entries, store.Entry{
			ScryfallID: id,
			Finish:     e.Finish,
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

	fmt.Printf("Imported deck #%d %q (%s) — %d cards resolved.\n",
		id, deck.Name, deck.Source, len(entries))
	if len(unresolved) > 0 {
		fmt.Printf("  %d cards could not be resolved and were skipped:\n", len(unresolved))
		for _, u := range unresolved {
			fmt.Printf("    - %s\n", u)
		}
	}
	return nil
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
	if len(decks) == 0 {
		fmt.Println("No decks yet. Import one with: mtg deck add <archidekt-url>")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSOURCE\tCARDS\tVALUE")
	for _, d := range decks {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%s\n", d.ID, d.Name, d.Source, d.TotalCopies, formatUSD(d.Value))
	}
	return tw.Flush()
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

	header := deck.Name
	if deck.Format != "" {
		header += " — " + deck.Format
	}
	fmt.Println(header)
	if deck.SourceURL != "" {
		fmt.Println(deck.SourceURL)
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "BOARD\tQTY\tNAME\tSET/NUM\tFINISH\tPRICE\tVALUE")
	var total float64
	for _, e := range entries {
		total += e.Value()
		finish := e.Finish
		if finish == "normal" {
			finish = "-"
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s/%s\t%s\t%s\t%s\n",
			e.Board, e.Quantity, e.Card.Name, e.Card.SetCode, e.Card.CollectorNumber,
			finish, formatPrice(e.Price()), formatUSD(e.Value()))
	}
	fmt.Fprintf(tw, "\t\t\t\t\tTOTAL\t%s\n", formatUSD(total))
	return tw.Flush()
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

// --- Formatting ---

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

func formatPrice(p *float64) string {
	if p == nil {
		return "—"
	}
	return formatUSD(*p)
}

func formatUSD(v float64) string {
	return fmt.Sprintf("$%.2f", v)
}
