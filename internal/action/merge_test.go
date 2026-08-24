package action

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

func fp(v float64) *float64 { return &v }

func mergeStore(t *testing.T, name string) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open %s: %v", name, err)
	}
	t.Cleanup(func() { st.Close() })
	return st, path
}

func card(id, set, num, name string, price float64) scryfall.Card {
	return scryfall.Card{
		ID: id, Set: set, CollectorNumber: num, Name: name,
		PriceUSD: fp(price), ScryfallURL: "https://scryfall.com/card/" + set + "/" + num,
		Raw: json.RawMessage(`{"rarity":"mythic","type_line":"Legendary Creature","name":"` + name + `"}`),
	}
}

func addTo(t *testing.T, st *store.Store, c scryfall.Card, fin finish.Finish, qty int) {
	t.Helper()
	if err := st.AddCardFinish(c, fin, qty); err != nil {
		t.Fatalf("AddCardFinish %s: %v", c.Name, err)
	}
}

func mergeInto(t *testing.T, target *store.Store, targetPath, sourcePath string, o MergeOptions) MergeResult {
	t.Helper()
	o.Source, o.Target = sourcePath, targetPath
	res, err := MergeHoard(Deps{Store: target}, progress.Fn(nil), o)
	if err != nil {
		t.Fatalf("MergeHoard: %v", err)
	}
	return res
}

func held(t *testing.T, st *store.Store, id string, fin finish.Finish) int {
	t.Helper()
	rows, err := st.ListCollectionByFinish()
	if err != nil {
		t.Fatalf("ListCollectionByFinish: %v", err)
	}
	for _, r := range rows {
		if r.ScryfallID == id && r.Finish == fin {
			return r.Quantity
		}
	}
	return 0
}

func TestMergeHoardHoldings(t *testing.T) {
	target, targetPath := mergeStore(t, "target.db")
	source, sourcePath := mergeStore(t, "source.db")

	shared := card("shared-id", "uma", "7", "Ulamog", 10)
	onlyThere := card("only-id", "c21", "1", "Sol Ring", 2)

	addTo(t, target, shared, finish.Nonfoil, 3)
	addTo(t, source, shared, finish.Nonfoil, 2)
	addTo(t, source, onlyThere, finish.Foil, 1)

	res := mergeInto(t, target, targetPath, sourcePath, MergeOptions{})

	if got := held(t, target, "shared-id", finish.Nonfoil); got != 5 {
		t.Errorf("shared printing: got %d copies, want 5 (3 held + 2 merged)", got)
	}
	if got := held(t, target, "only-id", finish.Foil); got != 1 {
		t.Errorf("printing only the source had: got %d copies, want 1", got)
	}
	if res.Copies != 3 {
		t.Errorf("reported %d copies merged, want 3", res.Copies)
	}
	if res.Printings != 2 {
		t.Errorf("reported %d printings carried, want 2", res.Printings)
	}
}

func TestMergeHoardCarriesCardDocuments(t *testing.T) {
	target, targetPath := mergeStore(t, "target.db")
	source, sourcePath := mergeStore(t, "source.db")

	addTo(t, source, card("new-id", "mh3", "1", "Oddball", 5), finish.Nonfoil, 1)
	mergeInto(t, target, targetPath, sourcePath, MergeOptions{})

	detail, err := target.CardDetail("new-id")
	if err != nil {
		t.Fatalf("CardDetail: %v", err)
	}
	if detail.TypeLine != "Legendary Creature" {
		t.Errorf("type line is %v; the generated columns are empty, so raw_json did not cross",
			detail.TypeLine)
	}

	stored := rawJSON(t, targetPath, "new-id")
	if strings.ContainsAny(stored, "\n\t") {
		t.Errorf("stored card document kept the document's indentation:\n%s", stored)
	}
	if !json.Valid([]byte(stored)) {
		t.Errorf("stored card document is not valid JSON: %s", stored)
	}
}

