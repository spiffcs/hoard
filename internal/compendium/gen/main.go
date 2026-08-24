package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/spiffcs/hoard/internal/compendium"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func main() {
	since := flag.Int("since", 0, "only sets released in this year or later")
	sets := flag.String("sets", "", "comma-separated set codes to include")
	rarity := flag.String("rarity", "",
		"comma-separated rarities to include (common, uncommon, rare, special, mythic, bonus)")
	legal := flag.String("legal", "",
		"only cards Scryfall marks legal in this format (premodern, legacy, modern, ...)")
	format := flag.String("format", "",
		"shorthand for a format's legality and its era's sets (premodern)")
	pricedOnly := flag.Bool("priced-only", false, "skip printings Scryfall does not price")
	days := flag.Int("days", 30, "days of price history to load (max 90)")

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: gen [flags] <output.db>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	o, err := compendium.ApplyFormat(compendium.Options{
		Since:      *since,
		Sets:       split(*sets),
		Rarities:   split(*rarity),
		Legal:      *legal,
		PricedOnly: *pricedOnly,
		Days:       *days,
		CacheDir:   pricing.DefaultCacheDir(),
	}, *format)
	if err != nil {
		fmt.Fprintln(os.Stderr, "compendium/gen:", err)
		os.Exit(2)
	}
	if err := run(flag.Arg(0), o); err != nil {
		fmt.Fprintln(os.Stderr, "compendium/gen:", err)
		os.Exit(1)
	}
}

func run(path string, o compendium.Options) error {
	if err := o.Validate(); err != nil {
		return err
	}
	st, err := store.Open(path)
	if err != nil {
		return err
	}
	defer st.Close()

	p := progress.Fn(func(ev progress.Event) {
		if ev.Note != "" {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", ev.Step, ev.Note)
			return
		}
		if ev.Unit == progress.UnitCards && ev.Done > 0 {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", ev.Step, ui.Count(int(ev.Done)))
		}
	})

	res, err := compendium.Build(context.Background(), st, o, p)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nseeded %s printings, %s entries\n",
		ui.Count(res.Printings), ui.Count(res.Entries))
	fmt.Fprintf(os.Stderr, "mapped %s to MTGJSON (%s unmapped, so unpriced)\n",
		ui.Count(res.Mapped), ui.Count(res.Printings-res.Mapped))
	fmt.Fprintf(os.Stderr, "backfilled %s observations and %s bids over %d days\n",
		ui.Count(res.Observations), ui.Count(res.Bids), days(o))
	fmt.Fprintf(os.Stderr, "\nbrowse it with:  HOARD_DB=%s hoard\n", path)
	fmt.Fprintln(os.Stderr, "it is stamped read-only: hoard refuses vacuum and every other write")
	return nil
}

func days(o compendium.Options) int {
	if o.Days <= 0 {
		return 30
	}
	return o.Days
}

func split(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, ",")
}
