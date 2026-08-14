package action

import (
	"encoding/json"
	"github.com/spiffcs/hoard/internal/finish"
	"slices"
	"testing"
)

func TestExportRowsCarryDetailOnlyForFetchedPrintings(t *testing.T) {
	st, _ := mergeStore(t, "export.db")

	fetched := card("elf-id", "dom", "168", "Llanowar Elves", 1.25)
	fetched.Raw = json.RawMessage(`{"rarity":"common",
	  "type_line":"Creature — Elf Druid","oracle_text":"{T}: Add {G}.",
	  "power":"1","toughness":"1","set_name":"Dominaria",
	  "released_at":"2018-04-27","artist":"Chris Rahn","layout":"normal",
	  "promo_types":["surgefoil"],"printed_name":"ラノワールのエルフ"}`)
	addTo(t, st, fetched, finish.Nonfoil, 1)

	unfetched := card("unf-id", "xxx", "1", "Unfetched Card", 0.10)
	unfetched.Raw = nil
	addTo(t, st, unfetched, finish.Nonfoil, 1)

	rows, err := Deps{Store: st}.ExportRows("", "")
	if err != nil {
		t.Fatalf("ExportRows: %v", err)
	}

	byID := map[string]int{}
	for i, r := range rows {
		byID[r.ScryfallID] = i
	}
	elf, ok := byID["elf-id"]
	if !ok {
		t.Fatalf("the fetched printing is missing from %d export rows", len(rows))
	}
	unf, ok := byID["unf-id"]
	if !ok {
		t.Fatalf("the unfetched printing is missing from %d export rows", len(rows))
	}

	d := rows[elf].Detail
	if d == nil {
		t.Fatal("a printing with a stored document carries no detail at all")
	}
	if d.Rarity != "common" || d.TypeLine != "Creature — Elf Druid" {
		t.Errorf("detail = %q/%q, want the document's values", d.Rarity, d.TypeLine)
	}

	if !slices.Equal(d.PromoTypes, []string{"surgefoil"}) {
		t.Errorf("PromoTypes = %v, want [surgefoil]", d.PromoTypes)
	}
	if d.PrintedName != "ラノワールのエルフ" {
		t.Errorf("PrintedName = %q, want the Japanese name", d.PrintedName)
	}

	if d.Loyalty != "" {
		t.Errorf("a creature's loyalty = %q, want empty", d.Loyalty)
	}

	if rows[unf].Detail != nil {
		t.Errorf("a printing with no stored document carried detail: %+v", rows[unf].Detail)
	}
}
