package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// Etched stopped borrowing the foil price in v21. A card whose etched product
// trades well away from its foil one was carried at the foil number in every
// total hoard reports, while the comps sheet on the same screen read the
// vendors' own etched bucket — the portfolio was the last place still folding
// the two together.
func TestEtchedIsValuedFromItsOwnPrice(t *testing.T) {
	s := newTestStore(t)
	kenrith := scryfall.Card{
		ID: "kenrith-id", Set: "cmr", CollectorNumber: "332", Name: "Kenrith",
		PriceUSD:       f(1.50),
		PriceUSDFoil:   f(4.00),
		PriceUSDEtched: f(30.00),
		ScryfallURL:    "https://scryfall.com/card/cmr/332",
	}
	if err := s.AddCardFinish(kenrith, "etched", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	totals, err := s.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if totals.Value != 60 {
		t.Errorf("value = %v, want 60 (2 × the etched price), not 8 (the foil one)", totals.Value)
	}

	// Nothing is unpriced: the etched column answers for the etched copy.
	un, err := s.Unpriced()
	if err != nil {
		t.Fatalf("Unpriced: %v", err)
	}
	if len(un) != 0 {
		t.Errorf("unpriced = %+v, want none", un)
	}
}

// Not every source splits the product, so an etched holding of a printing the
// feed prices only as a foil must keep reading the foil column — the fallback
// is what makes v21 a strict improvement rather than a new way to read $0.00.
func TestEtchedFallsBackToFoilWhenUnsplit(t *testing.T) {
	s := newTestStore(t)
	c := ulamog() // foil 25.00, no etched figure
	if err := s.AddCardFinish(c, "etched", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	totals, err := s.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if totals.Value != 25 {
		t.Errorf("value = %v, want the foil price 25 as the fallback", totals.Value)
	}
	un, err := s.Unpriced()
	if err != nil {
		t.Fatalf("Unpriced: %v", err)
	}
	if len(un) != 0 {
		t.Errorf("unpriced = %+v, want none: the foil fallback prices it", un)
	}
}

// PriceSources classifies each holding exactly as entryValue prices it, so the
// coverage breakdown and the total it explains cannot disagree.
func TestPriceSourcesCountsEtchedAsPriced(t *testing.T) {
	s := newTestStore(t)
	c := ulamog()
	c.PriceUSDEtched = f(30.00)
	if err := s.AddCardFinish(c, "etched", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	srcs, err := s.PriceSources()
	if err != nil {
		t.Fatalf("PriceSources: %v", err)
	}
	if len(srcs) != 1 || srcs[0].Source != "scryfall" || srcs[0].Printings != 1 {
		t.Errorf("sources = %+v, want one scryfall-priced printing", srcs)
	}
}

// The v23 rebuild is the one migration that touches the table nothing can
// re-download, so what it must not do is worth pinning: every row survives,
// with its quantity, and nothing is silently given a condition.
func TestConditionMigrationPreservesHoldings(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddCardFinish(ulamog(), "foil", 1); err != nil {
		t.Fatalf("AddCardFinish foil: %v", err)
	}
	rows, err := s.db.Query(`SELECT finish, condition, board, quantity FROM card_entries ORDER BY finish`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var got [][4]any
	for rows.Next() {
		var f, c, b string
		var q int
		if err := rows.Scan(&f, &c, &b, &q); err != nil {
			t.Fatal(err)
		}
		got = append(got, [4]any{f, c, b, q})
	}
	want := [][4]any{{"foil", "unknown", "main", 1}, {"nonfoil", "unknown", "main", 2}}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// A condition splits a bucket rather than merging into it. This is the whole point
// of the rebuild: before it, two conditions of one printing had nowhere to live
// but the same row.
func TestConditionSplitsTheBucket(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 3); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	cid, err := s.collectionID()
	if err != nil {
		t.Fatal(err)
	}
	// One played copy, alongside the three nobody has assessed.
	if _, err := s.db.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, 'ulamog-id', 'nonfoil', 'lp', 'main', 1)
ON CONFLICT(container_id, scryfall_id, finish, condition, board)
DO UPDATE SET quantity = quantity + excluded.quantity`, cid); err != nil {
		t.Fatalf("insert played copy: %v", err)
	}
	var rows, copies int
	if err := s.db.QueryRow(
		`SELECT COUNT(*), SUM(quantity) FROM card_entries`).Scan(&rows, &copies); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || copies != 4 {
		t.Errorf("got %d rows / %d copies, want 2 rows (unassessed + lp) totalling 4", rows, copies)
	}
	// Totals still see all four: condition does not change what a card is worth,
	// because no source hoard reads publishes a per-condition price.
	totals, err := s.CollectionTotals()
	if err != nil {
		t.Fatal(err)
	}
	if totals.TotalCopies != 4 || totals.Value != 40 {
		t.Errorf("totals = %+v, want 4 copies at the NM price (40)", totals)
	}
}

// Board is no longer part of the key, so a card can sit in a deck's main and
// side at once — which Archidekt and the text importer both produce — and a
// move between them is an ordinary update of an ordinary column.
func TestBoardIsAMutableColumnNotAKey(t *testing.T) {
	s := newTestStore(t)
	// UpsertDeck places entries but does not create printings; the card has to
	// be in the catalog for the foreign key to hold.
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	deckID, err := s.UpsertDeck(DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]Entry{
			{ScryfallID: "ulamog-id", Finish: "nonfoil", Board: "main", Quantity: 4},
			{ScryfallID: "ulamog-id", Finish: "nonfoil", Board: "side", Quantity: 2},
		})
	if err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM card_entries WHERE container_id = ?`, deckID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("got %d rows, want main and side kept apart", n)
	}
	// The move the surrogate key exists for: one statement, addressing the row
	// by its own identity rather than restating four columns.
	var id int64
	if err := s.db.QueryRow(
		`SELECT id FROM card_entries WHERE container_id = ? AND board = 'side'`, deckID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE card_entries SET board = 'maybe' WHERE id = ?`, id); err != nil {
		t.Fatalf("moving by id: %v", err)
	}
	var board string
	if err := s.db.QueryRow(`SELECT board FROM card_entries WHERE id = ?`, id).Scan(&board); err != nil {
		t.Fatal(err)
	}
	if board != "maybe" {
		t.Errorf("board = %q, want maybe", board)
	}
}

// The vocabulary gate, so a writer added later cannot admit a condition the rest of
// the schema disagrees with — the same contract validFinish has.
func TestValidCondition(t *testing.T) {
	for _, ok := range []string{"unknown", "nm", "lp", "mp", "hp", "dmg"} {
		if err := validCondition(ok); err != nil {
			t.Errorf("validCondition(%q) = %v, want nil", ok, err)
		}
	}
	// MTGJSON's spelling, Cardmarket's values, and a plausible invention all
	// have to be normalized before they reach the store, not after.
	for _, bad := range []string{"", "NEAR MINT", "near_mint", "excellent", "good", "NM", "poor"} {
		if err := validCondition(bad); err == nil {
			t.Errorf("validCondition(%q) = nil, want an error", bad)
		}
	}
}

// The rule E2 turns on: views about *what you own* split by condition, views about
// *what a card is worth* do not. Pinned because both halves are easy to "fix"
// in the wrong direction later.
func TestConditionSplitsHoldingsButNotPricing(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 3); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	cid, err := s.collectionID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, 'ulamog-id', 'nonfoil', 'lp', 'main', 1)`, cid); err != nil {
		t.Fatalf("insert played copy: %v", err)
	}

	// What you own: two rows, one per condition.
	held, err := s.BinderByFinish(cid)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 2 {
		t.Fatalf("BinderByFinish returned %d rows, want one per condition: %+v", len(held), held)
	}
	byCondition := map[string]int{}
	for _, h := range held {
		byCondition[h.Condition] = h.Quantity
	}
	if byCondition["unknown"] != 3 || byCondition["lp"] != 1 {
		t.Errorf("by condition = %v, want 3 unassessed and 1 lp", byCondition)
	}

	// What it is worth: one row. No source prices by condition, so splitting
	// here would show the same number twice and imply otherwise.
	owned, err := s.OwnedByFinish()
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 1 {
		t.Fatalf("OwnedByFinish returned %d rows, want one: %+v", len(owned), owned)
	}
	if owned[0].Copies != 4 {
		t.Errorf("copies = %d, want all 4 counted together", owned[0].Copies)
	}

	// And the coverage breakdown counts the printing once, not once per condition.
	srcs, err := s.PriceSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 1 || srcs[0].Printings != 1 || srcs[0].Copies != 4 {
		t.Errorf("sources = %+v, want one printing covering 4 copies", srcs)
	}
}

// "Distinct cards" means distinct printings, which is what CollectionTotals has
// always reported and what the JSON model documents. ListBinders counted rows
// instead, so a card held in two finishes counted twice and the same binder read
// 194 in the list and 190 in the summary. Grades would have widened it again.
func TestBinderAndSummaryAgreeOnDistinctCards(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddCardFinish(ulamog(), "foil", 1); err != nil {
		t.Fatal(err)
	}
	cid, err := s.collectionID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, 'ulamog-id', 'nonfoil', 'lp', 'main', 1)`, cid); err != nil {
		t.Fatal(err)
	}

	totals, err := s.CollectionTotals()
	if err != nil {
		t.Fatal(err)
	}
	binders, err := s.ListBinders()
	if err != nil {
		t.Fatal(err)
	}
	if len(binders) != 1 {
		t.Fatalf("got %d binders, want 1", len(binders))
	}
	if binders[0].DistinctCards != totals.DistinctCards {
		t.Errorf("ListBinders says %d distinct cards, CollectionTotals says %d — they must agree",
			binders[0].DistinctCards, totals.DistinctCards)
	}
	if totals.DistinctCards != 1 {
		t.Errorf("distinct cards = %d, want 1: three rows, one printing", totals.DistinctCards)
	}
}

// Repairing a finish must not disturb a condition, and must not delete the
// other condition buckets of the same card. The old DELETE matched on
// (container, card, finish, board), which since v23 names all of them.
func TestRepairFinishesPreservesOtherConditions(t *testing.T) {
	s := newTestStore(t)
	// A printing that comes only in nonfoil, recorded as foil in two conditions.
	c := ulamog()
	c.Finishes = []string{"nonfoil"}
	if err := s.AddCardFinish(c, "foil", 2); err != nil {
		t.Fatal(err)
	}
	cid, err := s.collectionID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, 'ulamog-id', 'foil', 'lp', 'main', 1)`, cid); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.RepairFinishes(map[string][]string{"ulamog-id": {"nonfoil"}}); err != nil {
		t.Fatalf("RepairFinishes: %v", err)
	}

	rows, err := s.db.Query(
		`SELECT finish, condition, quantity FROM card_entries ORDER BY condition`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var f, cond string
		var q int
		if err := rows.Scan(&f, &cond, &q); err != nil {
			t.Fatal(err)
		}
		if f != "nonfoil" {
			t.Errorf("finish = %q, want every row repaired to nonfoil", f)
		}
		got[cond] = q
	}
	if got["unknown"] != 2 || got["lp"] != 1 {
		t.Errorf("after repair = %v, want both conditions kept (2 unassessed, 1 lp)", got)
	}
}

