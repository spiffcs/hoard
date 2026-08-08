// Offline art-match eval: every labelled capture through the live chain
// (probe --emit-card → pHash → Best) against the full 107k index, scored by
// card name. Prints per-read distances so the acceptance gates can be refit
// from measured distributions.
package artindex_test

import (
	"encoding/csv"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/artindex"
	"github.com/spiffcs/hoard/internal/catalog"
)

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func TestArtMatchEval(t *testing.T) {
	if os.Getenv("HOARD_ARTMATCH_EVAL") == "" {
		t.Skip("set HOARD_ARTMATCH_EVAL=1 to replay the labelled corpus against the full index")
	}
	repo := "/Users/hal/development/hoard"
	cache, _ := os.UserCacheDir()
	ix, err := artindex.Open(filepath.Join(cache, "hoard", "artindex"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	cat, err := catalog.Open(filepath.Join(cache, "hoard", "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	probe := filepath.Join(repo, "bin", "cardkit-probe")

	type entry struct{ id, img, want string }
	var entries []entry
	read := func(path string, imgFor func(map[string]string) string) {
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		r := csv.NewReader(f)
		r.Comma = '\t'
		recs, _ := r.ReadAll()
		head := recs[0]
		for _, rec := range recs[1:] {
			row := map[string]string{}
			for i, h := range head {
				if i < len(rec) {
					row[h] = rec[i]
				}
			}
			if img := imgFor(row); img != "" {
				entries = append(entries, entry{row["id"], img, row["physical"]})
			}
		}
	}
	corpusDir := filepath.Join(repo, "scan", "foil-corpus")
	read(filepath.Join(corpusDir, "labels.tsv"), func(r map[string]string) string {
		p := filepath.Join(corpusDir, "full", r["id"]+".png")
		if _, err := os.Stat(p); err != nil {
			return ""
		}
		return p
	})
	read(filepath.Join(corpusDir, "stills-labels.tsv"), func(r map[string]string) string {
		p := filepath.Join(corpusDir, "stills", r["id"]+".jpg")
		if _, err := os.Stat(p); err != nil {
			return ""
		}
		return p
	})

	crop := filepath.Join(os.TempDir(), "artmatch-eval-crop.png")
	var located, decisive, right, wrongDecisive int
	for _, e := range entries {
		os.Remove(crop)
		exec.Command(probe, "--image", e.img, "--emit-card", crop).Run()
		f, err := os.Open(crop)
		if err != nil {
			fmt.Printf("%-8s NOCARD\n", e.id)
			continue
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			continue
		}
		located++
		best, second := ix.Best(artindex.FromImage(img))
		cards, _ := cat.Cards([]string{best.ScryfallID})
		name := slug(cards[best.ScryfallID].Name)
		ok := name == slug(e.want)
		mark := "  "
		if best.Distance <= 10 && second.Distance-best.Distance >= 8 {
			decisive++
			if ok {
				right++
				mark = "OK"
			} else {
				wrongDecisive++
				mark = "!!WRONG"
			}
		} else if ok {
			mark = "ok-shy" // correct but below the gates
		}
		fmt.Printf("%-8s best=%2d second=%2d margin=%2d %-7s want=%s got=%s (%s/%s)\n",
			e.id, best.Distance, second.Distance, second.Distance-best.Distance,
			mark, e.want, name,
			strings.ToUpper(cards[best.ScryfallID].Set), cards[best.ScryfallID].CollectorNumber)
	}
	fmt.Printf("\n%d labelled, %d located, %d decisive (%d right, %d WRONG)\n",
		len(entries), located, decisive, right, wrongDecisive)
}
