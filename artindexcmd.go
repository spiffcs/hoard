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
		err = ix.Build(ctx, toSources(srcs), func(done, total int) {
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
	default:
		return fmt.Errorf("unknown artindex subcommand %q (want status|build)", sub)
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
