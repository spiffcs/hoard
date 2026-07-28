// Command mtg catalogs valuable Magic: The Gathering cards in a local SQLite
// database. Cards are added by their Scryfall page URL; the tool records how
// many you own (normal and foil) and their current market prices.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/cphillips918/mtg_index/internal/scryfall"
	"github.com/cphillips918/mtg_index/internal/store"
)

const usage = `mtg — catalog valuable MTG cards in SQLite

Usage:
  mtg [--db PATH] <command> [args]

Commands:
  add <scryfall-url> [--foil] [--qty N]   Add a card (fetches current prices)
  list                                    List the collection and total value
  update-prices                           Refresh prices for all cards
  set-qty <scryfall-url> [--normal N] [--foil N]   Set exact quantities
  remove <scryfall-url>                   Remove a card from the collection

The database path defaults to ./mtg_index.db (override with --db or $MTG_INDEX_DB).
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// Global --db flag may appear before the subcommand.
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
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
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

func cmdAdd(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	foil := fs.Bool("foil", false, "add the card as foil")
	qty := fs.Int("qty", 1, "quantity to add")
	pos, err := parsePositionals(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("add requires exactly one Scryfall URL")
	}
	if *qty < 1 {
		return fmt.Errorf("--qty must be at least 1")
	}

	card, err := resolveCard(ctx, pos[0])
	if err != nil {
		return err
	}

	if err := st.AddCard(toStoreCard(card), *foil, *qty); err != nil {
		return err
	}

	finish := "normal"
	price := card.PriceUSD
	if *foil {
		finish = "foil"
		price = card.PriceUSDFoil
	}
	fmt.Printf("Added %d× %s (%s/%s) as %s — %s\n",
		*qty, card.Name, card.Set, card.CollectorNumber, finish, formatPrice(price))
	return nil
}

func cmdList(st *store.Store) error {
	cards, err := st.List()
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
		value := lineValue(c)
		total += value
		fmt.Fprintf(tw, "%s\t%s/%s\t%d\t%d\t%s\t%s\t%s\n",
			c.Name, c.SetCode, c.CollectorNumber, c.QtyNormal, c.QtyFoil,
			formatPrice(c.PriceUSD), formatPrice(c.PriceUSDFoil), formatUSD(value))
	}
	fmt.Fprintf(tw, "\t\t\t\t\tTOTAL\t%s\n", formatUSD(total))
	return tw.Flush()
}

func cmdUpdatePrices(ctx context.Context, st *store.Store) error {
	cards, err := st.List()
	if err != nil {
		return err
	}
	if len(cards) == 0 {
		fmt.Println("Collection is empty; nothing to update.")
		return nil
	}

	updated := 0
	for i, c := range cards {
		// Respect Scryfall's rate-limit guidance (~10 req/s).
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		fresh, err := scryfall.FetchCard(ctx, c.SetCode, c.CollectorNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  skip %s (%s/%s): %v\n", c.Name, c.SetCode, c.CollectorNumber, err)
			continue
		}
		if err := st.UpdatePrices(c.ScryfallID, fresh.PriceUSD, fresh.PriceUSDFoil); err != nil {
			return err
		}
		updated++
	}
	fmt.Printf("Updated prices for %d of %d cards.\n", updated, len(cards))
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
	// Look up the existing row so we can preserve any unspecified quantity.
	existing, err := findCard(st, set, number)
	if err != nil {
		return err
	}

	newNormal, newFoil := existing.QtyNormal, existing.QtyFoil
	if *normal >= 0 {
		newNormal = *normal
	}
	if *foil >= 0 {
		newFoil = *foil
	}

	if _, err := st.SetQuantities(existing.ScryfallID, newNormal, newFoil); err != nil {
		return err
	}
	fmt.Printf("Set %s (%s/%s) to normal=%d foil=%d\n",
		existing.Name, existing.SetCode, existing.CollectorNumber, newNormal, newFoil)
	return nil
}

func cmdRemove(st *store.Store, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("remove requires exactly one Scryfall URL")
	}
	set, number, err := scryfall.ParseCardURL(args[0])
	if err != nil {
		return err
	}
	existing, err := findCard(st, set, number)
	if err != nil {
		return err
	}
	if _, err := st.Remove(existing.ScryfallID); err != nil {
		return err
	}
	fmt.Printf("Removed %s (%s/%s)\n", existing.Name, existing.SetCode, existing.CollectorNumber)
	return nil
}

// findCard locates a stored card by set code and collector number. It errors if
// the card is not in the collection.
func findCard(st *store.Store, set, number string) (*store.Card, error) {
	cards, err := st.List()
	if err != nil {
		return nil, err
	}
	for i := range cards {
		if cards[i].SetCode == set && cards[i].CollectorNumber == number {
			return &cards[i], nil
		}
	}
	return nil, fmt.Errorf("card %s/%s is not in your collection", set, number)
}

// toStoreCard maps a fetched Scryfall card to a store.Card (quantities set later).
func toStoreCard(c *scryfall.Card) store.Card {
	return store.Card{
		ScryfallID:      c.ID,
		SetCode:         c.Set,
		CollectorNumber: c.CollectorNumber,
		Name:            c.Name,
		PriceUSD:        c.PriceUSD,
		PriceUSDFoil:    c.PriceUSDFoil,
		ScryfallURL:     c.ScryfallURL,
	}
}

func lineValue(c store.Card) float64 {
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
