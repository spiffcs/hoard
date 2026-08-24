package command

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

func moveStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	sol := scryfall.Card{ID: "sol", Set: "c21", CollectorNumber: "125", Name: "Sol Ring",
		ScryfallURL: "http://x", PriceUSD: f(2)}
	forest := scryfall.Card{ID: "forest", Set: "c21", CollectorNumber: "300", Name: "Forest",
		ScryfallURL: "http://x", PriceUSD: f(0.10)}
	remora := scryfall.Card{ID: "rem", Set: "ice", CollectorNumber: "78",
		Name: "Mystic Remora", ScryfallURL: "http://y"}
	if err := st.UpsertPrintings([]scryfall.Card{sol, forest, remora}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if err := st.AddCardFinish(sol, finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := st.AddCardFinish(forest, finish.Nonfoil, 5); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if _, err := st.CreateBinder("bulk"); err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if _, err := st.UpsertDeck(store.DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]store.Entry{{ScryfallID: "rem", Finish: finish.Nonfoil, Board: "main", Quantity: 1}}); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	return st
}

func binderContents(t *testing.T, st *store.Store, ref string) map[string]int {
	t.Helper()
	b, err := st.BinderByRef(ref)
	if err != nil {
		t.Fatalf("BinderByRef(%q): %v", ref, err)
	}
	rows, err := st.BinderByFinish(b.ID)
	if err != nil {
		t.Fatalf("BinderByFinish: %v", err)
	}
	out := map[string]int{}
	for _, r := range rows {
		out[r.Name] += r.Quantity
	}
	return out
}

func deckContents(t *testing.T, st *store.Store, ref string) map[string]int {
	t.Helper()
	d, err := st.DeckByRef(ref)
	if err != nil {
		t.Fatalf("DeckByRef(%q): %v", ref, err)
	}
	entries, err := st.DeckEntries(d.ID)
	if err != nil {
		t.Fatalf("DeckEntries: %v", err)
	}
	out := map[string]int{}
	for _, e := range entries {
		out[e.Card.Name] += e.Quantity
	}
	return out
}

func holdingsFor(t *testing.T, st *store.Store, args ...string) []byte {
	t.Helper()
	out, err := execCmd(context.Background(), st, append([]string{"export"}, args...), true)
	if err != nil {
		t.Fatalf("hoard export %v --json: %v", args, err)
	}
	return []byte(out)
}

func TestMoveSweepsABinderIntoAnother(t *testing.T) {
	st := moveStore(t)
	doc := holdingsFor(t, st, "--binder", "Binder")

	out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "bulk", "--yes"}, doc)
	if err != nil {
		t.Fatalf("hoard move: %v\n%s", err, out)
	}

	if got := binderContents(t, st, "bulk"); got["Sol Ring"] != 2 || got["Forest"] != 5 {
		t.Errorf("bulk = %v, want every copy from the source binder", got)
	}
	if got := binderContents(t, st, "Binder"); len(got) != 0 {
		t.Errorf("source binder = %v, want emptied", got)
	}
	if !strings.Contains(out, "bulk") {
		t.Errorf("move said nothing about where the cards went:\n%s", out)
	}
}

func TestMoveNeverTouchesDeckCards(t *testing.T) {
	st := moveStore(t)
	before := deckContents(t, st, "Fish")
	doc := holdingsFor(t, st, "--all")

	out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "bulk", "--yes"}, doc)
	if err != nil {
		t.Fatalf("hoard move: %v\n%s", err, out)
	}

	after := deckContents(t, st, "Fish")
	if len(after) != len(before) || after["Mystic Remora"] != before["Mystic Remora"] {
		t.Errorf("deck Fish = %v, was %v — move must never touch a decklist", after, before)
	}
	if after["Mystic Remora"] != 1 {
		t.Errorf("deck Fish holds %d Mystic Remora, want the original 1", after["Mystic Remora"])
	}

	if got := binderContents(t, st, "bulk"); got["Sol Ring"] != 2 || got["Forest"] != 5 {
		t.Errorf("bulk = %v, want the binder cards to have moved regardless", got)
	}
	if got := binderContents(t, st, "bulk"); got["Mystic Remora"] != 0 {
		t.Errorf("bulk = %v, want no deck card to have arrived", got)
	}

	if !strings.Contains(out, "skipped") {
		t.Errorf("move did not report skipping the deck rows:\n%s", out)
	}
	if !strings.Contains(out, "Fish") {
		t.Errorf("move did not name the deck it skipped:\n%s", out)
	}
}

