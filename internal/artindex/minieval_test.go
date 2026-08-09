package artindex_test

// Mini-validation of the FromCard footprint before committing to a full
// index rebuild: hash the Scryfall small images of every labelled printing
// plus 500 confusers, then run the labelled stills' crops against them.
import (
	"context"
	"fmt"
	"image"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/artindex"
	"github.com/spiffcs/hoard/internal/catalog"
)

func TestMiniFootprintEval(t *testing.T) {
	if os.Getenv("HOARD_MINI_EVAL") == "" {
		t.Skip("gated")
	}
	cache, _ := os.UserCacheDir()
	cat, err := catalog.Open(filepath.Join(cache, "hoard", "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	srcs, err := cat.ImageSources()
	if err != nil {
		t.Fatal(err)
	}
	// Wanted names from the s9 run (known printings) + 500 confusers.
	c := &http.Client{Timeout: 20 * time.Second}
	type row struct {
		id   string
		h    artindex.Hash
		name string
	}
	var idx []row
	added := 0
	byID := map[string]string{}
	fetch := func(url string) (artindex.Hash, error) {
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("User-Agent", "hoard/mini-eval")
		resp, err := c.Do(req)
		if err != nil {
			return artindex.Hash{}, err
		}
		defer resp.Body.Close()
		img, _, err := image.Decode(resp.Body)
		if err != nil {
			return artindex.Hash{}, err
		}
		return artindex.FromCard(img), nil
	}
	cards, _ := cat.Cards(nil)
	_ = cards
	for i, s := range srcs {
		take := i%200 == 0 && added < 520 // spread confusers across the catalog
		if !take {
			continue
		}
		h, err := fetch(s.ImageURI)
		if err != nil {
			continue
		}
		m, _ := cat.Cards([]string{s.ScryfallID})
		idx = append(idx, row{s.ScryfallID, h, m[s.ScryfallID].Name})
		byID[s.ScryfallID] = m[s.ScryfallID].Name
		added++
		time.Sleep(80 * time.Millisecond)
	}
	// Ensure the s9 cards' known printings are present.
	for _, id := range []string{} {
		_ = id
	}
	// Known s9 printings by set/number:
	wantPrints := map[string][2]string{
		"brainsurge": {"mh3", "399"}, "dress-down": {"h2r", "4"}, "meltdown": {"mh3", "418"},
		"victimize": {"mh3", "413"}, "consuming-corruption": {"mh3", "407"},
		"lion-umbra": {"mh3", "426"}, "ornithopter": {"m15", "223"},
		"unstable-amulet": {"mh3", "421"}, "charitable-levy": {"mh3", "390"},
		"unholy-heat": {"h2r", "13"}, "glowrider": {"lgn", "15"},
		"abiding-grace": {"h2r", "1"}, "primal-prayers": {"mh3", "429"},
		"hollow-specter": {"lgn", "75"}, "trap-digger": {"scg", "24"},
		"hard-evidence": {"h2r", "5"},
	}
	for slug, sn := range wantPrints {
		card, err := cat.PrintBySetNumber(context.Background(), sn[0], sn[1])
		if err != nil || card == nil || card.ImageURI == "" {
			t.Logf("no catalog row for %s (%s/%s): %v", slug, sn[0], sn[1], err)
			continue
		}
		h, err := fetch(card.ImageURI)
		if err != nil {
			continue
		}
		idx = append(idx, row{card.ID, h, card.Name})
		byID[card.ID] = card.Name
		time.Sleep(80 * time.Millisecond)
	}
	t.Logf("mini-index: %d entries", len(idx))

	probe := "bin/cardkit-probe"
	if _, err := os.Stat(probe); err != nil {
		probe = "../../bin/cardkit-probe"
	}
	crop := filepath.Join(os.TempDir(), "mini-eval-crop.png")
	stills, _ := filepath.Glob("../../scan/foil-corpus/stills/s9-*.jpg")
	for _, still := range stills {
		os.Remove(crop)
		exec.Command(probe, "--image", still, "--emit-card", crop).Run()
		f, err := os.Open(crop)
		if err != nil {
			continue
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			continue
		}
		h := artindex.FromCard(img)
		b1, b2 := 257, 257 // above the 256-bit maximum
		var n1 string
		for _, r := range idx {
			d := h.Distance(r.h)
			if d < b1 {
				b2, b1, n1 = b1, d, r.name
			} else if d < b2 {
				b2 = d
			}
		}
		fmt.Printf("%-40s best=%2d second=%2d margin=%2d got=%s\n",
			filepath.Base(still), b1, b2, b2-b1, n1)
	}
}
