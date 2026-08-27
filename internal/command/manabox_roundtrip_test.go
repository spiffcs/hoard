package command

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

const manaboxRoundTripFixture = "../collsource/testdata/manabox-roundtrip.csv"

var manaboxGradeAfterFolding = map[string]string{
	"near_mint":    "near_mint",
	"excellent":    "near_mint",
	"good":         "good",
	"light_played": "light_played",
	"played":       "played",
	"poor":         "poor",
}

type manaboxCopy struct {
	Binder     string
	Name       string
	Set        string
	Number     string
	Foil       string
	ScryfallID string
	Condition  string
	Language   string
	Price      float64
	Currency   string
}

func readManaboxCopies(t *testing.T, in string) []manaboxCopy {
	t.Helper()
	recs, err := csv.NewReader(strings.NewReader(in)).ReadAll()
	if err != nil {
		t.Fatalf("parsing manabox csv: %v\n%s", err, in)
	}
	if len(recs) == 0 {
		t.Fatalf("manabox csv had no header:\n%s", in)
	}
	col := map[string]int{}
	for i, h := range recs[0] {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, want := range []string{
		"binder name", "name", "set code", "collector number", "foil", "quantity",
		"scryfall id", "purchase price", "condition", "language", "purchase price currency",
	} {
		if _, ok := col[want]; !ok {
			t.Fatalf("manabox csv is missing the %q column; header = %v", want, recs[0])
		}
	}
	get := func(rec []string, name string) string {
		return strings.TrimSpace(rec[col[name]])
	}

	var out []manaboxCopy
	for _, rec := range recs[1:] {
		qty, err := strconv.Atoi(get(rec, "quantity"))
		if err != nil {
			t.Fatalf("quantity %q: %v", get(rec, "quantity"), err)
		}
		price := 0.0
		if raw := get(rec, "purchase price"); raw != "" {
			price, err = strconv.ParseFloat(strings.TrimPrefix(raw, "$"), 64)
			if err != nil {
				t.Fatalf("purchase price %q: %v", raw, err)
			}
		}
		c := manaboxCopy{
			Binder:     get(rec, "binder name"),
			Name:       get(rec, "name"),
			Set:        strings.ToUpper(get(rec, "set code")),
			Number:     get(rec, "collector number"),
			Foil:       strings.ToLower(get(rec, "foil")),
			ScryfallID: get(rec, "scryfall id"),
			Condition:  strings.ToLower(get(rec, "condition")),
			Language:   strings.ToLower(get(rec, "language")),
			Price:      price,
			Currency:   strings.ToUpper(get(rec, "purchase price currency")),
		}
		for range qty {
			out = append(out, c)
		}
	}
	sortManaboxCopies(out)
	return out
}

func sortManaboxCopies(cs []manaboxCopy) {
	sort.Slice(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		switch {
		case a.Binder != b.Binder:
			return a.Binder < b.Binder
		case a.Name != b.Name:
			return a.Name < b.Name
		case a.Set != b.Set:
			return a.Set < b.Set
		case a.Number != b.Number:
			return a.Number < b.Number
		case a.Foil != b.Foil:
			return a.Foil < b.Foil
		case a.Condition != b.Condition:
			return a.Condition < b.Condition
		case a.Language != b.Language:
			return a.Language < b.Language
		case a.Currency != b.Currency:
			return a.Currency < b.Currency
		default:
			return a.Price < b.Price
		}
	})
}

func roundTripStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func roundTripCards() []scryfall.Card {
	raw := func(lang string) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"lang": lang})
		return b
	}
	return []scryfall.Card{
		{ID: "sol-id-1", Set: "c21", CollectorNumber: "125", Name: "Sol Ring",
			SetName: "Commander 2021", ScryfallURL: "http://sol",
			PriceUSD: f(2), PriceUSDFoil: f(12.5), Lang: "en",
			Finishes: []string{"nonfoil", "foil"}, Raw: raw("en")},
		{ID: "bolt-id-1", Set: "2x2", CollectorNumber: "117", Name: "Lightning Bolt",
			SetName: "Double Masters 2022", ScryfallURL: "http://bolt",
			PriceUSD: f(1.5), PriceUSDFoil: f(4), Lang: "en",
			Finishes: []string{"nonfoil", "foil"}, Raw: raw("en")},
		{ID: "remora-ja-id", Set: "ice", CollectorNumber: "78", Name: "Mystic Remora",
			SetName: "Ice Age", ScryfallURL: "http://rem",
			PriceUSD: f(5), Lang: "ja",
			Finishes: []string{"nonfoil"}, Raw: raw("ja")},
	}
}

