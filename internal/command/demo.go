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

func NewCmdDemo(a *app) *cobra.Command {
	var reset bool

	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Open the browser on a sample collection",

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

		hist, err := demo.SeedEmbeddedHistory(st)
		if err != nil {
			return err
		}
		r.Result("Built a demo hoard: %d printings, %d copies, %d deck cards, %s price observations.",
			res.Printings, res.Copies, res.DeckCards, ui.Count(hist.Inserted+hist.BidInserted))
	} else {

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

func demoDBPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating cache directory: %w", err)
	}
	return filepath.Join(dir, "hoard", "demo.db"), nil
}
