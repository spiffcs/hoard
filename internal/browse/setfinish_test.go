package browse

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func bothFinishes(name, num string, usd, foil float64) scryfall.Card {
	return scryfall.Card{
		ID: name + "-id", Name: name, Set: "eoe", CollectorNumber: num,
		ColorIdentity: []string{"U"},
		Finishes:      []string{"nonfoil", "foil"},
		PriceUSD:      price(usd), PriceUSDFoil: price(foil),
	}
}

func galaxyOnly(name, num string, foil float64) scryfall.Card {
	return scryfall.Card{
		ID: name + "-id", Name: name, Set: "eoe", CollectorNumber: num,
		ColorIdentity: []string{"B"},
		Finishes:      []string{"foil"},
		PromoTypes:    []string{"galaxyfoil"},
		PriceUSDFoil:  price(foil),
	}
}

func stellarStore() *fakeStore {
	return &fakeStore{
		collection: []store.CollectionRow{
			row("Ancient Tomb", "eoe", "1", finish.Foil, 1, 195.31),
		},
		unowned: map[string][]store.UnownedRow{},
	}
}

func stellarModel(t *testing.T) Model {
	t.Helper()
	return eoeModel(t, stellarStore(), WithSetPrints(eoePrints(
		bothFinishes("Ancient Tomb", "1", 133.61, 195.31),
		galaxyOnly("Creeping Tar Pit", "99", 5.85))))
}

func finishOf(t *testing.T, m Model, name string, want finish.Finish) card {
	t.Helper()
	for _, c := range m.filteredCards {
		if c.Name == name && c.Finish == want {
			return c
		}
	}
	t.Fatalf("no %s row for %s in %v", want, name, cardRowKeys(m))
	return card{}
}

func cardRowKeys(m Model) []string {
	out := make([]string, 0, len(m.filteredCards))
	for _, c := range m.filteredCards {
		out = append(out, c.Name+"/"+c.Finish.String())
	}
	return out
}

func TestMissingListHasARowForEveryFinishYouDoNotOwn(t *testing.T) {
	m := key(stellarModel(t), "b")

	got := cardRowKeys(m)
	want := []string{"Ancient Tomb/nonfoil", "Creeping Tar Pit/foil"}
	if len(got) != len(want) {
		t.Fatalf("missing list = %v, want %v — owning the foil of Ancient Tomb "+
			"must not hide the nonfoil you still need", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("missing list = %v, want %v", got, want)
			break
		}
	}
}

func TestMissingRowsShowTheirFinishTheWayOwnedRowsDo(t *testing.T) {
	m := key(stellarModel(t), "b")

	for _, tc := range []struct {
		name string
		fin  finish.Finish
		want string
	}{
		{"Ancient Tomb", finish.Nonfoil, "-"},
		{"Creeping Tar Pit", finish.Foil, "galaxy"},
	} {
		c := finishOf(t, m, tc.name, tc.fin)
		if got := ui.FinishTreated(c.Finish, c.Treatment); got != tc.want {
			t.Errorf("%s renders its finish as %q, want %q — the same cell the owned "+
				"side of this set shows", tc.name, got, tc.want)
		}
	}
}

func TestMissingRowsAreOfferedAtTheirOwnFinishPrice(t *testing.T) {
	m := key(stellarModel(t), "b")

	tomb := finishOf(t, m, "Ancient Tomb", finish.Nonfoil)
	if tomb.Price == nil || *tomb.Price != 133.61 {
		t.Errorf("the nonfoil Ancient Tomb costs %v, want $133.61 — not the foil's price",
			tomb.Price)
	}
	pit := finishOf(t, m, "Creeping Tar Pit", finish.Foil)
	if pit.Price == nil || *pit.Price != 5.85 {
		t.Errorf("the galaxy foil costs %v, want $5.85", pit.Price)
	}
}

func TestSetTallyCountsFinishesNotPrintings(t *testing.T) {
	m := stellarModel(t)

	if _, totals := m.viewHeader(); !strings.Contains(totals, "1/3 owned") {
		t.Errorf("owned totals = %q, want 1/3 owned — three finishes are sold in this "+
			"set and you hold one of them", totals)
	}

	m = key(m, "b")
	if _, totals := m.viewHeader(); !strings.Contains(totals, "2/3 unowned") {
		t.Errorf("unowned totals = %q, want 2/3 unowned", totals)
	}
}

func TestFinishingCostCountsEveryFinishYouStillNeed(t *testing.T) {
	m := key(stellarModel(t), "b")

	if _, totals := m.viewHeader(); !strings.Contains(totals, "$139.46 to finish") {
		t.Errorf("unowned totals = %q, want $139.46 to finish — the $133.61 nonfoil "+
			"Ancient Tomb plus the $5.85 galaxy foil", totals)
	}
}
