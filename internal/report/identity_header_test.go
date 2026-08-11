package report

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// Two unrelated facts used to head the same column. `hoard binder list` prints
// an ID of "1" — a row id you can type at `guessed --checked <id>` — while
// `hoard movers` printed an ID of "WR", which is a color identity and is not
// an identifier at all. One of them had to give up the header, and it was not
// going to be the one naming a real, typeable row.
func TestIdentityColumnDoesNotShareTheBinderIDHeader(t *testing.T) {
	movers := moversTable(ui.Env{Width: 120, Clamp: true},
		moverSections([]store.PriceChange{{
			Name: "Kessig Wolf Run", SetCode: "isd", CollectorNumber: "232",
			Finish: "nonfoil", Copies: 1, Old: 1.00, New: 2.00,
			ColorIdentity: []string{"R"},
		}}, 10), time.Time{}).Render()

	if !strings.Contains(movers, ui.HeaderIdentity) {
		t.Errorf("movers lost its identity header %q:\n%s", ui.HeaderIdentity, movers)
	}
	if headerOf(movers, "ID") {
		t.Errorf("movers still heads its pips column ID:\n%s", movers)
	}

	// Kessig Wolf Run is the case that rules out heading it COLOR: it is a
	// colorless land whose color identity is red. The cell says R; a column
	// called COLOR would therefore be stating something false about the card.
	if !strings.Contains(movers, "R") {
		t.Errorf("identity pip missing from the row:\n%s", movers)
	}

	// The binder table keeps ID, because there it is one.
	binders := Binders(ui.Env{Width: 120, Clamp: true}, []store.DeckSummary{
		{TotalCopies: 915, Value: 3797.19},
	})
	if !headerOf(binders, "ID") {
		t.Errorf("binder list lost the header for its real row id:\n%s", binders)
	}
}

// Unpriced carries the same pips column and must have moved with it, or the
// two reports disagree about what the column beside the name is called.
func TestUnpricedIdentityHeaderMatchesMovers(t *testing.T) {
	out := Unpriced(ui.Env{Width: 120, Clamp: true}, []store.UnpricedRow{{
		Name: "Sol Ring", SetCode: "c21", CollectorNumber: "263",
		Finish: "nonfoil", Copies: 1, ColorIdentity: []string{},
	}})
	if !strings.Contains(out, ui.HeaderIdentity) {
		t.Errorf("unpriced lost its identity header %q:\n%s", ui.HeaderIdentity, out)
	}
	if headerOf(out, "ID") {
		t.Errorf("unpriced still heads its pips column ID:\n%s", out)
	}
}

// The footer under the unpriced table agrees with its count in three places,
// and one holding of one card used to read "1 copies across 1 cards count as
// $0.00" — three disagreements in eleven words, under a table proving hoard
// knows exactly how many there are.
func TestUnpricedFooterAgreesWithItsCounts(t *testing.T) {
	one := Unpriced(ui.Env{Width: 120, Clamp: true}, []store.UnpricedRow{{
		Name: "Sol Ring", SetCode: "c21", CollectorNumber: "263",
		Finish: "nonfoil", Copies: 1, ColorIdentity: []string{},
	}})
	if !strings.Contains(one, "1 copy across 1 card counts as $0.00.") {
		t.Errorf("singular footer disagrees with its counts:\n%s", one)
	}

	many := Unpriced(ui.Env{Width: 120, Clamp: true}, []store.UnpricedRow{
		{Name: "Sol Ring", SetCode: "c21", CollectorNumber: "263",
			Finish: "nonfoil", Copies: 2, ColorIdentity: []string{}},
		{Name: "Mana Crypt", SetCode: "2xm", CollectorNumber: "270",
			Finish: "nonfoil", Copies: 1, ColorIdentity: []string{}},
	})
	if !strings.Contains(many, "3 copies across 2 cards count as $0.00.") {
		t.Errorf("plural footer disagrees with its counts:\n%s", many)
	}
}

// headerOf reports whether any rendered line carries the given word as a
// standalone header cell. A plain substring search would not do: "ID" is
// inside "IDENTITY", and every one of these tables also renders card names.
func headerOf(rendered, want string) bool {
	for line := range strings.SplitSeq(rendered, "\n") {
		if slices.Contains(strings.Fields(line), want) {
			return true
		}
	}
	return false
}
