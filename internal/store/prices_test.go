package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func TestEtchedIsValuedFromItsOwnPrice(t *testing.T) {
	s := newTestStore(t)
	kenrith := scryfall.Card{
		ID: "kenrith-id", Set: "cmr", CollectorNumber: "332", Name: "Kenrith",
		PriceUSD:       f(1.50),
		PriceUSDFoil:   f(4.00),
		PriceUSDEtched: f(30.00),
		ScryfallURL:    "https://scryfall.com/card/cmr/332",
	}
	if err := s.AddCardFinish(kenrith, finish.Etched, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	totals, err := s.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if totals.Value != 60 {
		t.Errorf("value = %v, want 60 (2 × the etched price), not 8 (the foil one)", totals.Value)
	}

	un, err := s.Unpriced()
	if err != nil {
		t.Fatalf("Unpriced: %v", err)
	}
	if len(un) != 0 {
		t.Errorf("unpriced = %+v, want none", un)
	}
}

func TestEtchedFallsBackToFoilWhenUnsplit(t *testing.T) {
	s := newTestStore(t)
	c := ulamog()
	if err := s.AddCardFinish(c, finish.Etched, 1); err != nil {
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

func TestPriceSourcesCountsEtchedAsPriced(t *testing.T) {
	s := newTestStore(t)
	c := ulamog()
	c.PriceUSDEtched = f(30.00)
	if err := s.AddCardFinish(c, finish.Etched, 1); err != nil {
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

func TestConditionMigrationPreservesHoldings(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddCardFinish(ulamog(), finish.Foil, 1); err != nil {
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

func TestConditionSplitsTheBucket(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 3); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	cid, err := s.collectionID()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, 'ulamog-id', 'nonfoil', 'lp', 'main', 1)
ON CONFLICT(container_id, scryfall_id, finish, condition, board, COALESCE(purchase_price, -1))
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

	totals, err := s.CollectionTotals()
	if err != nil {
		t.Fatal(err)
	}
	if totals.TotalCopies != 4 || totals.Value != 40 {
		t.Errorf("totals = %+v, want 4 copies at the NM price (40)", totals)
	}
}

func TestBoardIsAMutableColumnNotAKey(t *testing.T) {
	s := newTestStore(t)

	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	deckID, err := s.UpsertDeck(DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]Entry{
			{ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Board: "main", Quantity: 4},
			{ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Board: "side", Quantity: 2},
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

func TestValidCondition(t *testing.T) {
	for _, ok := range []string{"unknown", "nm", "lp", "mp", "hp", "dmg"} {
		if err := validCondition(ok); err != nil {
			t.Errorf("validCondition(%q) = %v, want nil", ok, err)
		}
	}

	for _, bad := range []string{"", "NEAR MINT", "near_mint", "excellent", "good", "NM", "poor"} {
		if err := validCondition(bad); err == nil {
			t.Errorf("validCondition(%q) = nil, want an error", bad)
		}
	}
}

func TestConditionSplitsHoldingsButNotPricing(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 3); err != nil {
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

	srcs, err := s.PriceSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 1 || srcs[0].Printings != 1 || srcs[0].Copies != 4 {
		t.Errorf("sources = %+v, want one printing covering 4 copies", srcs)
	}
}

func TestBinderAndSummaryAgreeOnDistinctCards(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddCardFinish(ulamog(), finish.Foil, 1); err != nil {
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

func TestRepairFinishesPreservesOtherConditions(t *testing.T) {
	s := newTestStore(t)

	c := ulamog()
	c.Finishes = []string{"nonfoil"}
	if err := s.AddCardFinish(c, finish.Foil, 2); err != nil {
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

	if _, _, err := s.RepairFinishes(map[string][]finish.Finish{"ulamog-id": {finish.Nonfoil}}); err != nil {
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

func TestMoveEntryCondition(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 3); err != nil {
		t.Fatal(err)
	}
	cid, err := s.collectionID()
	if err != nil {
		t.Fatal(err)
	}

	prev, err := s.MoveEntryCondition(mainRef(cid, "ulamog-id", finish.Nonfoil, ConditionUnknown), ConditionLP)
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

	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatal(err)
	}
	prev, err = s.MoveEntryCondition(mainRef(cid, "ulamog-id", finish.Nonfoil, ConditionUnknown), ConditionLP)
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

func TestMoveEntryConditionToItselfIsANoOp(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 2); err != nil {
		t.Fatal(err)
	}
	cid, _ := s.collectionID()
	if _, err := s.MoveEntryCondition(mainRef(cid, "ulamog-id", finish.Nonfoil,
		ConditionUnknown), ConditionUnknown); err != nil {
		t.Fatalf("no-op move: %v", err)
	}
	totals, _ := s.CollectionTotals()
	if totals.TotalCopies != 2 {
		t.Errorf("copies = %d, want 2 untouched", totals.TotalCopies)
	}
}

func TestMoveEntryFinishKeepsTheCondition(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 2); err != nil {
		t.Fatal(err)
	}
	cid, _ := s.collectionID()
	if _, err := s.MoveEntryCondition(mainRef(cid, "ulamog-id", finish.Nonfoil,
		ConditionUnknown), ConditionMP); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MoveEntryFinish(mainRef(cid, "ulamog-id", finish.Nonfoil, ConditionMP), finish.Foil); err != nil {
		t.Fatalf("MoveEntryFinish: %v", err)
	}
	held, _ := s.BinderByFinish(cid)
	if len(held) != 1 || held[0].Finish != finish.Foil || held[0].Condition != ConditionMP {
		t.Errorf("after finish move = %+v, want foil still mp", held)
	}
}

func TestSetHoldingQuantityAddressesOneCondition(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 3); err != nil {
		t.Fatal(err)
	}
	cid, _ := s.collectionID()
	if _, err := s.db.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, quantity)
VALUES (?, 'ulamog-id', 'nonfoil', 'lp', 'main', 1)`, cid); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetEntryQuantity(mainRef(cid, "ulamog-id", finish.Nonfoil, ConditionUnknown), 0); err != nil {
		t.Fatal(err)
	}
	held, _ := s.BinderByFinish(cid)
	if len(held) != 1 || held[0].Condition != ConditionLP || held[0].Quantity != 1 {
		t.Errorf("after zeroing the unassessed bucket = %+v, want the lp row intact", held)
	}
}
