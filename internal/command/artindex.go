package command

// `hoard artindex`: the perceptual-hash index of every printing's Scryfall
// image, which is the scanner's art-identification channel.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/artindex"
	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/ui"
)

// artindexDir is where the art-identification index lives: beside the
// catalog, because it is derived from the catalog's image URLs and shares
// its cache lifecycle.
func artindexDir() string {
	return filepath.Join(filepath.Dir(catalogDir()), "artindex")
}

// artindexBuildOptions reads the two developer knobs on the index build from
// the environment. Both are unset for anyone who installed hoard, which is
// the point: the default build streams and discards exactly as it always
// has, and only someone deliberately iterating on the hash footprint stores
// ten gigabytes of card images.
//
// HOARD_ARTINDEX_CACHE — directory to keep source images in. Point it at
// ./artindex-cache in the working tree, which .gitignore excludes: the images
// are Wizards' copyright and are fetched, never committed. See
// docs/specs/data-licensing.md §6.
//
// HOARD_ARTINDEX_VARIANT — which Scryfall size to fetch (small, normal,
// art_crop, large). Read artindex's variant constants before changing it;
// the choice is coupled to the hash footprint.
//
// Environment rather than flags on purpose: these are not part of hoard's
// command surface, they are a contributor's iteration loop — set them in the
// environment around `hoard artindex build` / `rehash`.
func artindexBuildOptions() artindex.BuildOptions {
	return artindex.BuildOptions{
		CacheDir: os.Getenv("HOARD_ARTINDEX_CACHE"),
		Variant:  os.Getenv("HOARD_ARTINDEX_VARIANT"),
	}
}

// NewCmdArtindex builds `hoard artindex`, whose bare form reports status.
func NewCmdArtindex(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "artindex",
		GroupID: groupCollection,
		Short:   "The scanner's art-identification hash index",
		Example: "hoard artindex [status|build|rehash]",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return runArtindexStatus(a.env)
		},
	}
	cmd.AddCommand(
		&cobra.Command{
			Use: "status", Short: "How much of the catalog has been hashed",
			Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				return runArtindexStatus(a.env)
			},
		},
		&cobra.Command{
			Use: "build", Short: "Hash the printings not yet indexed (resumable)",
			Args: cobra.NoArgs,
			RunE: func(c *cobra.Command, _ []string) error {
				return runArtindexBuild(c.Context(), a.env)
			},
		},
		&cobra.Command{
			Use: "rehash", Short: "Re-derive every hash from the local image cache",
			Long: "build deliberately skips rows it already has, which makes it the\n" +
				"wrong verb after the hash footprint itself changes. rehash re-derives\n" +
				"every hash from the cached images instead, so iterating on the\n" +
				"footprint costs a disk read rather than a three-hour refetch.",
			Args: cobra.NoArgs,
			RunE: func(c *cobra.Command, _ []string) error {
				return runArtindexRehash(c.Context(), a.env)
			},
		},
	)
	return cmd
}

func runArtindexStatus(env *cli.Env) error {
	ix, err := artindex.Open(artindexDir())
	if err != nil {
		return fmt.Errorf("opening the art index: %w", err)
	}
	defer ix.Close()

	total := 0
	if cat := openCatalog(); cat != nil {
		if srcs, err := cat.ImageSources(); err == nil {
			total = len(srcs)
		}
		cat.Close()
	}
	rep := env.Report()
	rep.Progress("Art index: %s of %s printings hashed.",
		ui.Count(ix.Count()), ui.Count(total))
	// The source image is not cosmetic: hashes taken from art_crop cannot
	// be compared against a whole-card scanner flatten, and the two look
	// identical from the outside. Say which one built this index.
	if v := ix.Variant(); v != "" {
		rep.Progress("Hashed from Scryfall's %s images.", v)
	}
	if ix.Count() < total {
		rep.Progress("Run `hoard artindex build` to continue — the build is resumable.")
	}
	return nil
}

func runArtindexBuild(ctx context.Context, env *cli.Env) error {
	ix, err := artindex.Open(artindexDir())
	if err != nil {
		return fmt.Errorf("opening the art index: %w", err)
	}
	defer ix.Close()

	srcs, err := artindexSources()
	if err != nil {
		return err
	}
	if len(srcs) == 0 {
		return fmt.Errorf("the catalog has no image URLs — run `hoard catalog update` (schema 5+) first")
	}

	before := ix.Count()
	err = ix.Build(ctx, srcs, artindexBuildOptions(), func(done, total int) {
		if done%500 == 0 || done == total {
			fmt.Fprintf(env.Err, "  hashing images: %d/%d\r", done, total)
		}
	})
	fmt.Fprintln(env.Err)
	if err != nil {
		return err
	}
	env.Report().Progress("Art index ready: %s printings hashed (%s new).",
		ui.Count(ix.Count()), ui.Count(ix.Count()-before))
	return nil
}

func runArtindexRehash(ctx context.Context, env *cli.Env) error {
	opts := artindexBuildOptions()
	if opts.CacheDir == "" {
		return fmt.Errorf("no image cache configured — set HOARD_ARTINDEX_CACHE " +
			"to a directory and run `hoard artindex build` once to populate it")
	}

	ix, err := artindex.Open(artindexDir())
	if err != nil {
		return fmt.Errorf("opening the art index: %w", err)
	}
	defer ix.Close()

	srcs, err := artindexSources()
	if err != nil {
		return err
	}
	skipped, err := ix.Rehash(ctx, srcs, opts, func(done, total int) {
		if done%500 == 0 || done == total {
			fmt.Fprintf(env.Err, "  rehashing images: %d/%d\r", done, total)
		}
	})
	fmt.Fprintln(env.Err)
	if err != nil {
		return err
	}
	rep := env.Report()
	rep.Progress("Art index rehashed: %s printings from the local cache.", ui.Count(ix.Count()))
	if skipped > 0 {
		rep.Progress("%s printings were not cached and kept their old hash — "+
			"run `hoard artindex build` to fetch them.", ui.Count(skipped))
	}
	return nil
}

// artindexSources reads the catalog's image URLs, which both build and rehash
// need before they can touch the index.
func artindexSources() ([]artindex.Source, error) {
	cat := openCatalog()
	if cat == nil {
		return nil, fmt.Errorf("no catalog to read image URLs from — run `hoard catalog update` first")
	}
	srcs, err := cat.ImageSources()
	cat.Close()
	if err != nil {
		return nil, err
	}
	return toSources(srcs), nil
}

func toSources(in []catalog.ImageSource) []artindex.Source {
	out := make([]artindex.Source, len(in))
	for i, s := range in {
		out[i] = artindex.Source{ScryfallID: s.ScryfallID, ImageURI: s.ImageURI,
			SoleFinish: s.SoleFinish}
	}
	return out
}
