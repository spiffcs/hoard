package browse

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
)

func pennyDipModel(t *testing.T) Model {
	t.Helper()
	f := testStore()
	f.dips = []store.TrendRow{
		dipRow("Jeweled Lotus", 92.20, 75.34, 75.34),
		dipRow("Chromatic Star", 0.90, 0.20, 0.20),
	}
	f.momentum = []store.TrendRow{
		momentumRow("Command Tower", 28.28, 82.76, 19),
		momentumRow("Evolving Wilds", 0.10, 0.35, 5),
	}
	return showDipView(t, atAllCards(t, newTestModel(t, f)))
}

func runByID(t *testing.T, m Model, id string) Model {
	t.Helper()
	for _, c := range m.commands {
		if c.id != id {
			continue
		}
		if !c.applies(&m) {
			t.Fatalf("command %q does not apply on the %s view", id, m.view)
		}
		c.run(&m)
		return m
	}
	t.Fatalf("no command %q", id)
	return m
}

func dipNames(rows []store.TrendRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

func TestDipViewHidesPenniesByDefault(t *testing.T) {
	m := pennyDipModel(t)

	if got := dipNames(m.dips); len(got) != 1 || got[0] != "Jeweled Lotus" {
		t.Errorf("dips = %v, want the $0.20 row gated out", got)
	}
	if got := dipNames(m.momentum); len(got) != 1 || got[0] != "Command Tower" {
		t.Errorf("momentum = %v, want the $0.35 row gated out", got)
	}
	if !strings.Contains(m.status, "penny filter ≤ $1.00") {
		t.Errorf("arrival beat = %q, want the armed filter named", m.status)
	}
	m.status = ""
	if !strings.Contains(m.View(), "penny filter ≤ $1.00") {
		t.Error("the dip view must announce the armed gate on the status line")
	}
}

func TestDipPennyToggleShowsAndHidesBothTables(t *testing.T) {
	m := pennyDipModel(t)

	m = runByID(t, m, "pennies.toggle")
	if len(m.dips) != 2 || len(m.momentum) != 2 {
		t.Fatalf("with the gate off dips=%v momentum=%v, want every row",
			dipNames(m.dips), dipNames(m.momentum))
	}
	if !strings.Contains(m.status, "penny filter ≤ $1.00 off") {
		t.Errorf("status = %q, want the toggle receipt", m.status)
	}

	m = runByID(t, m, "pennies.toggle")
	if len(m.dips) != 1 || len(m.momentum) != 1 {
		t.Fatalf("re-armed dips=%v momentum=%v, want the pennies gone again",
			dipNames(m.dips), dipNames(m.momentum))
	}
	if !strings.Contains(m.status, "penny filter ≤ $1.00 on") {
		t.Errorf("status = %q, want the re-arm receipt", m.status)
	}
}

func TestDipPennyLimitMovesTheLine(t *testing.T) {
	m := pennyDipModel(t)

	m = runByID(t, m, "pennies.limit")
	if m.prompt == nil {
		t.Fatal("SetPennyFilter must open a prompt on the dip view")
	}
	m.prompt.commit(&m, "0.10")
	m.prompt = nil

	if len(m.dips) != 2 || len(m.momentum) != 2 {
		t.Fatalf("at $0.10 dips=%v momentum=%v, want both tables whole",
			dipNames(m.dips), dipNames(m.momentum))
	}

	m = runByID(t, m, "pennies.limit")
	m.prompt.commit(&m, "80")
	m.prompt = nil
	if len(m.dips) != 0 {
		t.Errorf("at $80 dips = %v, want the $75.34 row gated with the rest", dipNames(m.dips))
	}
	if len(m.momentum) != 1 || dipNames(m.momentum)[0] != "Command Tower" {
		t.Errorf("at $80 momentum = %v, want only the $82.76 row above the line", dipNames(m.momentum))
	}
}