func TestMergeHoardKeepsFresherPrices(t *testing.T) {
	target, targetPath := mergeStore(t, "target.db")
	source, sourcePath := mergeStore(t, "source.db")

	c := card("p-id", "uma", "7", "Ulamog", 10)
	addTo(t, source, c, finish.Nonfoil, 1)

	fresh := c
	fresh.PriceUSD = fp(99)
	addTo(t, target, fresh, finish.Nonfoil, 1)

	if err := backdate(t, sourcePath, "p-id", "2000-01-01T00:00:00Z"); err != nil {
		t.Fatalf("backdating: %v", err)
	}

	mergeInto(t, target, targetPath, sourcePath, MergeOptions{})

	detail, err := target.CardDetail("p-id")
	if err != nil {
		t.Fatalf("CardDetail: %v", err)
	}
	if detail.Card.PriceUSD == nil {
		t.Fatal("the merged printing has no price at all")
	}
	if *detail.Card.PriceUSD != 99 {
		t.Errorf("price is %.2f, want 99.00; the older source overwrote the fresher row",
			*detail.Card.PriceUSD)
	}
}

func TestMergeHoardDecks(t *testing.T) {
	target, targetPath := mergeStore(t, "target.db")
	source, sourcePath := mergeStore(t, "source.db")

	c := card("d-id", "cma", "263", "Sol Ring", 3)
	cmdr := card("c-id", "cmr", "1", "Atraxa", 20)
	for _, st := range []*store.Store{target, source} {
		if err := st.UpsertPrintings([]scryfall.Card{c, cmdr}); err != nil {
			t.Fatalf("UpsertPrintings: %v", err)
		}
	}
	if _, err := source.UpsertDeck(
		store.DeckMeta{Name: "Superfriends", Source: "archidekt", SourceID: "111", Format: "commander"},
		[]store.Entry{
			{ScryfallID: "d-id", Finish: finish.Nonfoil, Board: "main", Quantity: 1},
			{ScryfallID: "c-id", Finish: finish.Foil, Board: "commander", Quantity: 1},
		}); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}

	res := mergeInto(t, target, targetPath, sourcePath, MergeOptions{})
	if res.Decks != 1 {
		t.Fatalf("merged %d decks, want 1", res.Decks)
	}
	decks, err := target.ListDecks()
	if err != nil {
		t.Fatalf("ListDecks: %v", err)
	}
	if len(decks) != 1 || decks[0].Name != "Superfriends" {
		t.Fatalf("target decks = %+v, want one named Superfriends", decks)
	}
	entries, err := target.DeckEntries(decks[0].ID)
	if err != nil {
		t.Fatalf("DeckEntries: %v", err)
	}
	var boards []string
	for _, e := range entries {
		boards = append(boards, e.Board)
	}
	if !slices.Contains(boards, "commander") {
		t.Errorf("boards = %v, want the commander board carried across", boards)
	}
}

