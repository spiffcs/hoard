package store

import (
	"strings"
	"testing"
)

// A fresh database has exactly one binder — the default — listed first and
// answering to the display name, not its stored row name.
func TestListBindersStartsWithTheDefault(t *testing.T) {
	s := newTestStore(t)
	bs, err := s.ListBinders()
	if err != nil {
		t.Fatalf("ListBinders: %v", err)
	}
	if len(bs) != 1 || bs[0].Name != LooseName {
		t.Fatalf("binders = %+v, want just %q", bs, LooseName)
	}
	if bs[0].Kind != KindCollection {
		t.Errorf("kind = %q", bs[0].Kind)
	}
}

func TestCreateBinderAndAddTo(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateBinder("Trade Stock")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := s.AddCardFinishTo(id, ulamog(), "nonfoil", 3); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}
	// The default binder is untouched; the new one holds the cards.
	rows, err := s.ListCollectionByFinish()
	if err != nil {
		t.Fatalf("ListCollectionByFinish: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("default binder rows = %+v, want none", rows)
	}
	got, err := s.BinderByFinish(id)
	if err != nil {
		t.Fatalf("BinderByFinish: %v", err)
	}
	if len(got) != 1 || got[0].Quantity != 3 {
		t.Errorf("binder rows = %+v, want 3 copies", got)
	}
	// Totals span every binder: the summary's BINDER line means "not in a deck".
	tot, err := s.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if tot.TotalCopies != 3 || tot.Value != 30 {
		t.Errorf("totals = %+v, want 3 copies worth $30", tot)
	}
	// And the listing shows both, default first.
	bs, _ := s.ListBinders()
	if len(bs) != 2 || bs[0].Name != LooseName || bs[1].Name != "Trade Stock" {
		t.Errorf("binders = %+v", bs)
	}
	if bs[1].Value != 30 {
		t.Errorf("new binder value = %v, want 30", bs[1].Value)
	}
}

// Binder references resolve like deck references: id, exact name, fragment.
func TestBinderByRef(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateBinder("Trade Stock")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	for _, ref := range []string{"Trade Stock", "trade", "Stock"} {
		c, err := s.BinderByRef(ref)
		if err != nil {
			t.Fatalf("BinderByRef(%q): %v", ref, err)
		}
		if c.ID != id {
			t.Errorf("BinderByRef(%q) = %d, want %d", ref, c.ID, id)
		}
	}
	// The default answers to its display name.
	c, err := s.BinderByRef(LooseName)
	if err != nil {
		t.Fatalf("BinderByRef(%q): %v", LooseName, err)
	}
	if !IsDefaultBinder(*c) {
		t.Errorf("BinderByRef(%q) did not find the default", LooseName)
	}
	// A deck must not be reachable as a binder.
	if _, err := s.UpsertDeck(DeckMeta{Name: "Some Deck", Source: "manual", SourceID: "d1"}, nil); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	if _, err := s.BinderByRef("Some Deck"); err == nil {
		t.Error("a deck resolved as a binder")
	}
}

func TestCreateBinderRejectsCollisions(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateBinder("Trade"); err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	for _, name := range []string{"Trade", "trade", LooseName, "  "} {
		if _, err := s.CreateBinder(name); err == nil {
			t.Errorf("CreateBinder(%q) succeeded, want a refusal", name)
		}
	}
}

func TestRenameBinder(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateBinder("Trade")
	if err := s.RenameBinder(id, "For Sale"); err != nil {
		t.Fatalf("RenameBinder: %v", err)
	}
	if c, err := s.BinderByRef("For Sale"); err != nil || c.ID != id {
		t.Errorf("renamed binder not found: %v", err)
	}
	// The default keeps its fixed name.
	def, _ := s.BinderByRef(LooseName)
	if err := s.RenameBinder(def.ID, "Main"); err == nil {
		t.Error("renaming the default binder succeeded")
	}
}

func TestDeleteBinderRefusesDefaultAndNonEmpty(t *testing.T) {
	s := newTestStore(t)
	def, _ := s.BinderByRef(LooseName)
	if err := s.DeleteBinder(def.ID); err == nil {
		t.Error("deleting the default binder succeeded")
	}

	id, _ := s.CreateBinder("Trade")
	if err := s.AddCardFinishTo(id, ulamog(), "nonfoil", 2); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}
	err := s.DeleteBinder(id)
	if err == nil || !strings.Contains(err.Error(), "2 cards") {
		t.Errorf("DeleteBinder on a full binder = %v, want a refusal naming the count", err)
	}
	if _, err := s.RemoveFromBinder(id, "ulamog-id"); err != nil {
		t.Fatalf("RemoveFromBinder: %v", err)
	}
	if err := s.DeleteBinder(id); err != nil {
		t.Errorf("DeleteBinder on an emptied binder: %v", err)
	}
	if bs, _ := s.ListBinders(); len(bs) != 1 {
		t.Errorf("binders after delete = %+v", bs)
	}
}

// Per-binder edits touch only their binder.
func TestSetHoldingQuantityInIsScoped(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateBinder("Trade")
	if err := s.AddCardFinish(ulamog(), "nonfoil", 4); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddCardFinishTo(id, ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}
	prev, err := s.SetHoldingQuantityIn(id, "ulamog-id", "nonfoil", 5)
	if err != nil {
		t.Fatalf("SetHoldingQuantityIn: %v", err)
	}
	if prev != 1 {
		t.Errorf("previous = %d, want 1", prev)
	}
	if got := heldByFinish(t, s, "ulamog-id"); got["nonfoil"] != 4 {
		t.Errorf("default binder = %+v, want its 4 copies untouched", got)
	}
	rows, _ := s.BinderByFinish(id)
	if len(rows) != 1 || rows[0].Quantity != 5 {
		t.Errorf("trade binder rows = %+v, want 5", rows)
	}
}

// AllByFinish is the whole hoard in one list: the same printing held in a
// binder and a deck merges into one row with the quantities summed.
func TestAllByFinishMergesContainers(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if _, err := s.UpsertDeck(
		DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]Entry{
			{ScryfallID: "ulamog-id", Finish: "nonfoil", Board: "main", Quantity: 3},
			{ScryfallID: "ulamog-id", Finish: "foil", Board: "main", Quantity: 1},
		}); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}

	rows, err := s.AllByFinish()
	if err != nil {
		t.Fatalf("AllByFinish: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want one per finish", rows)
	}
	byFinish := map[string]int{}
	for _, r := range rows {
		byFinish[r.Finish] = r.Quantity
	}
	if byFinish["nonfoil"] != 5 || byFinish["foil"] != 1 {
		t.Errorf("quantities = %v, want binder and deck summed (nonfoil 5, foil 1)", byFinish)
	}
}
