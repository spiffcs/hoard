package action

import (
	"encoding/json"
	"github.com/spiffcs/hoard/internal/finish"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/hoardjson"
)

func TestEmittedDocumentOmitsDetailForUnfetchedPrintings(t *testing.T) {
	st, _ := mergeStore(t, "emit.db")

	fetched := card("elf-id", "dom", "168", "Llanowar Elves", 1.25)
	fetched.Raw = json.RawMessage(`{"rarity":"common",
	  "type_line":"Creature — Elf Druid","set_name":"Dominaria"}`)
	addTo(t, st, fetched, finish.Nonfoil, 3)

	for _, c := range []struct{ id, set, num, name string }{
		{"unf-id", "xxx", "1", "Unfetched Card"},
		{"unf2-id", "yyy", "2", "Also Unfetched"},
	} {
		u := card(c.id, c.set, c.num, c.name, 0.10)
		u.Raw = nil
		addTo(t, st, u, finish.Nonfoil, 1)
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

	if n := strings.Count(out, `"detail"`); n != 1 {
		t.Errorf("want exactly one detail object (the fetched printing), got %d:\n%s", n, out)
	}
	if strings.Contains(out, `"detail": {}`) {
		t.Errorf("an unfetched printing emitted an empty detail object:\n%s", out)
	}
}