func TestMergeHoardSkipsDeckTheTargetAlreadyHas(t *testing.T) {
	target, targetPath := mergeStore(t, "target.db")
	source, sourcePath := mergeStore(t, "source.db")

	c := card("d-id", "cma", "263", "Sol Ring", 3)
	meta := store.DeckMeta{Name: "Superfriends", Source: "archidekt", SourceID: "111"}
	for _, st := range []*store.Store{target, source} {
		if err := st.UpsertPrintings([]scryfall.Card{c}); err != nil {
			t.Fatalf("UpsertPrintings: %v", err)
		}
	}
	if _, err := target.UpsertDeck(meta,
		[]store.Entry{{ScryfallID: "d-id", Finish: finish.Nonfoil, Board: "main", Quantity: 1}}); err != nil {
		t.Fatalf("seeding target deck: %v", err)
	}
	if _, err := source.UpsertDeck(meta,
		[]store.Entry{{ScryfallID: "d-id", Finish: finish.Nonfoil, Board: "main", Quantity: 4}}); err != nil {
		t.Fatalf("seeding source deck: %v", err)
	}

	res := mergeInto(t, target, targetPath, sourcePath, MergeOptions{})
	if len(res.SkippedDecks) != 1 || res.SkippedDecks[0] != "Superfriends" {
		t.Fatalf("skipped decks = %v, want [Superfriends]", res.SkippedDecks)
	}
	entries := deckQuantity(t, target, "d-id")
	if entries != 1 {
		t.Errorf("the target's deck holds %d copies, want its own 1 — the merge overwrote it", entries)
	}

	res = mergeInto(t, target, targetPath, sourcePath, MergeOptions{ReplaceDecks: true, Again: true})
	if res.Decks != 1 {
		t.Fatalf("--replace-decks merged %d decks, want 1", res.Decks)
	}
	if got := deckQuantity(t, target, "d-id"); got != 4 {
		t.Errorf("after --replace-decks the deck holds %d copies, want 4", got)
	}
}

func TestMergeHoardWatches(t *testing.T) {
	target, targetPath := mergeStore(t, "target.db")
	source, sourcePath := mergeStore(t, "source.db")

	shared := card("w-id", "uma", "7", "Ulamog", 10)
	fresh := card("w2-id", "c21", "1", "Sol Ring", 2)
	for _, st := range []*store.Store{target, source} {
		if err := st.UpsertPrintings([]scryfall.Card{shared, fresh}); err != nil {
			t.Fatalf("UpsertPrintings: %v", err)
		}
	}

	if err := target.AddWatch("w-id", "Ulamog", finish.Nonfoil, "under", 5); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	if err := source.AddWatch("w-id", "Ulamog", finish.Nonfoil, "under", 8); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	if err := source.AddWatch("w2-id", "Sol Ring", finish.Nonfoil, "over", 3); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}

	res := mergeInto(t, target, targetPath, sourcePath, MergeOptions{})
	if len(res.SkippedWatches) != 1 {
		t.Fatalf("skipped watches = %v, want the one already standing", res.SkippedWatches)
	}
	if res.Watches != 1 {
		t.Errorf("merged %d watches, want 1", res.Watches)
	}
	for _, w := range listWatches(t, target) {
		if w.ScryfallID == "w-id" && w.Threshold != 5 {
			t.Errorf("threshold is %v; the merge overwrote this hoard's own watch", w.Threshold)
		}
	}
}

