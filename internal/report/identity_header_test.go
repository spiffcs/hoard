package report

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func TestIdentityColumnDoesNotShareTheBinderIDHeader(t *testing.T) {
	movers := moversTable(ui.Env{Width: 120, Clamp: true},
		moverSections([]store.PriceChange{{
			Name: "Kessig Wolf Run", SetCode: "isd", CollectorNumber: "232",
			Finish: finish.Nonfoil, Copies: 1, Old: 1.00, New: 2.00,
			ColorIdentity: []string{"R"},
		}}, 10), time.Time{}).Render()

	if !strings.Contains(movers, ui.HeaderIdentity) {
		t.Errorf("movers lost its identity header %q:\n%s", ui.HeaderIdentity, movers)
	}
	if headerOf(movers, "ID") {
		t.Errorf("movers still heads its pips column ID:\n%s", movers)
	}

	if !strings.Contains(movers, "R") {
		t.Errorf("identity pip missing from the row:\n%s", movers)
	}

	binders := Binders(ui.Env{Width: 120, Clamp: true}, []store.DeckSummary{
		{TotalCopies: 915, Value: 3797.19},
	})
	if !headerOf(binders, "ID") {
		t.Errorf("binder list lost the header for its real row id:\n%s", binders)
	}
}

func TestUnpricedIdentityHeaderMatchesMovers(t *testing.T) {
	out := Unpriced(ui.Env{Width: 120, Clamp: true}, []store.UnpricedRow{{
		Name: "Sol Ring", SetCode: "c21", CollectorNumber: "263",
		Finish: finish.Nonfoil, Copies: 1, ColorIdentity: []string{},
	}})
	if !strings.Contains(out, ui.HeaderIdentity) {
		t.Errorf("unpriced lost its identity header %q:\n%s", ui.HeaderIdentity, out)
	}
	if headerOf(out, "ID") {
		t.Errorf("unpriced still heads its pips column ID:\n%s", out)
	}
}

func TestUnpricedFooterAgreesWithItsCounts(t *testing.T) {
	one := Unpriced(ui.Env{Width: 120, Clamp: true}, []store.UnpricedRow{{
		Name: "Sol Ring", SetCode: "c21", CollectorNumber: "263",
		Finish: finish.Nonfoil, Copies: 1, ColorIdentity: []string{},
	}})
	if !strings.Contains(one, "1 copy across 1 card counts as $0.00.") {
		t.Errorf("singular footer disagrees with its counts:\n%s", one)
	}

	many := Unpriced(ui.Env{Width: 120, Clamp: true}, []store.UnpricedRow{
		{Name: "Sol Ring", SetCode: "c21", CollectorNumber: "263",
			Finish: finish.Nonfoil, Copies: 2, ColorIdentity: []string{}},
		{Name: "Mana Crypt", SetCode: "2xm", CollectorNumber: "270",
			Finish: finish.Nonfoil, Copies: 1, ColorIdentity: []string{}},
	})
	if !strings.Contains(many, "3 copies across 2 cards count as $0.00.") {
		t.Errorf("plural footer disagrees with its counts:\n%s", many)
	}
}

func headerOf(rendered, want string) bool {
	for line := range strings.SplitSeq(rendered, "\n") {
		if slices.Contains(strings.Fields(line), want) {
			return true
		}
	}
	return false
}