func TestMoveRefusesADocumentOfOnlyDeckCards(t *testing.T) {
	st := moveStore(t)
	doc := holdingsFor(t, st, "--deck", "Fish")

	out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "bulk", "--yes"}, doc)
	if err == nil {
		t.Fatalf("a document of nothing but deck cards must refuse, got:\n%s", out)
	}
	if got := deckContents(t, st, "Fish"); got["Mystic Remora"] != 1 {
		t.Errorf("deck Fish = %v, want untouched", got)
	}
}

func TestMoveMergesWithWhatIsAlreadyThere(t *testing.T) {
	st := moveStore(t)
	bulk, err := st.BinderByRef("bulk")
	if err != nil {
		t.Fatalf("BinderByRef: %v", err)
	}
	forest := scryfall.Card{ID: "forest", Set: "c21", CollectorNumber: "300", Name: "Forest",
		ScryfallURL: "http://x", PriceUSD: f(0.10)}
	if err := st.AddCardFinishTo(bulk.ID, forest, finish.Nonfoil, 3); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}

	doc := holdingsFor(t, st, "--binder", "Binder")
	out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "bulk", "--yes"}, doc)
	if err != nil {
		t.Fatalf("hoard move: %v\n%s", err, out)
	}

	if got := binderContents(t, st, "bulk"); got["Forest"] != 8 {
		t.Errorf("bulk Forest = %d, want the 5 moved added to the 3 already there", got["Forest"])
	}
}

func TestMoveDryRunWritesNothing(t *testing.T) {
	st := moveStore(t)
	doc := holdingsFor(t, st, "--binder", "Binder")

	out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "bulk", "--dry-run"}, doc)
	if err != nil {
		t.Fatalf("hoard move --dry-run: %v\n%s", err, out)
	}

	if !strings.Contains(out, "Would move") {
		t.Errorf("dry run did not say what it would do:\n%s", out)
	}
	if !strings.Contains(out, "bulk") {
		t.Errorf("dry run did not name the target:\n%s", out)
	}
	if got := binderContents(t, st, "bulk"); len(got) != 0 {
		t.Errorf("bulk = %v, want nothing: a dry run writes nothing", got)
	}
	if got := binderContents(t, st, "Binder"); got["Sol Ring"] != 2 || got["Forest"] != 5 {
		t.Errorf("source binder = %v, want untouched by a dry run", got)
	}
}

func TestMoveRefusesADeckAsTarget(t *testing.T) {
	st := moveStore(t)
	doc := holdingsFor(t, st, "--binder", "Binder")

	out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "Fish", "--yes"}, doc)
	if err == nil {
		t.Fatalf("moving into a deck must refuse, got:\n%s", out)
	}
	if got := deckContents(t, st, "Fish"); got["Sol Ring"] != 0 {
		t.Errorf("deck Fish = %v, want no card to have arrived", got)
	}
	if got := binderContents(t, st, "Binder"); got["Sol Ring"] != 2 {
		t.Errorf("source binder = %v, want untouched", got)
	}
}

func TestMoveRefusesADocumentOfTheWrongKind(t *testing.T) {
	st := moveStore(t)
	summary, err := execCmd(context.Background(), st, []string{"report"}, true)
	if err != nil {
		t.Fatalf("hoard report --json: %v", err)
	}

	out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "bulk", "--yes"}, []byte(summary))
	if err == nil {
		t.Fatalf("move accepted a document that is not holdings:\n%s", out)
	}
	if got := binderContents(t, st, "Binder"); got["Sol Ring"] != 2 {
		t.Errorf("source binder = %v, want untouched", got)
	}
}

