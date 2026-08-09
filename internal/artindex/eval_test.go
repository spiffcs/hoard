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
	"strconv"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/artindex"
	"github.com/spiffcs/hoard/internal/catalog"
)

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// repoRoot locates the working tree from the package directory rather than
// naming somebody's home directory. `go test` runs with the package as the
// working directory, so the tree is two levels up; HOARD_REPO overrides it for
// a run from anywhere else.
func repoRoot() string {
	if r := os.Getenv("HOARD_REPO"); r != "" {
		return r
	}
	return "../.."
}

// The acceptance gates under evaluation, overridable so Stage C can sweep them
// without editing this file.
//
// The defaults are the values fitted against the 64-bit hash and are WRONG for
// the current 256-bit footprint — every distance below is drawn from a range
// four times wider, so an unswept run reports approximately nothing decisive.
// That is the point of running this: the eval prints per-read distances and
// margins, and the gates get refit from those distributions, with zero
// wrong-printing matches as the bar. See docs/sprint-artmatch-v2.md Stage C.
func evalGates() (maxDistance, minMargin int) {
	maxDistance, minMargin = 10, 8
	if v, err := strconv.Atoi(os.Getenv("HOARD_ARTMATCH_MAX_DISTANCE")); err == nil {
		maxDistance = v
	}
	if v, err := strconv.Atoi(os.Getenv("HOARD_ARTMATCH_MIN_MARGIN")); err == nil {
		minMargin = v
	}
	return maxDistance, minMargin
}

func TestArtMatchEval(t *testing.T) {
	if os.Getenv("HOARD_ARTMATCH_EVAL") == "" {
		t.Skip("set HOARD_ARTMATCH_EVAL=1 to replay the labelled corpus against the full index")
	}
	gateDistance, gateMargin := evalGates()
	fmt.Printf("gates: distance ≤%d, margin ≥%d "+
		"(override with HOARD_ARTMATCH_MAX_DISTANCE / HOARD_ARTMATCH_MIN_MARGIN)\n",
		gateDistance, gateMargin)
	repo := repoRoot()
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
		if best.Distance <= gateDistance && second.Distance-best.Distance >= gateMargin {
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