func TestMergeHoardCreatesBinders(t *testing.T) {
	target, targetPath := mergeStore(t, "target.db")
	source, sourcePath := mergeStore(t, "source.db")

	c := card("b-id", "uma", "7", "Ulamog", 10)
	if err := source.UpsertPrintings([]scryfall.Card{c}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	id, err := source.CreateBinder("Trades")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := source.AddCardFinishTo(id, c, finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}

	res := mergeInto(t, target, targetPath, sourcePath, MergeOptions{})
	if !slices.Contains(res.Created, "Trades") {
		t.Fatalf("created binders = %v, want Trades among them", res.Created)
	}
	binders, err := target.ListBinders()
	if err != nil {
		t.Fatalf("ListBinders: %v", err)
	}
	var names []string
	for _, b := range binders {
		names = append(names, b.Name)
	}
	if !slices.Contains(names, "Trades") {
		t.Errorf("target binders = %v, want Trades", names)
	}
}

func TestMergeHoardRefusesRepeat(t *testing.T) {
	target, targetPath := mergeStore(t, "target.db")
	source, sourcePath := mergeStore(t, "source.db")
	addTo(t, source, card("r-id", "uma", "7", "Ulamog", 10), finish.Nonfoil, 2)

	mergeInto(t, target, targetPath, sourcePath, MergeOptions{})

	_, err := MergeHoard(Deps{Store: target}, progress.Fn(nil),
		MergeOptions{Source: sourcePath, Target: targetPath})
	if err == nil {
		t.Fatal("a second merge of the same database was allowed; quantities would double")
	}
	if !strings.Contains(err.Error(), "already imported") {
		t.Errorf("error was %q, want the ledger's refusal", err)
	}
	if got := held(t, target, "r-id", finish.Nonfoil); got != 2 {
		t.Errorf("holds %d copies after the refused merge, want 2", got)
	}

	mergeInto(t, target, targetPath, sourcePath, MergeOptions{Again: true})
	if got := held(t, target, "r-id", finish.Nonfoil); got != 4 {
		t.Errorf("holds %d copies after --again, want 4", got)
	}
}

func TestMergeHoardDryRun(t *testing.T) {
	target, targetPath := mergeStore(t, "target.db")
	source, sourcePath := mergeStore(t, "source.db")
	addTo(t, source, card("dr-id", "uma", "7", "Ulamog", 10), finish.Nonfoil, 2)

	res := mergeInto(t, target, targetPath, sourcePath, MergeOptions{DryRun: true})
	if res.Copies != 2 {
		t.Errorf("dry run reported %d copies, want 2", res.Copies)
	}
	if got := held(t, target, "dr-id", finish.Nonfoil); got != 0 {
		t.Errorf("dry run wrote %d copies", got)
	}

	if _, err := MergeHoard(Deps{Store: target}, progress.Fn(nil),
		MergeOptions{Source: sourcePath, Target: targetPath}); err != nil {
		t.Fatalf("merge after a dry run was refused: %v", err)
	}
}

func TestMergeHoardRefusesItself(t *testing.T) {
	target, targetPath := mergeStore(t, "target.db")
	addTo(t, target, card("s-id", "uma", "7", "Ulamog", 10), finish.Nonfoil, 1)

	_, err := MergeHoard(Deps{Store: target}, progress.Fn(nil),
		MergeOptions{Source: targetPath, Target: targetPath})
	if err == nil {
		t.Fatal("merging a hoard into itself was allowed")
	}
	if !strings.Contains(err.Error(), "into itself") {
		t.Errorf("error was %q, want it to name the mistake", err)
	}
}

func TestMergeHoardLeavesSourceUntouched(t *testing.T) {
	target, targetPath := mergeStore(t, "target.db")
	source, sourcePath := mergeStore(t, "source.db")
	addTo(t, source, card("u-id", "uma", "7", "Ulamog", 10), finish.Nonfoil, 1)
	if err := source.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	before, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	mergeInto(t, target, targetPath, sourcePath, MergeOptions{})
	after, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the source database changed; a merge must only read it")
	}
}

func TestMergeHoardDecliningTheUpgradeMergesNothing(t *testing.T) {
	target, targetPath := mergeStore(t, "target.db")
	_, sourcePath := mergeStore(t, "source.db")
	if err := stampVersion(sourcePath, store.SchemaVersion()-1); err != nil {
		t.Fatalf("stamping an old version: %v", err)
	}

	_, err := MergeHoard(Deps{Store: target}, progress.Fn(nil),
		MergeOptions{Source: sourcePath, Target: targetPath})
	if err == nil {
		t.Fatal("an out-of-date source was merged without the upgrade being agreed to")
	}
	if !strings.Contains(err.Error(), "not upgraded") {
		t.Errorf("error was %q, want it to say the upgrade was declined", err)
	}
	if v, verr := store.FileVersion(sourcePath); verr != nil || v != store.SchemaVersion()-1 {
		t.Errorf("source is now v%d (err %v); declining must leave it alone", v, verr)
	}
}

