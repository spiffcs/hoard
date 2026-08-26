package command

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/compendium"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

type compendiumOpts struct {
	since      int
	sets       string
	rarity     string
	format     string
	era        bool
	pricedOnly bool
	days       int
	all        bool
}

func NewCmdCompendium(a *app) *cobra.Command {
	var o compendiumOpts

	cmd := &cobra.Command{
		Use:   "compendium FILE",
		Short: "Build a database from a slice of all Magic",

		Long: "Builds a hoard-shaped database from a filter over every\n" +
			"paper printing, one copy of each, priced and backfilled.\n" +
			"Browse it with hoard --db FILE to price and build decks\n" +
			"in a format before you own any of it.\n\n" +
			"Your own hoard is never opened, read or written.\n\n" +
			"--format names a play format, such as premodern or\n" +
			"legacy. It is not the CSV dialect that import --format\n" +
			"means. Scryfall records legality per card rather than\n" +
			"per printing, so --format keeps every printing of every\n" +
			"legal card, later reprints included. Add --era to narrow\n" +
			"a format to its own era instead; premodern, predh and\n" +
			"aaa have one, and aaa needs it.\n\n" +
			"Pass at least one filter. Without one this builds every\n" +
			"paper printing, which is many gigabytes; --all says you\n" +
			"mean it.",
		Example: "hoard compendium --format premodern premodern.db\n" +
			"hoard compendium --format premodern --era premodern-era.db\n" +
			"hoard compendium --format aaa --era ebon-ante.db\n" +
			"hoard compendium --rarity mythic,rare --since 2020 m.db",

		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cli.Usagef("compendium needs exactly one output file")
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			return runCompendium(c.Context(), a, args[0], o)
		},
	}

	cmd.Flags().IntVar(&o.since, "since", 0,
		"only sets released in this year or later")
	cmd.Flags().StringVar(&o.sets, "sets", "",
		"comma-separated set codes to include")
	cmd.Flags().StringVar(&o.rarity, "rarity", "",
		"comma-separated rarities to include (common, uncommon, rare, special, mythic, bonus)")
	cmd.Flags().StringVar(&o.format, "format", "",
		"only cards Scryfall marks legal in this play format")
	cmd.Flags().BoolVar(&o.era, "era", false,
		"narrow --format to its own era, dropping later reprints")
	cmd.Flags().BoolVar(&o.pricedOnly, "priced-only", false,
		"skip printings Scryfall does not price")
	cmd.Flags().IntVar(&o.days, "days", 30,
		"days of price history to load (max 90)")
	cmd.Flags().BoolVar(&o.all, "all", false,
		"build every paper printing when no filter is given")

	return cli.NoStore(cmd)
}

func runCompendium(ctx context.Context, a *app, path string, o compendiumOpts) error {
	opts, err := o.buildOptions()
	if err != nil {
		return err
	}
	if err := refuseExistingDatabase(path); err != nil {
		return err
	}
	build := a.buildCompendium
	if build == nil {
		build = func(ctx context.Context, path string, o compendium.Options) error {
			return buildCompendium(ctx, a.env, path, o)
		}
	}
	return build(ctx, path, opts)
}

func (o compendiumOpts) buildOptions() (compendium.Options, error) {
	if !o.all && !o.filtered() {
		return compendium.Options{}, cli.Usagef(
			"compendium needs at least one of --rarity, --sets, --format or --since; " +
				"without one it builds every paper printing, which is many gigabytes — " +
				"pass --all if that is what you want")
	}

	opts, err := compendium.ApplyFormat(compendium.Options{
		Since:      o.since,
		Sets:       splitList(o.sets),
		Rarities:   splitList(o.rarity),
		PricedOnly: o.pricedOnly,
		Days:       o.days,
		CacheDir:   pricing.DefaultCacheDir(),
	}, o.format, o.era)
	if err != nil {
		return compendium.Options{}, err
	}
	if err := opts.Validate(); err != nil {
		return compendium.Options{}, err
	}
	return opts, nil
}

func (o compendiumOpts) filtered() bool {
	return o.since > 0 ||
		strings.TrimSpace(o.sets) != "" ||
		strings.TrimSpace(o.rarity) != "" ||
		strings.TrimSpace(o.format) != ""
}

func refuseExistingDatabase(path string) error {
	switch _, err := os.Stat(path); {
	case err == nil:
		return fmt.Errorf(
			"%s already exists; a compendium is built fresh, and building into a database "+
				"that is already there would mix it with whatever it holds — name a new file",
			path)
	case os.IsNotExist(err):
		return nil
	default:
		return err
	}
}

func buildCompendium(ctx context.Context, env *cli.Env, path string, o compendium.Options) error {
	st, err := store.Open(path)
	if err != nil {
		return err
	}
	defer st.Close()

	pr := stderrPrinter()
	res, err := compendium.Build(ctx, st, o, pr.Fn())
	pr.Close()
	if err != nil {
		return err
	}

	rep := env.Report()
	rep.Success("Seeded %s printings, %s entries.",
		ui.Count(res.Printings), ui.Count(res.Entries))
	if unmapped := res.Printings - res.Mapped; unmapped > 0 {
		rep.Warn("%s have no MTGJSON id, so they are unpriced.", ui.Count(unmapped))
	}
	rep.Result("Backfilled %s observations and %s bids.",
		ui.Count(res.Observations), ui.Count(res.Bids))
	rep.Hint("Browse it: hoard --db %s", path)
	return nil
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, ",")
}