func TestMoveRefusesWhenTheMoveIsNotConfirmed(t *testing.T) {
	st := moveStore(t)
	doc := holdingsFor(t, st, "--binder", "Binder")

	out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "bulk"}, doc)
	if err == nil {
		t.Fatalf("an unconfirmed move must not proceed, got:\n%s", out)
	}
	if got := binderContents(t, st, "bulk"); len(got) != 0 {
		t.Errorf("bulk = %v, want nothing: the move was never confirmed", got)
	}
}

func TestMoveCountsWhatIsAlreadyInTheTarget(t *testing.T) {
	st := moveStore(t)
	doc := holdingsFor(t, st, "--binder", "Binder")
	if out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "bulk", "--yes"}, doc); err != nil {
		t.Fatalf("first move: %v\n%s", err, out)
	}

	back := holdingsFor(t, st, "--binder", "bulk", "--filter", "name:Forest")
	if out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "Binder", "--yes"}, back); err != nil {
		t.Fatalf("moving the Forests back: %v\n%s", err, out)
	}

	both := holdingsFor(t, st, "--all")
	out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "bulk", "--yes"}, both)
	if err != nil {
		t.Fatalf("second move: %v\n%s", err, out)
	}
	if !strings.Contains(out, "2 copies already there") {
		t.Errorf("move did not count the Sol Rings already in the target:\n%s", out)
	}
	if got := binderContents(t, st, "bulk"); got["Sol Ring"] != 2 || got["Forest"] != 5 {
		t.Errorf("bulk = %v, want every copy once, not doubled", got)
	}
}

func TestMoveRefusesWhenEverythingIsAlreadyThere(t *testing.T) {
	st := moveStore(t)
	doc := holdingsFor(t, st, "--binder", "Binder")
	if out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "bulk", "--yes"}, doc); err != nil {
		t.Fatalf("first move: %v\n%s", err, out)
	}

	again := holdingsFor(t, st, "--binder", "bulk")
	out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "bulk", "--yes"}, again)
	if err == nil {
		t.Fatalf("moving a binder into itself must refuse, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "already in") {
		t.Errorf("error = %q, want it to say the holdings are already there", err)
	}
}

func TestMoveReportsTheValueMoved(t *testing.T) {
	st := moveStore(t)
	doc := holdingsFor(t, st, "--binder", "Binder")

	dry, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "bulk", "--dry-run"}, doc)
	if err != nil {
		t.Fatalf("hoard move --dry-run: %v\n%s", err, dry)
	}
	if !strings.Contains(dry, "$4.50") {
		t.Errorf("dry run did not price what it would move:\n%s", dry)
	}

	out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "bulk", "--yes"}, doc)
	if err != nil {
		t.Fatalf("hoard move: %v\n%s", err, out)
	}
	if !strings.Contains(out, "$4.50") {
		t.Errorf("move did not price what it moved:\n%s", out)
	}
}

func TestMoveValuesAnUnpricedCardAtNothing(t *testing.T) {
	st := moveStore(t)
	mystery := scryfall.Card{ID: "mystery", Set: "xxx", CollectorNumber: "1",
		Name: "Unpriced Thing", ScryfallURL: "http://x"}
	if err := st.UpsertPrintings([]scryfall.Card{mystery}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if err := st.AddCardFinish(mystery, finish.Nonfoil, 3); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	doc := holdingsFor(t, st, "--binder", "Binder")
	out, err := execCmdIn(context.Background(), st,
		[]string{"move", "--to", "bulk", "--yes"}, doc)
	if err != nil {
		t.Fatalf("hoard move: %v\n%s", err, out)
	}
	if !strings.Contains(out, "10 copies") {
		t.Errorf("move did not count the unpriced copies:\n%s", out)
	}
	if !strings.Contains(out, "$4.50") {
		t.Errorf("an unpriced card must add nothing to the value moved:\n%s", out)
	}
}