func exportManabox(t *testing.T, st *store.Store) string {
	t.Helper()
	out, err := execCmd(context.Background(), st,
		[]string{"export", "--all", "--format", "manabox"}, false)
	if err != nil {
		t.Fatalf("hoard export --format manabox: %v", err)
	}
	return out
}

func importManaboxInto(t *testing.T, st *store.Store, csvText string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manabox.csv")
	if err := os.WriteFile(path, []byte(csvText), 0o644); err != nil {
		t.Fatalf("writing csv: %v", err)
	}
	if err := importCmd(st, "--preserve-binders", path); err != nil {
		t.Fatalf("hoard import: %v", err)
	}
}

func TestManaBoxRoundTripPreservesEveryCopy(t *testing.T) {
	st := roundTripStore(t)
	stubFetch(t, roundTripCards()...)

	if err := importCmd(st, "--preserve-binders", manaboxRoundTripFixture); err != nil {
		t.Fatalf("hoard import: %v", err)
	}

	got := exportManabox(t, st)

	original, err := os.ReadFile(manaboxRoundTripFixture)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	want := readManaboxCopies(t, string(original))
	for i := range want {
		folded, ok := manaboxGradeAfterFolding[want[i].Condition]
		if !ok {
			t.Fatalf("fixture uses grade %q with no expected folding", want[i].Condition)
		}
		want[i].Condition = folded
	}
	sortManaboxCopies(want)

	have := readManaboxCopies(t, got)

	if len(have) != len(want) {
		t.Fatalf("round trip returned %d copies, want %d\n--- exported ---\n%s",
			len(have), len(want), got)
	}
	for i := range want {
		if have[i] != want[i] {
			t.Errorf("copy %d round-tripped as\n  %+v\nwant\n  %+v", i, have[i], want[i])
		}
	}
}

func TestManaBoxRoundTripIsStableOnASecondPass(t *testing.T) {
	stubFetch(t, roundTripCards()...)

	first := roundTripStore(t)
	if err := importCmd(first, "--preserve-binders", manaboxRoundTripFixture); err != nil {
		t.Fatalf("hoard import: %v", err)
	}
	once := exportManabox(t, first)

	second := roundTripStore(t)
	importManaboxInto(t, second, once)
	twice := exportManabox(t, second)

	if once != twice {
		t.Errorf("a second round trip changed the file; hoard's own grades must be a fixed point\n"+
			"--- first ---\n%s\n--- second ---\n%s", once, twice)
	}
}

func TestManaBoxImportRefusesNonUSDPurchasePrices(t *testing.T) {
	st := roundTripStore(t)
	stubFetch(t, roundTripCards()...)

	in := "Binder Name,Binder Type,Name,Set code,Set name,Collector number,Foil," +
		"Rarity,Quantity,ManaBox ID,Scryfall ID,Purchase price,Misprint,Altered," +
		"Condition,Language,Purchase price currency\n" +
		"Trade Binder,binder,Sol Ring,C21,Commander 2021,125,normal,uncommon,2," +
		"61187,sol-id-1,4.25,false,false,near_mint,en,EUR\n"

	path := filepath.Join(t.TempDir(), "eur.csv")
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatalf("writing csv: %v", err)
	}

	err := importCmd(st, "--preserve-binders", path)
	if err == nil {
		t.Fatal("hoard import accepted a EUR purchase price; it must refuse until " +
			"hoard tracks non-USD prices")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "EUR") {
		t.Errorf("refusal was %q, want it to name the currency it cannot store", err)
	}

	totals, terr := st.CollectionTotals()
	if terr != nil {
		t.Fatalf("CollectionTotals: %v", terr)
	}
	if totals.TotalCopies != 0 {
		t.Errorf("a refused import still wrote %d copies", totals.TotalCopies)
	}
}