// MoveEntryCondition is the mirror of MoveEntryFinish, and the only way an
// assessment enters the hoard by hand. It merges into an existing bucket rather
// than colliding with it, and reports what it merged into so an undo can put
// both sides back.
func TestMoveEntryCondition(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 3); err != nil {
		t.Fatal(err)
	}
	cid, err := s.collectionID()
	if err != nil {
		t.Fatal(err)
	}

	// Three unassessed copies; two of them turn out to be lightly played.
	prev, err := s.MoveEntryCondition(cid, "ulamog-id", "nonfoil", ConditionUnknown, ConditionLP)
	if err != nil {
		t.Fatalf("MoveEntryCondition: %v", err)
	}
	if prev != 0 {
		t.Errorf("prevTarget = %d, want 0: nothing was held as lp", prev)
	}
	held, err := s.BinderByFinish(cid)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 1 || held[0].Condition != ConditionLP || held[0].Quantity != 3 {
		t.Fatalf("after move = %+v, want all three as lp", held)
	}

	// Add an unassessed copy back, then merge it in: the quantities sum, and
	// the previous target quantity comes back for the undo.
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatal(err)
	}
	prev, err = s.MoveEntryCondition(cid, "ulamog-id", "nonfoil", ConditionUnknown, ConditionLP)
	if err != nil {
		t.Fatal(err)
	}
	if prev != 3 {
		t.Errorf("prevTarget = %d, want 3 — what the undo has to restore", prev)
	}
	held, _ = s.BinderByFinish(cid)
	if len(held) != 1 || held[0].Quantity != 4 {
		t.Errorf("after merge = %+v, want one lp row of 4", held)
	}
}

