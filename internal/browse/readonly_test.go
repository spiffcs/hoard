package browse

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

func TestReadOnlyRefusesEveryEdit(t *testing.T) {
	ro := ReadOnly(testStore())

	for name, err := range map[string]error{
		"AddWatch":        ro.AddWatch("id", "Sol Ring", finish.Nonfoil, ">", 1),
		"RemoveWatch":     ro.RemoveWatch(1),
		"RenameBinder":    ro.RenameBinder(1, "x"),
		"DeleteBinder":    ro.DeleteBinder(1),
		"RestoreHoldings": ro.RestoreHoldings("id", nil),
		"UpsertPrintings": ro.UpsertPrintings(nil),
	} {
		if err == nil {
			t.Errorf("%s succeeded on a read-only store; it must refuse", name)
			continue
		}
		if !strings.Contains(err.Error(), "catalog") {
			t.Errorf("%s error = %q, want it to say why", name, err)
		}
	}

	if _, err := ro.CreateBinder("x"); err == nil {
		t.Error("CreateBinder succeeded on a read-only store")
	}
	if _, err := ro.SetHoldingQuantityIn(1, "id", finish.Nonfoil, "unknown", 3); err == nil {
		t.Error("SetHoldingQuantityIn succeeded on a read-only store")
	}
	if _, err := ro.RemoveFromBinder(1, "id"); err == nil {
		t.Error("RemoveFromBinder succeeded on a read-only store")
	}
	if _, err := ro.RemoveContainer(1); err == nil {
		t.Error("RemoveContainer succeeded on a read-only store")
	}
	if _, err := ro.UpsertDeck(store.DeckMeta{Name: "d"}, nil); err == nil {
		t.Error("UpsertDeck succeeded on a read-only store")
	}
	if _, err := ro.MoveEntry(1, "id", finish.Nonfoil, "unknown", 2, "id"); err == nil {
		t.Error("MoveEntry succeeded on a read-only store")
	}
	if _, err := ro.MoveEntryFinish(1, "id", finish.Nonfoil, finish.Foil, "unknown"); err == nil {
		t.Error("MoveEntryFinish succeeded on a read-only store")
	}
	if _, err := ro.MoveEntryCondition(1, "id", finish.Nonfoil, "unknown", "nm"); err == nil {
		t.Error("MoveEntryCondition succeeded on a read-only store")
	}
}

func TestReadOnlyStillReads(t *testing.T) {
	ro := ReadOnly(testStore())

	if _, err := ro.AllByFinish(); err != nil {
		t.Errorf("AllByFinish: %v", err)
	}
	if _, err := ro.SetsHeld(); err != nil {
		t.Errorf("SetsHeld: %v", err)
	}
	if _, err := ro.ListBinders(); err != nil {
		t.Errorf("ListBinders: %v", err)
	}
	if err := ro.SaveSettings(map[string]string{"market.floor": "5"}); err != nil {
		t.Errorf("SaveSettings: %v — view preferences must still persist", err)
	}
}

func TestBrowseRefusesQuantityEditsOnAReadOnlyStore(t *testing.T) {
	m := newTestModel(t, ReadOnly(testStore()))

	m = key(m, "+")
	if m.status == "" || !m.statusErr {
		t.Fatalf("pressing + reported status %q (err=%v), want a refusal", m.status, m.statusErr)
	}
	if !strings.Contains(m.status, "catalog") {
		t.Errorf("status = %q, want it to name the catalog database as the reason", m.status)
	}
}
