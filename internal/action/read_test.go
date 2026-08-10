package action

import (
	"encoding/json"
	"slices"
	"testing"
)

// The export path is where store.CardDetailsInContainer's map-absence becomes
// the row's nil Detail, and that translation carries the whole of the format's
// "nobody has looked" signal — inside the object, an empty field means only
// that the card has no such value.
//
// Nothing else exercises the seam: the hoardjson tests build export.Row values
// directly, so they never call detailFor and would keep passing if it started
// handing back a zero CardDetail for a printing hoard has never fetched. This
// covers it from the store outwards.
//
// It also pins the two fields folded in from the retired CardFacts type,
// PromoTypes and PrintedName, which no other test reads through the
// container-scoped query.
func TestExportRowsCarryDetailOnlyForFetchedPrintings(t *testing.T) {
	st, _ := mergeStore(t, "export.db")

	fetched := card("elf-id", "dom", "168", "Llanowar Elves", 1.25)
	fetched.Raw = json.RawMessage(`{"rarity":"common",
	  "type_line":"Creature — Elf Druid","oracle_text":"{T}: Add {G}.",
	  "power":"1","toughness":"1","set_name":"Dominaria",
	  "released_at":"2018-04-27","artist":"Chris Rahn","layout":"normal",
	  "promo_types":["surgefoil"],"printed_name":"ラノワールのエルフ"}`)
	addTo(t, st, fetched, "nonfoil", 1)

	// Same shape, no document at all.
	unfetched := card("unf-id", "xxx", "1", "Unfetched Card", 0.10)
	unfetched.Raw = nil
	addTo(t, st, unfetched, "nonfoil", 1)

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
	// The two fields folded in from CardFacts. They reach a row only through
	// the container-scoped query, so a projection that dropped them would
	// show up here and nowhere else.
	if !slices.Equal(d.PromoTypes, []string{"surgefoil"}) {
		t.Errorf("PromoTypes = %v, want [surgefoil]", d.PromoTypes)
	}
	if d.PrintedName != "ラノワールのエルフ" {
		t.Errorf("PrintedName = %q, want the Japanese name", d.PrintedName)
	}
	// A field the document does not mention: empty, because the card has no
	// such value — NOT because nobody looked.
	if d.Loyalty != "" {
		t.Errorf("a creature's loyalty = %q, want empty", d.Loyalty)
	}

	// And the whole object absent for the printing nobody has fetched. This is
	// the assertion that fails if detailFor ever stops distinguishing a missing
	// map key from a present one.
	if rows[unf].Detail != nil {
		t.Errorf("a printing with no stored document carried detail: %+v", rows[unf].Detail)
	}
}
