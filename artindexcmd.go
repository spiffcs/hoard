package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spiffcs/hoard/internal/artindex"
	"github.com/spiffcs/hoard/internal/catalog"
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
// HOARD_ARTINDEX_CACHE — directory to keep source images in. `task
// artindex-cache` points it at ./artindex-cache in the working tree, which
// .gitignore excludes: the images are Wizards' copyright and are fetched,
// never committed. See docs/data-licensing.md §6.
//
// HOARD_ARTINDEX_VARIANT — which Scryfall size to fetch (small, normal,
// art_crop, large). Read artindex's variant constants before changing it;
// the choice is coupled to the hash footprint.
func artindexBuildOptions() artindex.BuildOptions {
	return artindex.BuildOptions{
		CacheDir: os.Getenv("HOARD_ARTINDEX_CACHE"),
		Variant:  os.Getenv("HOARD_ARTINDEX_VARIANT"),
	}
}

// cmdArtindex builds and reports the perceptual-hash index of every
// printing's Scryfall image — the scanner's art-identification channel.
func cmdArtindex(ctx context.Context, args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	ix, err := artindex.Open(artindexDir())
	if err != nil {
		return fmt.Errorf("opening the art index: %w", err)
	}
	defer ix.Close()

	switch sub {
	case "", "status":
		cat := openCatalog()
		total := 0
		if cat != nil {
			if srcs, err := cat.ImageSources(); err == nil {
				total = len(srcs)
			}
			cat.Close()
		}
		rep := ui.NewReport()
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
	case "build":
		cat := openCatalog()
		if cat == nil {
			return fmt.Errorf("no catalog to read image URLs from — run `hoard catalog update` first")
		}
		srcs, err := cat.ImageSources()
		cat.Close()
		if err != nil {
			return err
		}
		if len(srcs) == 0 {
			return fmt.Errorf("the catalog has no image URLs — run `hoard catalog update` (schema 5+) first")
		}
		before := ix.Count()
		err = ix.Build(ctx, toSources(srcs), artindexBuildOptions(), func(done, total int) {
			if done%500 == 0 || done == total {
				fmt.Fprintf(os.Stderr, "  hashing images: %d/%d\r", done, total)
			}
		})
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		ui.NewReport().Progress("Art index ready: %s printings hashed (%s new).",
			ui.Count(ix.Count()), ui.Count(ix.Count()-before))
		return nil
	case "rehash":
		// Re-derive every hash from the cached images. This is the loop that
		// makes a footprint change cheap: `build` deliberately skips rows it
		// already has, so it is the wrong verb after the hash itself changes.
		opts := artindexBuildOptions()
		if opts.CacheDir == "" {
			return fmt.Errorf("no image cache configured — set HOARD_ARTINDEX_CACHE " +
				"(or run `task artindex-cache`) and build once to populate it")
		}
		cat := openCatalog()
		if cat == nil {
			return fmt.Errorf("no catalog to read image URLs from — run `hoard catalog update` first")
		}
		srcs, err := cat.ImageSources()
		cat.Close()
		if err != nil {
			return err
		}
		skipped, err := ix.Rehash(ctx, toSources(srcs), opts, func(done, total int) {
			if done%500 == 0 || done == total {
				fmt.Fprintf(os.Stderr, "  rehashing images: %d/%d\r", done, total)
			}
		})
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		rep := ui.NewReport()
		rep.Progress("Art index rehashed: %s printings from the local cache.", ui.Count(ix.Count()))
		if skipped > 0 {
			rep.Progress("%s printings were not cached and kept their old hash — "+
				"run `hoard artindex build` to fetch them.", ui.Count(skipped))
		}
		return nil
	default:
		return fmt.Errorf("unknown artindex subcommand %q (want status|build|rehash)", sub)
	}
}

func toSources(in []catalog.ImageSource) []artindex.Source {
	out := make([]artindex.Source, len(in))
	for i, s := range in {
		out[i] = artindex.Source{ScryfallID: s.ScryfallID, ImageURI: s.ImageURI,
			SoleFinish: s.SoleFinish}
	}
	return out
}
