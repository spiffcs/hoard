package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
)

type entrySnapshot struct {
	id        int64
	finish    string
	condition string
	paid      *float64
	quantity  int
}

func entrySnapshots(t *testing.T, s *Store) []entrySnapshot {
	t.Helper()
	rows, err := s.db.Query(
		`SELECT id, finish, condition, purchase_price, quantity FROM card_entries ORDER BY id`)
	if err != nil {
		t.Fatalf("reading entries: %v", err)
	}
	defer rows.Close()
	var out []entrySnapshot
	for rows.Next() {
		var e entrySnapshot
		if err := rows.Scan(&e.id, &e.finish, &e.condition, &e.paid, &e.quantity); err != nil {
			t.Fatalf("scanning entries: %v", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading entries: %v", err)
	}
	return out
}

func TestFinishRepairReportsWithoutTouchingAnything(t *testing.T) {
	s := newTestStore(t)
	cid, err := s.collectionID()
	if err != nil {
		t.Fatalf("collectionID: %v", err)
	}
	if err := s.AddCardFinishPaidTo(cid, unpricedFoil(), finish.Nonfoil, 3, f(4.25)); err != nil {
		t.Fatalf("AddCardFinishPaidTo: %v", err)
	}
	before := entrySnapshots(t, s)
	if len(before) != 1 || before[0].paid == nil {
		t.Fatalf("fixture = %+v, want one costed holding", before)
	}

	fixable, _, err := s.MisfinishedEntries(map[string][]finish.Finish{"ripple-id": {finish.Foil}})
	if err != nil {
		t.Fatalf("MisfinishedEntries: %v", err)
	}

	if len(fixable) != 1 || fixable[0].From != finish.Nonfoil || fixable[0].To != finish.Foil {
		t.Errorf("report = %+v, want the nonfoil-to-foil problem still named", fixable)
	}

	after := entrySnapshots(t, s)
	if len(after) != 1 {
		t.Fatalf("entries after = %+v, want the one holding left alone", after)
	}
	if after[0].id != before[0].id {
		t.Errorf("row id %d became %d: the holding was rewritten", before[0].id, after[0].id)
	}
	if after[0].finish != before[0].finish {
		t.Errorf("finish %q became %q: a report must not move the holding",
			before[0].finish, after[0].finish)
	}
	if after[0].paid == nil {
		t.Fatal("the report erased what the copies cost")
	}
	if *after[0].paid != *before[0].paid {
		t.Errorf("purchase price %v became %v", *before[0].paid, *after[0].paid)
	}
}