func TestMergeHoardUpgradesSourceOnConsent(t *testing.T) {
	target, targetPath := mergeStore(t, "target.db")
	source, sourcePath := mergeStore(t, "source.db")
	addTo(t, source, card("up-id", "uma", "7", "Ulamog", 10), finish.Nonfoil, 2)
	if err := source.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := stampVersion(sourcePath, store.SchemaVersion()-1, newestUndo...); err != nil {
		t.Fatalf("stamping an old version: %v", err)
	}

	var asked string
	res, err := MergeHoard(
		Deps{Store: target, Confirm: func(q string) bool { asked = q; return true }},
		progress.Fn(nil), MergeOptions{Source: sourcePath, Target: targetPath})
	if err != nil {
		t.Fatalf("MergeHoard: %v", err)
	}
	if !strings.Contains(asked, "must be upgraded") {
		t.Errorf("the question asked was %q", asked)
	}
	if !res.Upgraded || res.BackupPath == "" {
		t.Fatalf("result does not record the upgrade: %+v", res)
	}
	if _, serr := os.Stat(res.BackupPath); serr != nil {
		t.Errorf("no backup at %s: %v", res.BackupPath, serr)
	}
	if got := held(t, target, "up-id", finish.Nonfoil); got != 2 {
		t.Errorf("merged %d copies after the upgrade, want 2", got)
	}
}

func TestMergeHoardRestoresSourceWhenUpgradeFails(t *testing.T) {
	target, targetPath := mergeStore(t, "target.db")
	source, sourcePath := mergeStore(t, "source.db")
	addTo(t, source, card("rb-id", "uma", "7", "Ulamog", 10), finish.Nonfoil, 2)
	if err := source.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := stampVersion(sourcePath, 0); err != nil {
		t.Fatalf("stamping version 0: %v", err)
	}
	before, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	_, err = MergeHoard(
		Deps{Store: target, Confirm: func(string) bool { return true }},
		progress.Fn(nil), MergeOptions{Source: sourcePath, Target: targetPath})
	if err == nil {
		t.Fatal("replaying every migration onto a current database somehow succeeded")
	}
	if !strings.Contains(err.Error(), "data is intact") {
		t.Errorf("error was %q; it must tell the user their database survived", err)
	}

	after, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the candidate was not restored; a failed upgrade must leave it byte-identical")
	}
	if got := held(t, target, "rb-id", finish.Nonfoil); got != 0 {
		t.Errorf("merged %d copies despite the failure, want 0", got)
	}
}

func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func backdate(t *testing.T, path, scryfallID, when string) error {
	t.Helper()
	_, err := openRaw(t, path).Exec(
		`UPDATE cards SET updated_at = ? WHERE scryfall_id = ?`, when, scryfallID)
	return err
}

func stampVersion(path string, v int, undo ...string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, s := range undo {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	_, err = db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v))
	return err
}

var newestUndo = []string{`ALTER TABLE containers DROP COLUMN name_locked`}

func rawJSON(t *testing.T, path, scryfallID string) string {
	t.Helper()
	var raw string
	if err := openRaw(t, path).QueryRow(
		`SELECT COALESCE(raw_json,'') FROM cards WHERE scryfall_id = ?`, scryfallID).
		Scan(&raw); err != nil {
		t.Fatalf("reading raw_json: %v", err)
	}
	return raw
}

func deckQuantity(t *testing.T, st *store.Store, scryfallID string) int {
	t.Helper()
	decks, err := st.ListDecks()
	if err != nil {
		t.Fatalf("ListDecks: %v", err)
	}
	total := 0
	for _, d := range decks {
		entries, err := st.DeckEntries(d.ID)
		if err != nil {
			t.Fatalf("DeckEntries: %v", err)
		}
		for _, e := range entries {
			if e.Card.ScryfallID == scryfallID {
				total += e.Quantity
			}
		}
	}
	return total
}

func listWatches(t *testing.T, st *store.Store) []store.WatchStatus {
	t.Helper()
	ws, err := st.ListWatches()
	if err != nil {
		t.Fatalf("ListWatches: %v", err)
	}
	return ws
}
