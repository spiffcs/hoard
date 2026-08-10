package action

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/hoardjson"
)

// The whole path, store to emitted bytes: a printing hoard has never fetched
// must produce no `detail` key at all.
//
// The row-level assertion in TestExportRowsCarryDetailOnlyForFetchedPrintings
// is the precise one; this is the user-visible surface behind it, and it is
// worth holding separately because the failure it catches is quiet. An empty
// `"detail": {}` object is valid against the schema — every field is optional
// — so nothing downstream would reject it, and a consumer reading it would
// conclude the card has no rarity, no type line and no oracle text rather than
// that nobody has looked.
func TestEmittedDocumentOmitsDetailForUnfetchedPrintings(t *testing.T) {
	st, _ := mergeStore(t, "emit.db")

	fetched := card("elf-id", "dom", "168", "Llanowar Elves", 1.25)
	fetched.Raw = json.RawMessage(`{"rarity":"common",
	  "type_line":"Creature — Elf Druid","set_name":"Dominaria"}`)
	addTo(t, st, fetched, "nonfoil", 3)

	// Two of them, so a regression leaking one row's object cannot be masked
	// by the other landing in a different position.
	for _, c := range []struct{ id, set, num, name string }{
		{"unf-id", "xxx", "1", "Unfetched Card"},
		{"unf2-id", "yyy", "2", "Also Unfetched"},
	} {
		u := card(c.id, c.set, c.num, c.name, 0.10)
		u.Raw = nil
		addTo(t, st, u, "nonfoil", 1)
	}

	rows, err := Deps{Store: st}.ExportRows("", "")
	if err != nil {
		t.Fatalf("ExportRows: %v", err)
	}
	var sb strings.Builder
	if err := hoardjson.Write(&sb, hoardjson.FromExportRows(rows)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := sb.String()

	// One fetched printing, so exactly one detail object — never three, and
	// never an empty one.
	if n := strings.Count(out, `"detail"`); n != 1 {
		t.Errorf("want exactly one detail object (the fetched printing), got %d:\n%s", n, out)
	}
	if strings.Contains(out, `"detail": {}`) {
		t.Errorf("an unfetched printing emitted an empty detail object:\n%s", out)
	}
}