// A move to the condition already held is a no-op, not an error and not a
// delete — the same contract MoveEntryFinish has.
func TestMoveEntryConditionToItselfIsANoOp(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
		t.Fatal(err)
	}
	cid, _ := s.collectionID()
	if _, err := s.MoveEntryCondition(cid, "ulamog-id", "nonfoil",
		ConditionUnknown, ConditionUnknown); err != nil {
		t.Fatalf("no-op move: %v", err)
	}
	totals, _ := s.CollectionTotals()
	if totals.TotalCopies != 2 {
		t.Errorf("copies = %d, want 2 untouched", totals.TotalCopies)
	}
}

// Correcting a finish must carry the condition across rather than resetting it:
// what a card is made of says nothing about how worn it is.
func TestMoveEntryFinishKeepsTheCondition(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
		t.Fatal(err)
	}
	cid, _ := s.collectionID()
	if _, err := s.MoveEntryCondition(cid, "ulamog-id", "nonfoil",
		ConditionUnknown, ConditionMP); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MoveEntryFinish(cid, "ulamog-id", "nonfoil", "foil", ConditionMP); err != nil {
		t.Fatalf("MoveEntryFinish: %v", err)
	}
	held, _ := s.BinderByFinish(cid)
	if len(held) != 1 || held[0].Finish != "foil" || held[0].Condition != ConditionMP {
		t.Errorf("after finish move = %+v, want foil still mp", held)
	}
}

// Setting a quantity addresses one condition bucket, not every bucket of that
// finish. Before condition joined the signature this deleted both.
func TestSetHoldingQuantityAddressesOneCondition(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 3); err != nil {
		t.Fatal(err)
	}
	cid, _ := s.collectionID()
	if _, err := s.db.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, 'ulamog-id', 'nonfoil', 'lp', 'main', 1)`, cid); err != nil {
		t.Fatal(err)
	}

	// Zero out the unassessed bucket; the lp one must survive.
	if _, err := s.SetHoldingQuantityIn(cid, "ulamog-id", "nonfoil", ConditionUnknown, 0); err != nil {
		t.Fatal(err)
	}
	held, _ := s.BinderByFinish(cid)
	if len(held) != 1 || held[0].Condition != ConditionLP || held[0].Quantity != 1 {
		t.Errorf("after zeroing the unassessed bucket = %+v, want the lp row intact", held)
	}
}
