package command

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/demo"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// NewCmdDemo builds `hoard demo`.
//
// The browser is the half of hoard that no static example conveys, and an empty
// hoard shows none of it: a new user's first run is a blank collection, which
// looks the same as a broken one. This opens the same browser on a small sample
// collection so there is something to look at before there is anything to look
// at.
//
// It runs without a database — cli.NoStore — because the database it wants is
// not the user's. Left to the root's PersistentPreRunE this would open, and on a
// fresh machine create, the real hoard as a side effect of asking for a demo.
func NewCmdDemo(a *app) *cobra.Command {
	var reset bool

	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Open the browser on a sample collection",
		// Hard-wrapped at 60 columns, and it has to stay that way: the help
		// renderer wraps flag text but copies Long verbatim, and
		// TestUsageFitsANarrowTerminal holds every help line to 60.
		Long: "Opens the browser on a small sample collection, in a\n" +
			"database of its own.\n\n" +
			"Your own hoard is never opened, read, or written by\n" +
			"this command. The demo database lives beside the card\n" +
			"catalog in your cache directory and is safe to delete\n" +
			"at any time; --reset does it for you. Edits made in\n" +
			"the demo persist, so it is a place to try adding and\n" +
			"removing cards without consequence.\n\n" +
			"The sample is real card data with prices, and ninety\n" +
			"days of their history, frozen when this build was\n" +
			"made. It shows the shape of a populated hoard; it\n" +
			"does not price anything.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runDemo(c, a, reset)
		},
	}
	cmd.Flags().BoolVar(&reset, "reset", false,
		"discard the demo database and build it again from the sample")

	return cli.NoStore(cmd)
}

func runDemo(c *cobra.Command, a *app, reset bool) error {
	path, err := demoDBPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	if reset {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", path, err)
		}
	}

	// Seed only what we just created. An existing demo database is the user's
	// to keep — they may have added cards to it — and re-seeding would double
	// every quantity, which is the same mistake merging a hoard into itself
	// makes.
	_, statErr := os.Stat(path)
	fresh := os.IsNotExist(statErr)

	st, err := store.Open(path)
	if err != nil {
		return err
	}
	defer st.Close()

	r := a.env.Report()
	if fresh {
		res, err := action.SeedHoard(st, demo.Collection, "the sample collection")
		if err != nil {
			return err
		}
		// The history goes in with the cards, not on demand. Movers is the one
		// view a seeded hoard could not show — it charts a card against its own
		// past, and a database built a second ago has none — and the only way
		// to give it one was a ~150 MB download the demo has no business
		// making. Seeded here, the view is populated before the browser opens.
		hist, err := demo.SeedEmbeddedHistory(st)
		if err != nil {
			return err
		}
		r.Result("Built a demo hoard: %d printings, %d copies, %d deck cards, %s price observations.",
			res.Printings, res.Copies, res.DeckCards, ui.Count(hist.Inserted+hist.BidInserted))
	} else {
		// An older demo database predates the compiled-in history and would
		// open movers empty forever otherwise; demo.TopUpHistory decides.
		hist, seeded, err := demo.TopUpHistory(st)
		if err != nil {
			return err
		}
		if seeded {
			r.Result("Added %s sample price observations, so the movers view has something to chart.",
				ui.Count(hist.Inserted+hist.BidInserted))
		}
	}
	r.Detail("This is sample data in %s — not your collection. Delete it any time, or use --reset.", path)

	return cmdBrowse(c.Context(), st, a.env.JSON)
}

// demoDBPath is the cache directory, not the data directory.
//
// That placement is the whole safety argument: dataDir holds the hoard, which is
// not re-downloadable and must never be evicted, while everything under the
// cache is rebuildable by definition. A demo database is rebuildable from a
// document inside the binary, so it belongs with the catalog and the price
// downloads — where the README already tells people it is safe to delete.
func demoDBPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating cache directory: %w", err)
	}
	return filepath.Join(dir, "hoard", "demo.db"), nil
}
