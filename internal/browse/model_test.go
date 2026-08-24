package browse

import (
	"context"
	"fmt"
	"image"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tui"
	"github.com/spiffcs/hoard/internal/ui"
)

type fakeStore struct {
	movedCondition string

	totals     store.CollectionTotals
	decks      []store.DeckSummary
	collection []store.CollectionRow
	deckCards  map[int64][]store.EntryView

	traits    map[string][]string
	enriched  int
	watches   []store.WatchStatus
	binders   map[int64]string
	uncounted map[int64]bool

	binderRows map[int64][]store.CollectionRow

	folders     []store.DeckSummary
	folderRows  map[int64][]store.CollectionRow
	folderMoves [][2]int64

	entryKeys []store.EntryKey

	settings map[string]string

	sets   []store.SetSummary
	nextID int64

	err error

	matchCalls int

	movers   []store.PriceChange
	unpriced []store.UnpricedRow
	dips     []store.TrendRow
	momentum []store.TrendRow

	dataVersion      int64
	dataVersionReads int
	slowRead         time.Duration

	binderListCalls int
	watchListCalls  int
	unpricedCalls   int

	bidSeries map[string][]store.PricePoint

	holdingsByName map[string][]store.Holding

	holdingsByID map[string][]store.Holding

	removedCard map[string][]store.Holding
	removedDeck int64

	upserted []scryfall.Card
}

func (f *fakeStore) MatchingCardIDs(tf store.TraitFilter) (map[string]bool, error) {
	f.matchCalls++
	if f.err != nil {
		return nil, f.err
	}
	want := append(append([]string{}, tf.Rarities...), tf.Types...)
	want = append(want, tf.Colors...)
	out := map[string]bool{}
	for id, have := range f.traits {
		ok := true
		for _, w := range want {
			if !slices.Contains(have, strings.ToLower(w)) {
				ok = false
				break
			}
		}
		if ok {
			out[id] = true
		}
	}
	return out, nil
}

func (f *fakeStore) Movers(since string) ([]store.PriceChange, error) {
	return f.movers, f.err
}
func (f *fakeStore) Unpriced() ([]store.UnpricedRow, error) {
	f.unpricedCalls++
	return f.unpriced, f.err
}

func (f *fakeStore) DataVersion() (int64, error) {
	f.dataVersionReads++
	return f.dataVersion, nil
}

func (f *fakeStore) EnrichedCount() (int, int, error) {
	return f.enriched, len(f.collection), f.err
}

const defaultBinderID = 1

func (f *fakeStore) rowsIn(cid int64) []store.CollectionRow {
	if cid != defaultBinderID {
		if rows, ok := f.binderRows[cid]; ok {
			return rows
		}
	}
	return f.collection
}

func (f *fakeStore) setRowsIn(cid int64, rows []store.CollectionRow) {
	if cid != defaultBinderID {
		if _, ok := f.binderRows[cid]; ok {
			f.binderRows[cid] = rows
			return
		}
	}
	f.collection = rows
}

func (f *fakeStore) ListBinders() ([]store.DeckSummary, error) {
	f.binderListCalls++
	if f.err != nil {
		return nil, f.err
	}
	b := store.DeckSummary{
		DistinctCards: f.totals.DistinctCards,
		TotalCopies:   f.totals.TotalCopies,
		Value:         f.totals.Value,
		IsDefault:     true,
		Counted:       !f.uncounted[defaultBinderID],
	}
	b.ID = defaultBinderID
	b.Name = store.LooseName
	b.Kind = store.KindCollection
	out := []store.DeckSummary{b}
	for id, name := range f.binders {
		nb := store.DeckSummary{Counted: !f.uncounted[id]}
		nb.ID, nb.Name, nb.Kind = id, name, store.KindCollection
		out = append(out, nb)
	}
	return out, nil
}
func (f *fakeStore) ListDecks() ([]store.DeckSummary, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := append([]store.DeckSummary(nil), f.decks...)
	for i := range out {
		out[i].Counted = !f.uncounted[out[i].ID]
	}
	return out, nil
}
func (f *fakeStore) ListFolders() ([]store.DeckSummary, error) {
	return f.folders, f.err
}

func (f *fakeStore) CreateFolder(name string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	for _, existing := range f.folders {
		if strings.EqualFold(existing.Name, name) {
			return 0, fmt.Errorf("a folder named %q already exists", existing.Name)
		}
	}
	f.nextID++
	d := store.DeckSummary{}
	d.ID, d.Name, d.Kind = f.nextID, name, store.KindFolder
	d.Counted = true
	f.folders = append(f.folders, d)
	return d.ID, nil
}

func (f *fakeStore) RenameFolder(id int64, name string) error {
	if f.err != nil {
		return f.err
	}
	for i := range f.folders {
		if f.folders[i].ID == id {
			f.folders[i].Name = name
			return nil
		}
	}
	return fmt.Errorf("no folder #%d", id)
}

func (f *fakeStore) RenameDeck(id int64, name string) error {
	if f.err != nil {
		return f.err
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("a deck needs a name")
	}
	for i := range f.decks {
		if f.decks[i].ID == id {
			f.decks[i].Name = name
			return nil
		}
	}
	return fmt.Errorf("no deck #%d", id)
}

func (f *fakeStore) MoveDeckToFolder(deckID, folderID int64) error {
	if f.err != nil {
		return f.err
	}
	if folderID != 0 && !slices.ContainsFunc(f.folders,
		func(d store.DeckSummary) bool { return d.ID == folderID }) {
		return fmt.Errorf("no folder #%d", folderID)
	}
	for i := range f.decks {
		if f.decks[i].ID != deckID {
			continue
		}
		f.decks[i].ParentID = folderID
		f.folderMoves = append(f.folderMoves, [2]int64{deckID, folderID})
		return nil
	}
	return fmt.Errorf("no deck #%d", deckID)
}
func (f *fakeStore) FolderByFinish(id int64) ([]store.CollectionRow, error) {
	return f.folderRows[id], f.err
}
func (f *fakeStore) BinderByFinish(id int64) ([]store.CollectionRow, error) {
	if rows, ok := f.binderRows[id]; ok {
		return rows, f.err
	}
	return f.collection, f.err
}

func (f *fakeStore) EntryKeys() ([]store.EntryKey, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.entryKeys != nil {
		return f.entryKeys, nil
	}
	var out []store.EntryKey
	addRows := func(cid int64, rows []store.CollectionRow) {
		for _, r := range rows {
			out = append(out, store.EntryKey{ContainerID: cid, ScryfallID: r.ScryfallID, Finish: r.Finish, Quantity: r.Quantity})
		}
	}
	addRows(defaultBinderID, f.collection)
	for id := range f.binders {
		if rows, ok := f.binderRows[id]; ok {
			addRows(id, rows)
		} else {
			addRows(id, f.collection)
		}
	}
	for id, entries := range f.deckCards {
		for _, e := range entries {
			out = append(out, store.EntryKey{ContainerID: id, ScryfallID: e.Card.ScryfallID, Finish: e.Finish, Quantity: e.Quantity})
		}
	}
	return out, nil
}
func (f *fakeStore) DeckEntries(id int64) ([]store.EntryView, error) {
	return f.deckCards[id], f.err
}

func (f *fakeStore) Settings() (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.settings, nil
}
func (f *fakeStore) SaveSettings(kv map[string]string) error {
	if f.err != nil {
		return f.err
	}
	if f.settings == nil {
		f.settings = map[string]string{}
	}
	for k, v := range kv {
		f.settings[k] = v
	}
	return nil
}

func (f *fakeStore) CardDetail(id string) (store.CardDetail, error) {
	var d store.CardDetail
	d.ScryfallID = id
	d.Name = strings.TrimSuffix(id, "-id")
	d.SetCode = "uma"
	d.CollectorNumber = "85"
	d.ImageURI = "http://img.test/" + id
	tcg := int64(12345)
	d.TCGplayerID = &tcg
	d.CKURL = "https://mtgjson.com/links/plain"
	d.CKFoilURL = "https://mtgjson.com/links/foil"
	return d, f.err
}

func (f *fakeStore) HoldingsOf(id string) ([]store.Holding, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []store.Holding
	for _, h := range f.holdingsByID[id] {
		if h.ContainerKind == store.KindCollection {
			qty := 0
			for _, r := range f.rowsIn(h.ContainerID) {
				if r.ScryfallID == id && r.Finish == h.Finish {
					qty = r.Quantity
				}
			}
			if qty == 0 {
				continue
			}
			h.Quantity = qty
		}
		out = append(out, h)
	}
	return out, nil
}

func (f *fakeStore) HoldingsOfName(name string) ([]store.Holding, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []store.Holding
	for _, h := range f.holdingsByName[name] {
		if h.ContainerKind == store.KindCollection {
			qty := 0
			for _, r := range f.collection {
				if r.ScryfallID == h.ScryfallID && r.Finish == h.Finish {
					qty = r.Quantity
				}
			}
			if qty == 0 {
				continue
			}
			h.Quantity = qty
		}
		out = append(out, h)
	}
	return out, nil
}
func (f *fakeStore) PriceSeries(string, finish.Finish) ([]store.PricePoint, error) { return nil, f.err }

func (f *fakeStore) BidSeries(id string, fin finish.Finish) ([]store.PricePoint, error) {
	return f.bidSeries[id+"|"+fin.String()], f.err
}

func (f *fakeStore) AllByFinish() ([]store.CollectionRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	time.Sleep(f.slowRead)
	out := append([]store.CollectionRow(nil), f.collection...)
	for _, entries := range f.deckCards {
		for _, e := range entries {
			r := store.CollectionRow{Card: e.Card, Finish: e.Finish, Quantity: e.Quantity}
			r.Value = e.Value()
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) SetsHeld() ([]store.SetSummary, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.sets != nil {
		return f.sets, nil
	}
	all, err := f.AllByFinish()
	if err != nil {
		return nil, err
	}
	byCode := map[string]*store.SetSummary{}
	var codes []string
	for _, r := range all {
		s, ok := byCode[r.SetCode]
		if !ok {
			s = &store.SetSummary{Code: r.SetCode, Name: strings.ToUpper(r.SetCode)}
			byCode[r.SetCode] = s
			codes = append(codes, r.SetCode)
		}
		s.Copies += r.Quantity
		s.Value += r.Value
	}
	slices.Sort(codes)
	out := make([]store.SetSummary, 0, len(codes))
	for _, c := range codes {
		out = append(out, *byCode[c])
	}
	return out, nil
}

func (f *fakeStore) SetByFinish(code string) ([]store.CollectionRow, error) {
	all, err := f.AllByFinish()
	if err != nil {
		return nil, err
	}
	var out []store.CollectionRow
	at := map[string]int{}
	for _, r := range all {
		if r.SetCode != code {
			continue
		}
		key := r.ScryfallID + "|" + r.Finish.String()
		if i, ok := at[key]; ok {
			out[i].Quantity += r.Quantity
			out[i].Value += r.Value
			continue
		}
		at[key] = len(out)
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeStore) ListWatches() ([]store.WatchStatus, error) {
	f.watchListCalls++
	return f.watches, f.err
}

func (f *fakeStore) WouldFire() ([]store.WatchStatus, error) {
	if f.err != nil {
		return nil, f.err
	}
	var fired []store.WatchStatus
	for _, w := range f.watches {
		if w.PriceUSD != nil && w.Met() && w.LastState != "met" {
			fired = append(fired, w)
		}
	}
	return fired, nil
}

func (f *fakeStore) AddWatch(id, display string, fin finish.Finish, op string, threshold float64) error {
	if f.err != nil {
		return f.err
	}
	w := store.WatchStatus{Name: display}
	w.ScryfallID, w.Display, w.Finish, w.Op, w.Threshold = id, display, fin, op, threshold
	f.watches = append(f.watches, w)
	return nil
}

func (f *fakeStore) RemoveWatch(id int64) error {
	for i, w := range f.watches {
		if w.ID == id {
			f.watches = append(f.watches[:i], f.watches[i+1:]...)
			return nil
		}
	}
	return f.err
}

func (f *fakeStore) CreateBinder(name string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	if f.binders == nil {
		f.binders = map[int64]string{}
	}
	f.nextID++
	id := 100 + f.nextID
	f.binders[id] = name
	return id, nil
}

func (f *fakeStore) RenameBinder(id int64, name string) error {
	if f.err != nil {
		return f.err
	}
	f.binders[id] = name
	return nil
}

func (f *fakeStore) DeleteBinder(id int64) error {
	if f.err != nil {
		return f.err
	}
	delete(f.binders, id)
	return nil
}

func (f *fakeStore) SetEntryQuantity(ref store.EntryRef, qty int) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	cid, id, fin := ref.ContainerID, ref.ScryfallID, ref.Finish
	if _, isDeck := f.deckCards[cid]; isDeck {
		return f.setDeckEntryQuantity(ref, qty)
	}
	rows := f.rowsIn(cid)
	var previous int
	var found bool
	out := rows[:0:0]
	for _, r := range rows {
		if r.ScryfallID == id && r.Finish == fin {
			previous, found = r.Quantity, true
			if qty == 0 {
				continue
			}
			unit := r.Value / float64(max(r.Quantity, 1))
			r.Quantity = qty
			r.Value = unit * float64(qty)
		}
		out = append(out, r)
	}

	if !found && qty > 0 {
		out = append(out,
			row(strings.TrimSuffix(id, "-id"), "uma", "1", fin, qty, float64(qty)))
	}
	f.setRowsIn(cid, out)
	return previous, nil
}

func (f *fakeStore) MoveEntryCondition(from store.EntryRef, toCondition string) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.movedCondition = from.Condition + "→" + toCondition
	return 0, nil
}

func (f *fakeStore) MoveEntry(from store.EntryRef, toC int64, toID string) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	fromC, id, fin := from.ContainerID, from.ScryfallID, from.Finish
	if fromC == toC && id == toID {
		return 0, nil
	}
	take := func(cid int64) []store.CollectionRow {
		if cid != defaultBinderID {
			if rows, ok := f.binderRows[cid]; ok {
				return rows
			}
		}
		return f.collection
	}
	put := func(cid int64, rows []store.CollectionRow) {
		if cid != defaultBinderID {
			if f.binderRows == nil {
				f.binderRows = map[int64][]store.CollectionRow{}
			}
			f.binderRows[cid] = rows
			return
		}
		f.collection = rows
	}

	src := take(fromC)
	var moved store.CollectionRow
	found := false
	kept := src[:0:0]
	for _, r := range src {
		if !found && r.ScryfallID == id && r.Finish == fin {
			moved, found = r, true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return 0, fmt.Errorf("no such holding to move")
	}
	put(fromC, kept)

	dst := take(toC)
	for i, r := range dst {
		if r.ScryfallID == toID && r.Finish == fin {
			prev := r.Quantity
			dst[i].Quantity += moved.Quantity
			put(toC, dst)
			return prev, nil
		}
	}
	moved.ScryfallID = toID
	put(toC, append(dst, moved))
	return 0, nil
}

func (f *fakeStore) MoveEntryFinish(from store.EntryRef, toFinish finish.Finish) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	cid, id, fromFinish := from.ContainerID, from.ScryfallID, from.Finish
	if fromFinish == toFinish {
		return 0, nil
	}
	rows := f.collection
	inBinder := false
	if cid != defaultBinderID {
		if br, ok := f.binderRows[cid]; ok {
			rows, inBinder = br, true
		}
	}
	var moved store.CollectionRow
	found := false
	kept := rows[:0:0]
	for _, r := range rows {
		if !found && r.ScryfallID == id && r.Finish == fromFinish {
			moved, found = r, true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return 0, fmt.Errorf("no such holding to move")
	}
	prev := 0
	merged := false
	for i, r := range kept {
		if r.ScryfallID == id && r.Finish == toFinish {
			prev = r.Quantity
			kept[i].Quantity += moved.Quantity
			merged = true
			break
		}
	}
	if !merged {
		moved.Finish = toFinish
		kept = append(kept, moved)
	}
	if inBinder {
		f.binderRows[cid] = kept
	} else {
		f.collection = kept
	}
	return prev, nil
}

func (f *fakeStore) UpsertPrintings(cards []scryfall.Card) error {
	if f.err != nil {
		return f.err
	}
	f.upserted = append(f.upserted, cards...)
	return nil
}

func (f *fakeStore) RemoveFromBinder(cid int64, id string) ([]store.Holding, error) {
	if f.err != nil {
		return nil, f.err
	}
	var removed []store.Holding
	var kept []store.CollectionRow
	for _, r := range f.rowsIn(cid) {
		if r.ScryfallID == id {
			removed = append(removed, store.Holding{
				ContainerID: cid, ContainerKind: store.KindCollection,
				Finish: r.Finish, Board: "main", Quantity: r.Quantity,
			})
			continue
		}
		kept = append(kept, r)
	}
	f.setRowsIn(cid, kept)
	if f.removedCard == nil {
		f.removedCard = map[string][]store.Holding{}
	}
	f.removedCard[id] = removed
	return removed, nil
}

func (f *fakeStore) RestoreHoldings(id string, holdings []store.Holding) error {
	if f.err != nil {
		return f.err
	}
	for _, h := range holdings {
		cid := h.ContainerID
		if cid == 0 {
			cid = defaultBinderID
		}
		f.setRowsIn(cid, append(f.rowsIn(cid),
			row(strings.TrimSuffix(id, "-id"), "uma", "1", h.Finish, h.Quantity, float64(h.Quantity))))
	}
	return nil
}
func (f *fakeStore) RemoveContainer(id int64) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.removedDeck = id
	delete(f.deckCards, id)
	for i, d := range f.decks {
		if d.ID == id {
			f.decks = append(f.decks[:i], f.decks[i+1:]...)
			break
		}
	}
	return 1, nil
}

func (f *fakeStore) UpsertDeck(meta store.DeckMeta, entries []store.Entry) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	id := f.removedDeck
	views := make([]store.EntryView, 0, len(entries))
	var copies int
	for _, e := range entries {
		views = append(views, entry(e.ScryfallID[:len(e.ScryfallID)-3], e.Board, e.Finish, e.Quantity, 1))
		copies += e.Quantity
	}
	f.deckCards[id] = views
	f.decks = append(f.decks, deck(id, meta.Name, copies, 0))
	return id, nil
}

func price(v float64) *float64 { return &v }

func deck(id int64, name string, copies int, value float64) store.DeckSummary {
	d := store.DeckSummary{DistinctCards: copies, TotalCopies: copies, Value: value}
	d.ID = id
	d.Name = name
	d.Kind = store.KindDeck
	return d
}

func row(name, set, num string, fin finish.Finish, qty int, value float64) store.CollectionRow {
	r := store.CollectionRow{Finish: fin, Quantity: qty, Value: value}
	r.ScryfallID = name + "-id"
	r.Name = name
	r.SetCode = set
	r.CollectorNumber = num
	if fin == finish.Nonfoil {
		r.PriceUSD = price(value / float64(max(qty, 1)))
	} else {
		r.PriceUSDFoil = price(value / float64(max(qty, 1)))
	}
	return r
}

func entry(name, board string, fin finish.Finish, qty int, usd float64) store.EntryView {
	e := store.EntryView{Finish: fin, Board: board, Quantity: qty}
	e.Card.ScryfallID = name + "-id"
	e.Card.Name = name
	e.Card.SetCode = "mh3"
	e.Card.CollectorNumber = "1"
	if fin == finish.Nonfoil {
		e.Card.PriceUSD = price(usd)
	} else {
		e.Card.PriceUSDFoil = price(usd)
	}
	return e
}

func testStore() *fakeStore {
	return &fakeStore{
		totals: store.CollectionTotals{DistinctCards: 3, TotalCopies: 8, Value: 300},

		decks: []store.DeckSummary{
			deck(201, "Cheap Deck", 100, 50),
			deck(202, "Rich Deck", 100, 500),
		},
		collection: []store.CollectionRow{
			row("Bitterblossom", "uma", "85", finish.Nonfoil, 4, 136),
			row("Ancient Tomb", "uma", "236", finish.Foil, 1, 134),
			row("Sol Ring", "c21", "1", finish.Nonfoil, 3, 30),
		},
		traits: map[string][]string{
			"Bitterblossom-id": {"mythic", "enchantment", "B"},
			"Ancient Tomb-id":  {"rare", "land"},
			"Sol Ring-id":      {"uncommon", "artifact"},
		},
		enriched: 3,
		deckCards: map[int64][]store.EntryView{
			202: {entry("Solitude", "main", finish.Nonfoil, 1, 34), entry("Force of Will", "side", finish.Foil, 2, 90)},
			201: {entry("Llanowar Elves", "main", finish.Nonfoil, 1, 1)},
		},
	}
}

func newTestModel(t *testing.T, st Store) Model {
	t.Helper()

	m, err := New(st, WithEnv(ui.Env{Color: true}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	m.setsMode = false
	if err := m.loadContainers(); err != nil {
		t.Fatalf("loadContainers: %v", err)
	}

	m.cursor[paneContainers] = 1
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return next.(Model)
}

func atAllCards(t *testing.T, m Model) Model {
	t.Helper()
	m.cursor[paneContainers] = 0
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}
	m.deriveView()
	return m
}

func key(m Model, k string) Model {
	var msg tea.KeyMsg
	switch k {
	case "up", "down", "tab", "left", "right", "home", "end", "pgup", "pgdown",
		"shift+up", "shift+down", "enter", "esc", "ctrl+u":
		msg = tea.KeyMsg{Type: keyType(k)}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func keyType(k string) tea.KeyType {
	switch k {
	case "up":
		return tea.KeyUp
	case "down":
		return tea.KeyDown
	case "tab":
		return tea.KeyTab
	case "left":
		return tea.KeyLeft
	case "right":
		return tea.KeyRight
	case "home":
		return tea.KeyHome
	case "end":
		return tea.KeyEnd
	case "pgup":
		return tea.KeyPgUp
	case "pgdown":
		return tea.KeyPgDown
	case "shift+up":
		return tea.KeyShiftUp
	case "shift+down":
		return tea.KeyShiftDown
	case "enter":
		return tea.KeyEnter
	case "esc":
		return tea.KeyEsc
	case "ctrl+u":
		return tea.KeyCtrlU
	}
	return tea.KeyNull
}

func TestContainersAreCollectionThenDecksByValue(t *testing.T) {
	m := newTestModel(t, testStore())

	var got []string
	for _, c := range m.containers {
		got = append(got, c.Name)
	}
	want := []string{allCardsName, store.LooseName, "Rich Deck", "Cheap Deck"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("containers = %v, want %v", got, want)
	}
	if m.containers[1].Value != 300 || m.containers[1].Copies != 8 {
		t.Errorf("collection row = %+v, want the totals", m.containers[1])
	}

	if all := m.containers[0]; all.Kind != kindAllCards || all.Value <= m.containers[1].Value {
		t.Errorf("all-cards row = %+v, want every container summed", all)
	}
}

func TestMovingContainerCursorLoadsThatContainersCards(t *testing.T) {
	m := newTestModel(t, testStore())

	if len(m.cards) != 3 || m.cards[0].Name != "Bitterblossom" {
		t.Fatalf("initial cards = %+v, want the collection by value", names(m.cards))
	}
	m = key(m, "down")
	if len(m.cards) != 2 {
		t.Fatalf("cards = %v, want Rich Deck's two", names(m.cards))
	}
	if m.cards[0].Name != "Force of Will" {
		t.Errorf("cards = %v, want the 2x$90 foil first by value", names(m.cards))
	}
	m = key(m, "down")
	if len(m.cards) != 1 || m.cards[0].Name != "Llanowar Elves" {
		t.Errorf("cards = %v, want Cheap Deck's one", names(m.cards))
	}
}

func TestCardCursorDoesNotReloadCards(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = key(m, "tab")
	if m.focus != paneCards {
		t.Fatal("tab did not move focus to the card pane")
	}
	m = key(m, "down")
	if m.cursor[paneCards] != 1 {
		t.Errorf("card cursor = %d, want 1", m.cursor[paneCards])
	}
	if m.cursor[paneContainers] != 1 {
		t.Errorf("container cursor moved to %d", m.cursor[paneContainers])
	}
}

func TestTabTogglesFocusBothWays(t *testing.T) {
	m := newTestModel(t, testStore())
	if m.focus != paneContainers {
		t.Fatal("should start on the container pane")
	}
	if m = key(m, "tab"); m.focus != paneCards {
		t.Fatal("tab did not reach the card pane")
	}
	if m = key(m, "tab"); m.focus != paneContainers {
		t.Fatal("tab did not toggle back")
	}

	if m = key(m, "right"); m.focus != paneCards {
		t.Error("right did not focus the card pane")
	}
	if m = key(m, "right"); m.focus != paneCards {
		t.Error("right should stay on the card pane, not toggle")
	}
	if m = key(m, "left"); m.focus != paneContainers {
		t.Error("left did not focus the container pane")
	}
}

func TestCursorClampsAtBothEnds(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "up")
	if m.cursor[paneContainers] != 0 {
		t.Errorf("up from the top = %d, want 0", m.cursor[paneContainers])
	}
	for range 10 {
		m = key(m, "down")
	}
	if got, want := m.cursor[paneContainers], len(m.containers)-1; got != want {
		t.Errorf("down past the end = %d, want %d", got, want)
	}
}

func TestSortCycles(t *testing.T) {
	m := newTestModel(t, testStore())
	if m.cards[0].Name != "Bitterblossom" {
		t.Fatalf("default sort = %v, want value order", names(m.cards))
	}

	for _, step := range []struct{ label, first string }{
		{"name", "Ancient Tomb"},
		{"set", "Sol Ring"},
		{"finish", "Ancient Tomb"},
		{"qty", "Bitterblossom"},
		{"price", "Ancient Tomb"},
		{"value", "Bitterblossom"},
	} {
		m = key(m, "s")
		if got := m.sortLabel(); got != step.label {
			t.Fatalf("sort label = %q, want %q", got, step.label)
		}
		if m.cards[0].Name != step.first {
			t.Errorf("by %s = %v, want %s first", step.label, names(m.cards), step.first)
		}
	}

	m = key(m, "S")
	if m.sortLabel() != "value (reversed)" || m.cards[0].Name != "Sol Ring" {
		t.Errorf("reversed value = %v (label %q), want the cheapest first",
			names(m.cards), m.sortLabel())
	}
	m = key(m, "S")
	if m.cards[0].Name != "Bitterblossom" {
		t.Errorf("un-reversed = %v, want value order again", names(m.cards))
	}
}

func TestSortPersistsAcrossContainers(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "s")
	m = key(m, "down")
	if m.sortLabel() != "name" {
		t.Fatalf("sort reset to %v", m.sortLabel())
	}
	if m.cards[0].Name != "Force of Will" {
		t.Errorf("deck cards = %v, want name order", names(m.cards))
	}
}

func TestReadFailureBecomesAStatusNotAQuit(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	st.err = errFake{}

	m = key(m, "r")
	if !m.statusErr || m.status == "" {
		t.Errorf("want an error status, got %q (err=%v)", m.status, m.statusErr)
	}
	if m.err != nil {
		t.Errorf("session ended with %v, want it to stay open", m.err)
	}

	if len(m.containers) == 0 {
		t.Error("containers were cleared on a failed reload")
	}
}

type errFake struct{}

func (errFake) Error() string { return "database is locked" }

func TestEmptyHoard(t *testing.T) {
	m := newTestModel(t, &fakeStore{})

	if len(m.containers) != 2 || m.containers[0].Kind != kindAllCards {
		t.Fatalf("containers = %+v, want the merged row and the empty collection", m.containers)
	}
	if len(m.cards) != 0 {
		t.Errorf("cards = %v, want none", names(m.cards))
	}
	if out := m.View(); out == "" {
		t.Error("View rendered nothing for an empty hoard")
	}

	m = key(m, "tab")
	m = key(m, "down")
	if m.cursor[paneCards] != 0 {
		t.Errorf("card cursor = %d on an empty pane", m.cursor[paneCards])
	}
}

func names(cards []card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.Name
	}
	return out
}

func TestViewFitsEveryWidth(t *testing.T) {
	for _, w := range []int{40, 60, 80, 100, 140} {
		t.Run(strconv.Itoa(w), func(t *testing.T) {
			m, err := New(testStore())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 20})
			out := next.(Model).View()

			for i, line := range strings.Split(out, "\n") {
				if got := ansi.StringWidth(line); got > w {
					t.Errorf("line %d is %d cells wide at width %d: %q", i, got, w, line)
				}
			}
		})
	}
}

func TestScrollingKeepsTheCursorVisible(t *testing.T) {
	st := testStore()

	for i := range 40 {
		st.collection = append(st.collection,
			row("Filler "+strconv.Itoa(i), "set", strconv.Itoa(i), finish.Nonfoil, 1, float64(i)))
	}
	m, err := New(st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	m = next.(Model)
	m = key(m, "tab")

	rows := m.visibleRows() - 1
	for range len(m.cards) + 5 {
		m = key(m, "down")
		if m.cursor[paneCards] < m.offset[paneCards] ||
			m.cursor[paneCards] >= m.offset[paneCards]+rows {
			t.Fatalf("cursor %d outside window [%d,%d)",
				m.cursor[paneCards], m.offset[paneCards], m.offset[paneCards]+rows)
		}

		if name := m.cards[m.cursor[paneCards]].Name; !strings.Contains(m.View(), name) {
			t.Fatalf("selected row %q is not in the rendered frame", name)
		}

		if n := len(strings.Split(m.View(), "\n")); n > 12 {
			t.Fatalf("rendered %d lines at height 12", n)
		}
	}
	if m.cursor[paneCards] != len(m.cards)-1 {
		t.Errorf("cursor = %d, want the last of %d", m.cursor[paneCards], len(m.cards))
	}
}

func TestBoardColumnOnlyAppearsForDecks(t *testing.T) {
	m := newTestModel(t, testStore())
	if strings.Contains(m.View(), "BOARD") {
		t.Error("BOARD shown for the loose collection")
	}
	m = key(m, "down")
	view := m.View()
	if !strings.Contains(view, "BOARD") {
		t.Error("BOARD missing for a deck")
	}

	if name, board := strings.Index(view, "NAME"), strings.Index(view, "BOARD"); name > board {
		t.Error("BOARD column renders before NAME")
	}
}

func TestSelectionBarSpansTheWholeRow(t *testing.T) {

	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(prev)

	m := newTestModel(t, testStore())
	m = key(m, "down")
	m = key(m, "tab")

	name := m.cards[m.cursor[paneCards]].Name
	var sel string
	for line := range strings.SplitSeq(m.View(), "\n") {
		if strings.Contains(line, name) && strings.Contains(line, "\x1b[7m") {
			sel = line
			break
		}
	}
	if sel == "" {
		t.Fatalf("no reverse-video line contains the selected card %q", name)
	}

	if !strings.Contains(sel, "mh3/1") {
		t.Fatalf("selected row lost its SET/NUM column: %q", sel)
	}
	if !strings.Contains(sel, "\x1b[2m") {
		t.Errorf("the dim BOARD cell lost its own styling under the bar: %q", sel)
	}
	body := strings.TrimSuffix(sel[strings.Index(sel, "\x1b[7m"):], "\x1b[0m")
	for rest := body; ; {
		j := strings.Index(rest, "\x1b[0m")
		if j < 0 {
			break
		}
		rest = rest[j+len("\x1b[0m"):]
		if !strings.HasPrefix(rest, "\x1b[7m") {
			t.Fatalf("a reset mid-row is not followed by the bar re-assertion: %q", sel)
		}
	}
}

func typeFilter(m Model, q string) Model {
	m = key(m, "/")
	for _, r := range q {
		if r == ' ' {
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
			m = next.(Model)
			continue
		}
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	return m
}

func TestFilterNarrowsAsYouType(t *testing.T) {
	m := newTestModel(t, testStore())
	if len(m.cards) != 3 {
		t.Fatalf("want 3 cards to start, got %d", len(m.cards))
	}
	m = typeFilter(m, "sol")
	if len(m.cards) != 1 || m.cards[0].Name != "Sol Ring" {
		t.Errorf("cards = %v, want just Sol Ring", names(m.cards))
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(Model)
	if len(m.cards) != 2 {
		t.Errorf("cards = %v after backspace, want Sol Ring and Bitterblossom", names(m.cards))
	}
}

func TestFilterBarSwallowsCommandKeys(t *testing.T) {
	m := newTestModel(t, testStore())
	m = typeFilter(m, "q")
	if !m.filtering || m.filterText != "q" {
		t.Errorf("filtering=%v text=%q — q was treated as quit", m.filtering, m.filterText)
	}
	m = typeFilter(m, "s")
	if m.sortIdx[viewHoldings] != 0 {
		t.Error("s changed the sort while the filter bar was open")
	}
}

func TestFilterEnterKeepsEscapeClears(t *testing.T) {
	m := newTestModel(t, testStore())
	m = typeFilter(m, "sol")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.filtering {
		t.Error("enter left the bar open")
	}
	if len(m.cards) != 1 {
		t.Errorf("enter dropped the filter: %v", names(m.cards))
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if len(m.cards) != 3 {
		t.Errorf("esc did not clear the filter: %v", names(m.cards))
	}
}

func TestFilterEscapeFromTheBarAbandonsTheQuery(t *testing.T) {
	m := newTestModel(t, testStore())
	m = typeFilter(m, "sol")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.filtering || len(m.cards) != 3 {
		t.Errorf("esc left filtering=%v cards=%v", m.filtering, names(m.cards))
	}
}

func TestNameFilterNeverQueriesTheCatalog(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = typeFilter(m, "sol ring qty>1")
	if st.matchCalls != 0 {
		t.Errorf("made %d catalog queries for a name/qty filter, want 0", st.matchCalls)
	}
}

func TestTraitFilterQueriesTheCatalog(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = typeFilter(m, "rarity:mythic")
	if st.matchCalls == 0 {
		t.Fatal("a rarity filter did not query the catalog")
	}
	if len(m.cards) != 1 || m.cards[0].Name != "Bitterblossom" {
		t.Errorf("cards = %v, want the mythic", names(m.cards))
	}
}

func TestTraitAndHoldingTermsCompose(t *testing.T) {
	m := newTestModel(t, testStore())
	m = typeFilter(m, "rarity:mythic qty>3")
	if len(m.cards) != 1 {
		t.Errorf("cards = %v, want the 4-copy mythic", names(m.cards))
	}
	m = newTestModel(t, testStore())
	m = typeFilter(m, "rarity:mythic qty>10")
	if len(m.cards) != 0 {
		t.Errorf("cards = %v, want none", names(m.cards))
	}
}

func TestPartialQueryKeepsTheLastGoodResult(t *testing.T) {
	m := newTestModel(t, testStore())
	m = typeFilter(m, "qty>1")
	if m.filterErr != "" || len(m.cards) != 2 {
		t.Fatalf("setup: err=%q cards=%v", m.filterErr, names(m.cards))
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'>'}})
	m = next.(Model)
	if m.filterErr == "" {
		t.Error("want an error shown for a dangling comparison")
	}
	if len(m.cards) != 2 {
		t.Errorf("cards = %v, want the last good result held", names(m.cards))
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(Model)
	if m.filterErr != "" {
		t.Errorf("error %q persisted after the query became valid again", m.filterErr)
	}
	if len(m.cards) != 2 {
		t.Errorf("cards = %v, want the query working again", names(m.cards))
	}
}

func TestEmptyTraitResultExplainsAnUnrefreshedCatalog(t *testing.T) {
	st := testStore()
	st.traits = map[string][]string{}
	st.enriched = 0
	m := newTestModel(t, st)
	m = typeFilter(m, "rarity:mythic")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if got := m.statusLine(); !strings.Contains(got, "UpdatePrices") {
		t.Errorf("status = %q, want it to point at the UpdatePrices command", got)
	}
}

func TestFilterPersistsAcrossContainers(t *testing.T) {
	st := testStore()
	st.deckCards[202] = append(st.deckCards[202], entry("Sol Ring", "main", finish.Nonfoil, 1, 2))
	m := newTestModel(t, st)
	m = typeFilter(m, "ring")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	m = key(m, "left")
	m = key(m, "down")
	if len(m.cards) != 1 || m.cards[0].Name != "Sol Ring" {
		t.Errorf("deck cards = %v, want the filter still applied", names(m.cards))
	}
}

func findCard(m Model, name string) int {
	for i, c := range m.cards {
		if c.Name == name {
			return i
		}
	}
	return -1
}

func TestAdjustQuantity(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	i := findCard(m, "Bitterblossom")
	m.cursor[paneCards] = i

	m = key(m, "+")
	if got := m.cards[findCard(m, "Bitterblossom")].Quantity; got != 5 {
		t.Errorf("after +: qty = %d, want 5", got)
	}
	m = key(m, "-")
	m = key(m, "-")
	if got := m.cards[findCard(m, "Bitterblossom")].Quantity; got != 3 {
		t.Errorf("after two -: qty = %d, want 3", got)
	}
}

func TestAdjustQuantityToZeroRemovesTheRow(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	m.cursor[paneCards] = findCard(m, "Ancient Tomb")

	m = key(m, "-")
	if findCard(m, "Ancient Tomb") != -1 {
		t.Errorf("row survived being zeroed: %v", names(m.cards))
	}

	m = key(m, "u")
	if findCard(m, "Ancient Tomb") == -1 {
		t.Errorf("undo did not restore it: %v", names(m.cards))
	}
}

func TestUndoRestoresAQuantity(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	m.cursor[paneCards] = findCard(m, "Sol Ring")

	m = key(m, "+")
	m = key(m, "+")
	if got := m.cards[findCard(m, "Sol Ring")].Quantity; got != 5 {
		t.Fatalf("qty = %d, want 5", got)
	}
	m = key(m, "u")

	if got := m.cards[findCard(m, "Sol Ring")].Quantity; got != 4 {
		t.Errorf("after undo: qty = %d, want 4 (one level)", got)
	}
	m = key(m, "u")
	if !strings.Contains(m.status, "nothing to undo") {
		t.Errorf("status = %q, want the second undo to report nothing left", m.status)
	}
}

func TestRemoveAsksBeforeActing(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = key(m, "tab")
	m.cursor[paneCards] = findCard(m, "Sol Ring")

	m = key(m, "d")
	if m.confirm == nil {
		t.Fatal("d did not ask for confirmation")
	}
	if !strings.Contains(m.confirm.prompt, "Sol Ring") {
		t.Errorf("prompt = %q, want it to name the card", m.confirm.prompt)
	}
	if findCard(m, "Sol Ring") == -1 {
		t.Error("the card was removed before confirming")
	}

	m = key(m, "n")
	if m.confirm != nil || findCard(m, "Sol Ring") == -1 {
		t.Error("n did not cancel the removal")
	}
}

func TestConfirmHintNamesTheKeys(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	m.cursor[paneCards] = findCard(m, "Sol Ring")
	m = key(m, "d")
	if m.confirm == nil {
		t.Fatal("d staged no confirm")
	}
	if got := m.statusLine(); !strings.Contains(got, "y remove · any other key cancels") {
		t.Errorf("status line = %q, want the confirm's own keys", got)
	}

	m.confirm = &pendingConfirm{prompt: "sure?"}
	if got := m.statusLine(); !strings.Contains(got, "y/n") {
		t.Errorf("status line = %q, want the y/n fallback", got)
	}
}

func TestRemoveCardAndUndo(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	m.cursor[paneCards] = findCard(m, "Sol Ring")

	m = key(m, "d")
	m = key(m, "y")
	if findCard(m, "Sol Ring") != -1 {
		t.Fatalf("card not removed: %v", names(m.cards))
	}
	m = key(m, "u")
	if findCard(m, "Sol Ring") == -1 {
		t.Errorf("undo did not restore the card: %v", names(m.cards))
	}
}

func TestRemoveDeckAndUndo(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = key(m, "down")

	m = key(m, "d")
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "remove deck") {
		t.Fatalf("d on a deck did not stage a deck removal: %+v", m.confirm)
	}
	m = key(m, "y")
	for _, c := range m.containers {
		if c.Name == "Rich Deck" {
			t.Fatal("deck was not removed")
		}
	}

	m = key(m, "u")
	var found bool
	for _, c := range m.containers {
		if c.Name == "Rich Deck" {
			found = true
		}
	}
	if !found {
		t.Error("undo did not bring the deck back")
	}
}

func TestCollectionCannotBeRemoved(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "d")
	if m.confirm != nil {
		t.Error("staged a removal of the loose collection")
	}
	if !m.statusErr {
		t.Errorf("status = %q, want a refusal", m.status)
	}
}

func TestEditRefreshesContainerTotals(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = key(m, "tab")
	m.cursor[paneCards] = findCard(m, "Sol Ring")

	st.totals.TotalCopies = 99
	m = key(m, "+")
	if m.containers[1].Copies != 99 {
		t.Errorf("collection row = %d copies, want the re-read total", m.containers[1].Copies)
	}
}

func TestEditKeepsTheCursorInPlace(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	m.cursor[paneCards] = findCard(m, "Sol Ring")
	at := m.cursor[paneCards]

	m = key(m, "+")
	if m.cursor[paneCards] != at {
		t.Errorf("cursor moved from %d to %d after an edit", at, m.cursor[paneCards])
	}
	if m.cards[m.cursor[paneCards]].Name != "Sol Ring" {
		t.Errorf("cursor is on %q, want it still on Sol Ring", m.cards[m.cursor[paneCards]].Name)
	}
}

func TestDetailOpensAndCloses(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = key(m, "tab")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("enter did not open the detail")
	}
	if out := m.View(); !strings.Contains(out, "HELD") || !strings.Contains(out, "PRICE") {
		t.Errorf("detail view missing its sections:\n%s", out)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if next.(Model).detail != nil {
		t.Error("esc did not close the detail")
	}
}

func TestDetailSwallowsNavigationKeys(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	at := m.cursor[paneCards]

	m = key(m, "down")
	m = key(m, "s")
	if m.cursor[paneCards] != at {
		t.Error("the cursor moved behind the detail overlay")
	}
	if m.sortIdx[viewHoldings] != 0 {
		t.Error("s changed the sort behind the detail overlay")
	}
}

func TestViewCyclesAndLoads(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{
		{Name: "Riser", SetCode: "a", CollectorNumber: "1", Finish: finish.Nonfoil, Copies: 2, Old: 1, New: 5},
		{Name: "Sinker", SetCode: "b", CollectorNumber: "2", Finish: finish.Foil, Copies: 1, Old: 50, New: 10},
	}
	st.unpriced = []store.UnpricedRow{
		{Name: "No Price", SetCode: "c", CollectorNumber: "3", Finish: finish.Foil, Copies: 1, HeldIn: "Collection"},
	}
	m := atAllCards(t, newTestModel(t, st))

	m = key(m, "v")
	if m.view != viewMovers {
		t.Fatalf("view = %v, want movers", m.view)
	}

	if len(m.movers) != 2 || m.movers[0].Name != "Riser" {
		t.Errorf("movers = %+v, want the biggest gain first", m.movers)
	}
	if out := m.View(); !strings.Contains(out, "MOVERS") || !strings.Contains(out, "IMPACT") {
		t.Errorf("movers view not rendered:\n%s", out)
	}

	m = key(m, "v")
	if m.view != viewMarket {
		t.Errorf("view = %v, want market after movers", m.view)
	}
	m = key(m, "v")
	if m.view != viewDip {
		t.Errorf("view = %v, want dip after market", m.view)
	}
	m = key(m, "v")
	if m.view != viewWatches || len(m.unpriced) != 1 {
		t.Fatalf("view = %v with %d unpriced rows", m.view, len(m.unpriced))
	}

	if out := m.View(); !strings.Contains(out, "UNPRICED") || !strings.Contains(out, "HELD IN") {
		t.Errorf("unpriced table not rendered on the watches screen:\n%s", out)
	}
	m = key(m, "v")
	if m.view != viewHoldings {
		t.Errorf("view = %v, want back to holdings", m.view)
	}
}

func TestUnpricedEnterOpensDetail(t *testing.T) {
	st := testStore()
	st.unpriced = []store.UnpricedRow{
		{ScryfallID: "sf1", Name: "No Price", SetCode: "c", CollectorNumber: "3", Finish: finish.Foil, Copies: 1, HeldIn: "Collection"},
	}
	m := newTestModel(t, st)
	for m.view != viewWatches {
		m = key(m, "v")
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("enter on an unpriced row did not open the card detail")
	}
}

func TestSortWorksInEveryView(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{
		{Name: "Riser", SetCode: "a", CollectorNumber: "1", Finish: finish.Nonfoil, Copies: 2, Old: 1, New: 5},
		{Name: "Sinker", SetCode: "b", CollectorNumber: "2", Finish: finish.Foil, Copies: 1, Old: 50, New: 10},
	}
	st.unpriced = []store.UnpricedRow{
		{Name: "Zebra", SetCode: "c", CollectorNumber: "3", Finish: finish.Foil, Copies: 1, HeldIn: "Collection"},
		{Name: "Aardvark", SetCode: "d", CollectorNumber: "4", Finish: finish.Nonfoil, Copies: 5, HeldIn: "Deck"},
	}
	m := atAllCards(t, newTestModel(t, st))

	m = key(m, "v")
	m = key(m, "s")
	if m.sortLabel() != "name" || m.movers[0].Name != "Riser" {
		t.Errorf("movers by %s = %s first, want Riser", m.sortLabel(), m.movers[0].Name)
	}
	m = key(m, "S")
	if m.movers[0].Name != "Sinker" {
		t.Errorf("movers by %s = %s first, want Sinker", m.sortLabel(), m.movers[0].Name)
	}

	m = key(m, "v")
	m = key(m, "v")

	m = key(m, "v")
	if m.unpriced[0].Name != "Aardvark" {
		t.Fatalf("unpriced default = %s first, want name order", m.unpriced[0].Name)
	}
	for range 3 {
		m = key(m, "s")
	}
	if m.sortLabel() != "UNPRICED · qty" || m.unpriced[0].Name != "Aardvark" {
		t.Errorf("unpriced by %s = %s first, want the 5-copy card", m.sortLabel(), m.unpriced[0].Name)
	}

	m = key(m, "v")
	m = key(m, "v")
	if m.sortLabel() != "name (reversed)" || m.movers[0].Name != "Sinker" {
		t.Errorf("movers sort did not survive the round trip: %s, %s first",
			m.sortLabel(), m.movers[0].Name)
	}
}

func TestMarketSortIsPerTable(t *testing.T) {
	m := newTestModel(t, testStore())
	m.view = viewMarket
	opp := func(name string, buy, sell, lastSold float64) market.Opportunity {
		o := market.Opportunity{BuyAt: buy, SellAt: sell, Market: lastSold,
			HasMarket: lastSold > 0, HasRetail: true, HasBuy: sell > 0}
		o.Card.Name, o.Card.Copies = name, 1
		return o
	}
	m.marketAllRows = []market.Row{
		{Kind: market.KindProfit, Opportunity: opp("Zulu Profit", 1, 3, 0)},
		{Kind: market.KindProfit, Opportunity: opp("Alpha Profit", 2, 3, 0)},
		{Kind: market.KindLiquid, Opportunity: opp("Zulu Liquid", 1, 4, 5)},
		{Kind: market.KindLiquid, Opportunity: opp("Alpha Liquid", 1, 1, 2)},
	}
	m.deriveMarketPages()
	m.marketLoaded = true

	names := func() []string {
		out := make([]string, len(m.marketRows))
		for i, r := range m.marketRows {
			out[i] = r.Card.Name
		}
		return out
	}

	m = key(m, "s")
	if want := []string{"Alpha Profit", "Zulu Profit", "Zulu Liquid", "Alpha Liquid"}; !slices.Equal(names(), want) {
		t.Errorf("after sorting profits = %v, want %v", names(), want)
	}
	if m.sortLabel() != "arbitrage · name" {
		t.Errorf("label = %q, want the table named", m.sortLabel())
	}
	if m.cursor[paneCards] != 0 {
		t.Errorf("cursor = %d, want the sorted table's first row", m.cursor[paneCards])
	}

	m.cursor[paneCards] = 2
	m = key(m, "s")
	if want := []string{"Alpha Profit", "Zulu Profit", "Alpha Liquid", "Zulu Liquid"}; !slices.Equal(names(), want) {
		t.Errorf("after sorting liquid = %v, want %v", names(), want)
	}
	if m.sortLabel() != "liquid · name" {
		t.Errorf("label = %q", m.sortLabel())
	}
	if m.cursor[paneCards] != 2 {
		t.Errorf("cursor = %d, want the liquid table's first row", m.cursor[paneCards])
	}

	m = key(m, "S")
	if want := []string{"Alpha Profit", "Zulu Profit", "Zulu Liquid", "Alpha Liquid"}; !slices.Equal(names(), want) {
		t.Errorf("after reversing liquid = %v, want %v", names(), want)
	}
}

func TestViewRowCountFollowsTheMode(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{{Name: "One", New: 5}, {Name: "Two", New: 8}}
	m := newTestModel(t, st)
	if got := m.rowCount(paneCards); got != 3 {
		t.Errorf("holdings rowCount = %d, want 3", got)
	}
	m = atAllCards(t, m)
	m = key(m, "v")
	if got := m.rowCount(paneCards); got != 2 {
		t.Errorf("movers rowCount = %d, want 2", got)
	}
}

func TestAnalyticalViewsRefuseHoldingActions(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{
		{ScryfallID: "riser-id", Name: "Riser", Finish: finish.Nonfoil, Copies: 2, Old: 1, New: 5},
	}
	m := atAllCards(t, newTestModel(t, st))
	before := len(st.collection)
	qty := m.cards[0].Quantity

	m = key(m, "v")
	m = key(m, "+")
	if !m.statusErr || !strings.Contains(m.status, "press v") {
		t.Errorf("status = %q, want a refusal that says how to get back", m.status)
	}

	m = key(m, "d")
	if m.confirm != nil {
		t.Error("staged a removal from the movers view")
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("enter on a mover did not open the card detail")
	}
	m = key(m, "esc")

	m = key(m, "v")
	m = key(m, "v")
	if len(st.collection) != before || m.cards[0].Quantity != qty {
		t.Errorf("the collection changed: %d rows, top qty %d (want %d, %d)",
			len(st.collection), m.cards[0].Quantity, before, qty)
	}
}

func marketModel(t *testing.T, fn MarketFunc) Model {
	t.Helper()
	m, err := New(testStore(), WithMarket(fn))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 20})
	return next.(Model)
}

func opp(name string, buy, sell float64) market.Opportunity {
	return market.Opportunity{
		Card: store.OwnedFinish{ScryfallID: "sf-" + name, Name: name,
			SetCode: "mh3", CollectorNumber: "1", Finish: finish.Nonfoil, Copies: 1},
		Market:    buy,
		BuyAt:     buy,
		SellAt:    sell,
		HasMarket: true,
		HasRetail: true,
		HasBuy:    sell > 0,
	}
}

func TestMarketArrivalFetchesOnlyWithoutData(t *testing.T) {
	var calls int
	m := marketModel(t, func(context.Context, progress.Fn, float64) (market.Result, error) {
		calls++
		return market.Result{Compared: 1}, nil
	})
	for m.view != viewMarket {
		m = key(m, "v")
	}
	if !m.marketLoading {
		t.Fatal("an empty arrival must start the fetch itself")
	}
	next, _ := m.Update(marketMsg{gen: m.marketGen, res: market.Result{Compared: 1}})
	m = next.(Model)
	if !m.marketLoaded || m.marketLoading {
		t.Fatalf("loaded=%v loading=%v after the reply", m.marketLoaded, m.marketLoading)
	}
	for range len(viewCycle) {
		m = key(m, "v")
	}
	if m.view != viewMarket || m.marketLoading {
		t.Errorf("view %v loading %v, want loaded data left alone on return", m.view, m.marketLoading)
	}
	_ = calls
}

func TestArbitrageFetchesOnFAndRenders(t *testing.T) {
	res := market.Result{
		Opportunities: []market.Opportunity{opp("Profitable", 2, 20), opp("Liquid", 10, 9)},
		Compared:      2,
	}
	m := marketModel(t, func(context.Context, progress.Fn, float64) (market.Result, error) { return res, nil })
	for m.view != viewMarket {
		m = key(m, "v")
	}
	if !m.marketLoading {
		t.Fatal("arrival did not start a fetch")
	}
	if out := m.View(); !strings.Contains(out, "reading today's vendor prices") {
		t.Errorf("no progress shown:\n%s", out)
	}

	next, _ := m.Update(marketMsg{gen: m.marketGen, res: res})
	m = next.(Model)

	if m.marketLoading || !m.marketLoaded {
		t.Fatalf("loading=%v loaded=%v", m.marketLoading, m.marketLoaded)
	}
	if len(m.marketRows) == 0 {
		t.Fatal("no rows after a successful fetch")
	}
	out := m.View()
	for _, want := range []string{"ARBITRAGE", "buylist pays more", "Profitable", "BUYLIST NEAR MARKET", "+$18.00"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
}

func TestStaleArbitrageReplyIsDiscarded(t *testing.T) {
	m := marketModel(t, func(context.Context, progress.Fn, float64) (market.Result, error) {
		return market.Result{}, nil
	})
	for m.view != viewMarket {
		m = key(m, "v")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	stale := m.marketGen - 1

	next, _ = m.Update(marketMsg{
		gen: stale,
		res: market.Result{Opportunities: []market.Opportunity{opp("Ghost", 1, 5)}},
	})
	m = next.(Model)
	if m.marketLoaded || len(m.marketRows) != 0 {
		t.Errorf("a stale reply landed: loaded=%v rows=%d", m.marketLoaded, len(m.marketRows))
	}
}

func TestArbitrageUnavailableWithoutAFetcher(t *testing.T) {
	m := newTestModel(t, testStore())
	for m.view != viewMarket {
		m = key(m, "v")
	}
	m = key(m, "F")
	if !m.statusErr || !strings.Contains(m.status, "unavailable") {
		t.Errorf("status = %q, want it to say arbitrage is unavailable", m.status)
	}
}

func TestArbitrageErrorIsShown(t *testing.T) {
	m := marketModel(t, func(context.Context, progress.Fn, float64) (market.Result, error) {
		return market.Result{}, errFake{}
	})
	for m.view != viewMarket {
		m = key(m, "v")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.Update(marketMsg{gen: m.marketGen, err: errFake{}})
	m = next.(Model)
	if !m.statusErr {
		t.Errorf("status = %q, want the failure surfaced", m.status)
	}
}

func TestArbitrageRefusesHoldingActions(t *testing.T) {
	m := marketModel(t, func(context.Context, progress.Fn, float64) (market.Result, error) {
		return market.Result{}, nil
	})
	for m.view != viewMarket {
		m = key(m, "v")
	}
	m = key(m, "+")
	if !m.statusErr {
		t.Errorf("status = %q, want a refusal", m.status)
	}
}

type capturingMarket struct {
	mu  sync.Mutex
	ctx context.Context
}

func (c *capturingMarket) fetch(ctx context.Context, _ progress.Fn, _ float64) (market.Result, error) {
	c.mu.Lock()
	c.ctx = ctx
	c.mu.Unlock()
	<-ctx.Done()
	return market.Result{}, ctx.Err()
}

func (c *capturingMarket) context() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ctx
}

func startFetch(t *testing.T) (Model, *capturingMarket) {
	t.Helper()
	cap := &capturingMarket{}
	m := marketModel(t, cap.fetch)

	for m.view.next() != viewMarket {
		m = key(m, "v")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = next.(Model)
	if cmd == nil || !m.marketLoading {
		t.Fatal("the empty arrival did not start a fetch")
	}

	go func() {
		if batch, ok := cmd().(tea.BatchMsg); ok {
			for _, c := range batch {
				if c != nil {
					go c()
				}
			}
		}
	}()
	for range 200 {
		if cap.context() != nil {
			return m, cap
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the fetch never received a context")
	return m, cap
}

func TestArbitrageEscapeCancels(t *testing.T) {
	m, cap := startFetch(t)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.marketLoading {
		t.Error("still loading after esc")
	}
	if cap.context().Err() == nil {
		t.Error("esc did not cancel the fetch's context")
	}

	next, _ = m.Update(marketMsg{gen: m.marketGen, err: context.Canceled})
	if got := next.(Model); got.statusErr {
		t.Errorf("cancellation reported as an error: %q", got.status)
	}
}

func TestArbitrageViewChangeCancels(t *testing.T) {
	m, cap := startFetch(t)

	m = key(m, "v")
	if m.marketLoading {
		t.Error("still loading after leaving the view")
	}
	if m.view != viewDip {
		t.Errorf("view = %v, want dip", m.view)
	}
	if cap.context().Err() == nil {
		t.Error("leaving the view did not cancel the fetch")
	}
}

func TestAddKeyRequestsTheCascade(t *testing.T) {
	m := newTestModel(t, testStore())
	m.newAddChild = func() (tui.Child, error) {
		return tui.NewChild(context.Background(), childSearcher{}, noopChildAdder, nil, "", nil), nil
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got := next.(Model)

	if got.addChild == nil {
		t.Fatal("a did not open the embedded cascade")
	}
	if got.mode() != modeAddChild {
		t.Errorf("mode = %v, want modeAddChild", got.mode())
	}
}

func TestAddKeyIsInTheHelp(t *testing.T) {
	m := newTestModel(t, testStore())
	for _, focus := range []pane{paneContainers, paneCards} {
		m.focus = focus
		if !strings.Contains(m.helpLine(), "a add") {
			t.Errorf("focus %v help = %q, want it to mention adding", focus, m.helpLine())
		}
	}
}

func TestAddKeyIsTextWhileFiltering(t *testing.T) {
	m := newTestModel(t, testStore())
	m = typeFilter(m, "a")
	if m.addChild != nil || strings.Contains(m.status, "unavailable") {
		t.Error("typing a into the filter bar requested the add cascade")
	}
	if m.filterText != "a" {
		t.Errorf("filterText = %q", m.filterText)
	}
}

func TestNoEstimateMarkerInHoldings(t *testing.T) {
	st := testStore()
	st.collection[1].AltSource = "cardkingdom"
	m := newTestModel(t, st)

	if out := m.View(); strings.Contains(out, "*") {
		t.Errorf("holdings view still carries an estimate marker:\n%s", out)
	}
	if got := m.statusLine(); strings.Contains(got, "estimated") {
		t.Errorf("status = %q, want no estimate legend", got)
	}
}

func TestMultipleBindersEachGetARow(t *testing.T) {
	st := testStore()
	m := newTestModel(t, &multiBinderStore{fakeStore: st})
	view := m.View()
	for _, want := range []string{store.LooseName, "Trade Stock"} {
		if !strings.Contains(view, want) {
			t.Errorf("left pane is missing binder %q:\n%s", want, view)
		}
	}

	m = key(m, "down")
	sel := m.selectedContainer()
	if sel == nil || sel.Name != "Trade Stock" {
		t.Fatalf("selected = %+v, want the Trade Stock binder", sel)
	}
	if ok, why := m.editable(); !ok {
		t.Errorf("a named binder is not editable: %s", why)
	}
}

type multiBinderStore struct{ *fakeStore }

func (m *multiBinderStore) ListBinders() ([]store.DeckSummary, error) {
	bs, err := m.fakeStore.ListBinders()
	if err != nil {
		return nil, err
	}
	b := store.DeckSummary{TotalCopies: 2, Value: 20}
	b.ID = 42
	b.Name = "Trade Stock"
	b.Kind = store.KindCollection
	return append(bs, b), nil
}

func TestQuitPolicy(t *testing.T) {
	m := newTestModel(t, testStore())

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("q must not quit without asking")
	}
	if m.confirm == nil || m.confirm.prompt != "quit hoard?" {
		t.Fatalf("q staged %+v, want the quit confirm", m.confirm)
	}
	m = key(m, "n")
	if m.confirm != nil {
		t.Fatal("declining q must stay")
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if cmd != nil || m.confirm == nil || m.confirm.prompt != "quit hoard?" {
		t.Fatalf("esc at top: cmd=%v confirm=%+v", cmd, m.confirm)
	}

	m = key(m, "n")
	if m.confirm != nil {
		t.Fatal("decline must clear the confirm")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("y on the quit confirm must quit")
	}
	_ = next

	m2 := newTestModel(t, testStore())
	_, cmd = m2.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c must quit")
	}

	m3 := newTestModel(t, testStore())
	m3.detail = &detail{}
	next, cmd = m3.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m3 = next.(Model)
	if cmd != nil || m3.detail == nil {
		t.Fatal("q must do nothing on the detail overlay")
	}
}

func TestHelpLineIsViewSpecific(t *testing.T) {
	m := newTestModel(t, testStore())
	m.view = viewWatches

	if h := m.helpLine(); !strings.Contains(h, "w edit threshold") || !strings.Contains(h, ": commands") {
		t.Errorf("watches help = %q", h)
	}

	if h := m.helpLine(); !strings.Contains(h, "F refresh prices") ||
		!strings.Contains(h, "s sort") || !strings.Contains(h, "]/[ next/prev table") {
		t.Errorf("watches help lost the unpriced table's keys = %q", h)
	}
	m.view = viewMovers
	if h := m.helpLine(); !strings.Contains(h, "W lookback 7/30/90 days") {
		t.Errorf("movers help = %q", h)
	}
	m.view = viewHoldings
	if h := m.helpLine(); !strings.Contains(h, "q quit") {
		t.Errorf("holdings help must advertise q again: %q", h)
	}
}

func TestArbitrageLiquidRowIsNotAGain(t *testing.T) {
	res := market.Result{
		Opportunities: []market.Opportunity{
			opp("Gilded Lotus", 10.00, 9.00),
			opp("Quantum Misalignment", 6.31, 2.80),
		},
		Compared: 2,
	}
	m := marketModel(t, func(context.Context, progress.Fn, float64) (market.Result, error) { return res, nil })
	for m.view != viewMarket {
		m = key(m, "v")
	}
	m = key(m, "F")
	next, _ := m.Update(marketMsg{gen: m.marketGen, res: res})
	m = next.(Model)

	out := m.View()

	for _, want := range []string{"BUYLIST NEAR MARKET", "TCG SOLD", "BUYLIST", "PAYS", "90.0%"} {
		if !strings.Contains(out, want) {
			t.Errorf("liquid section missing %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "Quantum Misalignment") {
		t.Errorf("sub-floor row must not be listed at all:\n%s", out)
	}
	if !strings.Contains(out, "pays $9.00 · tcg last sold for $10.00") {
		t.Errorf("status should state both prices plainly:\n%s", out)
	}
}

func loadedMarket(t *testing.T, res market.Result) Model {
	t.Helper()
	m := marketModel(t, func(context.Context, progress.Fn, float64) (market.Result, error) { return res, nil })
	for m.view != viewMarket {
		m = key(m, "v")
	}
	m = key(m, "F")
	next, _ := m.Update(marketMsg{gen: m.marketGen, res: res})
	return next.(Model)
}

func bandRes() market.Result {
	return market.Result{
		Opportunities: []market.Opportunity{
			opp("Profitable", 10.00, 28.00),
			opp("Gilded Lotus", 10.00, 9.00),
			opp("Fleeced Alchemist", 10.00, 2.00),
		},
		Compared: 3,
	}
}

func TestBuylistBandFlipsToTheLowballs(t *testing.T) {
	m := loadedMarket(t, bandRes())
	m = key(m, "]")

	if sec, _ := m.marketCursorPos(); sec != int(market.KindLiquid) {
		t.Fatalf("cursor section = %d, want the buylist table", sec)
	}
	out := m.View()
	if !strings.Contains(out, "BUYLIST NEAR MARKET") || strings.Contains(out, "Fleeced Alchemist") {
		t.Fatalf("near-market band should lead with the good guys:\n%s", out)
	}

	m = key(m, "b")
	if !m.liquidLowball {
		t.Fatal("'b' in the buylist table did not flip the band")
	}
	out = m.View()
	for _, want := range []string{"BUYLIST LOWBALL", "under 50%", "Fleeced Alchemist", "20.0%"} {
		if !strings.Contains(out, want) {
			t.Errorf("lowball band missing %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "BUYLIST NEAR MARKET") || strings.Contains(out, "Gilded Lotus") {
		t.Errorf("the good guys must leave when the bad guys arrive:\n%s", out)
	}

	if note := m.selectedMarketNote(); !strings.Contains(note, "pays only $2.00 · tcg last sold for $10.00") {
		t.Errorf("row note = %q, want the offer read as an insult", note)
	}

	m = key(m, "b")
	if m.liquidLowball {
		t.Fatal("'b' did not flip the band back")
	}
	if out := m.View(); !strings.Contains(out, "BUYLIST NEAR MARKET") || !strings.Contains(out, "Gilded Lotus") {
		t.Errorf("flipping back should restore the good guys:\n%s", out)
	}
}

func TestLowballBandLeadsWithTheWorstOffer(t *testing.T) {
	m := loadedMarket(t, market.Result{
		Opportunities: []market.Opportunity{
			opp("Mild Haircut", 10.00, 4.50),
			opp("Daylight Robbery", 10.00, 1.00),
		},
		Compared: 2,
	})
	if m.marketSectionTotals()[market.KindLiquid] != 0 {
		t.Fatal("this test needs an empty near-market band")
	}

	for sec, _ := m.marketCursorPos(); sec != int(market.KindLiquid); sec, _ = m.marketCursorPos() {
		m = key(m, "]")
	}
	m = key(m, "b")
	if !m.liquidLowball {
		t.Fatal("'b' on the empty band's heading did not reach the other band")
	}

	var got []string
	for _, r := range m.marketRows {
		if r.Kind == market.KindLiquid {
			got = append(got, r.Card.Name)
		}
	}
	if len(got) != 2 || got[0] != "Daylight Robbery" {
		t.Errorf("lowball rows = %v, want the worst offer first", got)
	}
}

func TestBandKeyOnACompSheetFlipsTheSide(t *testing.T) {
	res := bandRes()
	res.Comps = []market.Comp{comp("Ancient Tomb", 60, 55, 44)}
	m := loadedMarket(t, res)
	for m.selectedComp() == nil {
		m = key(m, "]")
	}

	m = key(m, "b")
	if !m.compsBuySide {
		t.Error("'b' on a comp sheet should flip the comps side")
	}
	if m.liquidLowball {
		t.Error("'b' on a comp sheet must not touch the buylist band")
	}
}

func TestEmptyMarketTableHeadingIsSelectable(t *testing.T) {
	res := bandRes()
	res.Comps = []market.Comp{comp("Ancient Tomb", 60, 55, 44)}
	m := loadedMarket(t, res)

	if sec, _ := m.marketCursorPos(); m.marketSections()[sec].count == 0 {
		t.Fatalf("arrival parked the cursor on an empty heading (section %d)", sec)
	}

	m.marketAllRows = nil
	m.marketRows = nil
	m.applyMarketComps(res.Comps)
	if got, want := m.marketCursorSlots(), m.marketTotalRows(); got != want+2 {
		t.Errorf("cursor slots = %d, rows = %d: each empty table should add a slot", got, want)
	}

	for sec, _ := m.marketCursorPos(); sec != int(market.KindLiquid); sec, _ = m.marketCursorPos() {
		m = key(m, "]")
	}
	if m.selectedComp() != nil || m.selectedMarketRow() != nil {
		t.Error("an empty heading must address no row")
	}
	if got := m.marketStatus(); !strings.Contains(got, "BUYLIST NEAR MARKET · empty") {
		t.Errorf("status = %q, want the empty table naming itself", got)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if next.(Model).detail != nil {
		t.Error("enter on an empty heading must not open a detail")
	}
}

func TestBandKeyDoesNothingOnTheArbitrageTable(t *testing.T) {
	res := bandRes()
	res.Comps = []market.Comp{comp("Ancient Tomb", 60, 55, 44)}
	m := loadedMarket(t, res)
	if sec, _ := m.marketCursorPos(); sec != int(market.KindProfit) {
		t.Fatalf("cursor section = %d, want the arbitrage table", sec)
	}

	m = key(m, "b")
	if m.compsBuySide || m.liquidLowball {
		t.Errorf("'b' on the arbitrage table flipped something: comps=%v band=%v",
			m.compsBuySide, m.liquidLowball)
	}
}

func TestHoldingsPaletteRanksCollectionVerbs(t *testing.T) {
	m, err := New(testStore(),
		WithAddCascade(func() (tui.Child, error) { return tui.Child{}, nil }),
		WithDeckAddByURL(func(context.Context, progress.Fn, string) (OpReport, error) {
			return OpReport{}, nil
		}),
		WithImportFile(func(context.Context, progress.Fn, string, bool) (OpReport, error) {
			return OpReport{}, nil
		}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)
	m.openPalette()
	if len(m.palette.matches) < 4 {
		t.Fatal("palette too small")
	}
	first := m.commands[m.palette.matches[0].index].id
	if first != "add" {
		t.Errorf("first palette entry = %q, want add", first)
	}
	top := map[string]bool{}
	for _, pm := range m.palette.matches[:min(6, len(m.palette.matches))] {
		top[m.commands[pm.index].id] = true
	}
	for _, want := range []string{"deck.add-url", "binder.new", "import.file"} {
		if !top[want] {
			t.Errorf("%s missing from the palette's top suggestions", want)
		}
	}
}

func TestHoldingsHelpMentionsBinderAndInterop(t *testing.T) {
	m := newTestModel(t, testStore())
	m.focus = paneContainers
	h := m.helpLine()
	for _, want := range []string{"n new binder", "R rename", ": commands"} {
		if !strings.Contains(h, want) {
			t.Errorf("containers help missing %q: %q", want, h)
		}
	}
}

func TestHelpLineWrapsInsteadOfTruncating(t *testing.T) {
	m := newTestModel(t, testStore())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m = next.(Model)

	lines := ui.WrapHelp(m.helpLine(), 60)
	if len(lines) < 2 {
		t.Fatalf("holdings help should wrap at width 60, got %d line(s)", len(lines))
	}
	for _, l := range lines {
		if len([]rune(l)) > 60 {
			t.Errorf("wrapped line overflows: %q", l)
		}
	}
	out := m.View()
	if !strings.Contains(out, "q quit") {
		t.Error("the last help entry must survive the wrap")
	}
	if strings.Contains(out, "…") {
		t.Errorf("help must not be truncated:\n%s", out)
	}

	next, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	wide := strings.Count(next.(Model).View(), "\n")
	if narrow := strings.Count(out, "\n"); narrow != wide {
		t.Errorf("frame height changed with wrapping: narrow=%d wide=%d", narrow, wide)
	}
}

func TestDetailPaletteKeepsOverlay(t *testing.T) {
	m := newCascadeModel(t, testStore(),
		WithUpdatePrices(func(ctx context.Context, p progress.Fn) (string, error) {
			return "prices updated", nil
		}))
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("setup: no detail open")
	}

	m = key(m, ":")
	if m.detail == nil || m.palette == nil {
		t.Fatal("the palette must open over the overlay, not replace it")
	}
	if out := m.View(); !strings.Contains(out, "▸ ") {
		t.Fatalf("palette drawer not rendered over the detail:\n%s", out)
	}

	m, cmd := runPaletteCommand(t, m, "op.update-prices")
	m.palette = nil
	if m.detail == nil {
		t.Fatal("running an op must not close the overlay")
	}
	if m.op == nil || cmd == nil {
		t.Fatal("the op must start behind the overlay")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.detail != nil {
		t.Fatal("esc must close the overlay")
	}
}

func TestDetailPromptRendersOverOverlay(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("setup: no detail open")
	}
	m, _ = runPaletteCommand(t, m, "binder.new")
	if m.prompt == nil || m.detail == nil {
		t.Fatalf("prompt=%v detail=%v", m.prompt, m.detail)
	}
	if out := m.View(); !strings.Contains(out, m.prompt.label) {
		t.Fatalf("prompt invisible behind the overlay:\n%s", out)
	}
}

func TestArbitrageFAlwaysAnswers(t *testing.T) {
	res := market.Result{
		Opportunities: []market.Opportunity{opp("Profitable", 2, 20)},
		Compared:      1,
	}
	m := marketModel(t, func(context.Context, progress.Fn, float64) (market.Result, error) { return res, nil })
	for m.view != viewMarket {
		m = key(m, "v")
	}
	m = key(m, "F")
	if !m.marketLoading {
		t.Fatal("first F did not start the fetch")
	}
	m = key(m, "F")
	if !strings.Contains(m.status, "already fetching") {
		t.Fatalf("F while loading = %q, want the already-fetching status", m.status)
	}

	next, _ := m.Update(marketMsg{gen: m.marketGen, res: res})
	m = next.(Model)
	if !m.marketLoaded {
		t.Fatal("setup: fetch did not land")
	}
	m = key(m, "F")
	if !m.marketLoading {
		t.Fatal("F on a loaded view must re-fetch, not fall silent")
	}
}

func TestArbitrageEnterOpensDetail(t *testing.T) {
	res := market.Result{
		Opportunities: []market.Opportunity{opp("Profitable", 2, 20)},
		Compared:      1,
	}
	m := marketModel(t, func(context.Context, progress.Fn, float64) (market.Result, error) { return res, nil })
	for m.view != viewMarket {
		m = key(m, "v")
	}
	m = key(m, "F")
	next, _ := m.Update(marketMsg{gen: m.marketGen, res: res})
	m = next.(Model)
	m.focus = paneCards

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("enter on an arbitrage row must open the detail")
	}
}

func TestValueFloorCyclesAndFilters(t *testing.T) {
	m := newTestModel(t, testStore())
	all := len(m.cards)
	if all == 0 {
		t.Fatal("setup: no cards")
	}

	counts := []int{}
	for range 3 {
		m = key(m, "M")
		counts = append(counts, len(m.cards))
	}

	if !(counts[0] >= counts[1] && counts[1] >= counts[2]) {
		t.Fatalf("floor counts not monotonic: %v", counts)
	}
	if counts[2] >= all {
		t.Fatalf("$25 floor hid nothing: %d of %d", counts[2], all)
	}
	for _, c := range m.cards {
		if c.Price == nil || *c.Price < 25 {
			t.Fatalf("card %s under the $25 floor still visible", c.Name)
		}
	}
	if !strings.Contains(m.status, "floor $25.00") {
		t.Errorf("cycle status = %q", m.status)
	}

	m.status = ""
	if !strings.Contains(m.View(), "floor $25.00") {
		t.Errorf("floor indicator missing:\n%s", m.View())
	}

	for range len(floorLevels) - 3 {
		m = key(m, "M")
	}
	if len(m.cards) != all {
		t.Fatalf("floor off restored %d of %d cards", len(m.cards), all)
	}

	m = key(m, "M")
	m = key(m, "v")
	m = key(m, "v")
	before := len(m.unpriced)
	m = key(m, "M")
	if len(m.unpriced) != before {
		t.Fatalf("unpriced view changed under the floor: %d → %d", before, len(m.unpriced))
	}
}

func TestArbitrageLoadsFromCacheOnArrival(t *testing.T) {
	res := market.Result{
		Opportunities: []market.Opportunity{opp("Profitable", 2, 20)},
		Compared:      1,
	}
	fetches := 0
	m, err := New(testStore(),
		WithMarket(func(context.Context, progress.Fn, float64) (market.Result, error) {
			fetches++
			return res, nil
		}),
		WithMarketCached(func(float64) (market.Result, bool) { return res, true }))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ctx = context.Background()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)

	for m.view != viewMarket {
		m = key(m, "v")
	}
	if !m.marketLoaded || len(m.marketRows) == 0 {
		t.Fatal("arrival must populate from the day cache")
	}
	if fetches != 0 {
		t.Fatal("arrival must not fetch")
	}
	if !strings.Contains(m.status, "earlier today") {
		t.Fatalf("status = %q, want the from-cache note", m.status)
	}

	m = key(m, "F")
	if !m.marketLoading {
		t.Fatal("F must still refetch fresh numbers")
	}
}

func TestArbitrageArrivalWithoutCacheStaysEmpty(t *testing.T) {
	m, err := New(testStore(),
		WithMarket(func(context.Context, progress.Fn, float64) (market.Result, error) {
			return market.Result{}, nil
		}),
		WithMarketCached(func(float64) (market.Result, bool) { return market.Result{}, false }))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ctx = context.Background()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)
	for m.view != viewMarket {
		m = key(m, "v")
	}
	if m.marketLoaded {
		t.Fatal("no cache: the view must wait for F")
	}
}

func TestDetailLinesCardFrameOrder(t *testing.T) {
	m := newTestModel(t, testStore())
	p := func(s string) *string { return &s }
	d := detail{card: store.CardDetail{
		Card: store.Card{Name: "Ulamog, the Infinite Gyre", SetCode: "uma",
			CollectorNumber: "7", ColorIdentity: []string{}, ManaCost: p("{11}")},
		TypeLine: "Legendary Creature — Eldrazi", Rarity: "mythic",
		OracleText: "Annihilator 4", FlavorText: "A rising dread.",
		Power: "10", Toughness: "10",
		Artist: "Mark Tedin", SetName: "Ultimate Masters", ReleasedAt: "2018-12-07",
		Enriched: true,
	}}
	lines := m.detailLines(d, 80)
	joined := strings.Join(lines, "\n")

	last := -1
	for _, want := range []string{
		"Ulamog, the Infinite Gyre", "{11}",
		"Legendary Creature — Eldrazi · mythic",
		"Annihilator 4",
		"A rising dread.",
		"10/10",
		"Mark Tedin · Ultimate Masters · uma/7 · 2018-12-07",
		"HELD", "PRICE",
	} {
		i := strings.Index(joined, want)
		if i < 0 {
			t.Fatalf("detail is missing %q:\n%s", want, joined)
		}
		if i < last {
			t.Errorf("%q renders out of card-frame order:\n%s", want, joined)
		}
		last = i
	}
	for _, l := range lines {
		if strings.HasSuffix(strings.TrimRight(l, " "), "10/10") && !strings.HasPrefix(l, "  ") {
			t.Errorf("the stat box is not anchored right: %q", l)
		}
	}

	bare := detail{card: store.CardDetail{Card: store.Card{Name: "Mystery"}}}
	if got := strings.Join(m.detailLines(bare, 80), "\n"); !strings.Contains(got, "run UpdatePrices") {
		t.Errorf("unenriched detail lost its remedy line:\n%s", got)
	}
}

func TestDetailImageAttachesToItsCard(t *testing.T) {
	m := newTestModel(t, testStore())
	m.imgTier = ui.ImageHalfblock
	m.imageFetch = func(ctx context.Context, id, url string) (image.Image, error) {
		img := image.NewRGBA(image.Rect(0, 0, 2, 4))
		return img, nil
	}
	m.detail = &detail{card: store.CardDetail{}}
	m.detail.card.ScryfallID = "sf1"
	m.detail.card.ImageURI = "https://img/card.jpg"

	cmd := m.fetchDetailImage()
	if cmd == nil {
		t.Fatal("no fetch command despite tier, fetcher and URL")
	}
	msg, ok := cmd().(imageMsg)
	if !ok || msg.scryfallID != "sf1" || len(msg.lines) == 0 {
		t.Fatalf("fetch produced %#v", msg)
	}

	next, _ := m.Update(msg)
	if got := next.(Model).detail.image; len(got) == 0 {
		t.Error("image did not attach to its detail")
	}

	m.detail = &detail{card: store.CardDetail{}}
	m.detail.card.ScryfallID = "other"
	next, _ = m.Update(msg)
	if got := next.(Model).detail.image; got != nil {
		t.Error("a stale image decorated the wrong card")
	}

	m.imgTier = ui.ImageNone
	if m.fetchDetailImage() != nil {
		t.Error("fetch offered on an incapable terminal")
	}
}

func TestDetailLinesHintOnlyWhenUnfetched(t *testing.T) {
	m := newTestModel(t, testStore())
	const hint = "card details not stored yet"

	sparse := detail{card: store.CardDetail{
		Card:     store.Card{Name: "Grizzly Bears", SetCode: "dom", CollectorNumber: "1"},
		TypeLine: "Creature — Bear", Rarity: "common",
		Power: "2", Toughness: "2", Enriched: true,
	}}
	if got := strings.Join(m.detailLines(sparse, 80), "\n"); strings.Contains(got, hint) {
		t.Errorf("a fetched card with sparse data must not be offered a refresh:\n%s", got)
	}

	unfetched := detail{card: store.CardDetail{
		Card: store.Card{Name: "Grizzly Bears", SetCode: "dom", CollectorNumber: "1"},
	}}
	if got := strings.Join(m.detailLines(unfetched, 80), "\n"); !strings.Contains(got, hint) {
		t.Errorf("an unfetched printing must be told to refresh:\n%s", got)
	}
}

func TestDetailImageNeedsAURL(t *testing.T) {
	m := newTestModel(t, testStore())
	m.imgTier = ui.ImageHalfblock
	m.imageFetch = func(ctx context.Context, id, url string) (image.Image, error) {
		if url == "" {
			t.Error("the fetcher was handed an empty image URL")
		}
		return image.NewRGBA(image.Rect(0, 0, 2, 4)), nil
	}

	m.detail = &detail{card: store.CardDetail{}}
	m.detail.card.ScryfallID = "sf1"
	if cmd := m.fetchDetailImage(); cmd != nil {
		cmd()
		t.Error("a printing with no stored image URL must not start a fetch")
	}

	m.detail.card.ImageURI = "https://img/card.jpg"
	if m.fetchDetailImage() == nil {
		t.Error("no fetch started for a printing that does have an image URL")
	}
}

func TestDetailImageStacksWhenNarrow(t *testing.T) {
	m := newTestModel(t, testStore())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 30})
	m = next.(Model)
	m.detail = &detail{card: store.CardDetail{}}
	m.detail.card.Name = "Sol Ring"
	m.detail.card.ImageURI = "https://img/x.jpg"
	m.detail.image = []string{"IMGROW1", "IMGROW2"}

	out := m.View()
	name := strings.Index(out, "Sol Ring")
	img := strings.Index(out, "IMGROW1")
	held := strings.Index(out, "HELD")
	if name < 0 || img < 0 || held < 0 {
		t.Fatalf("stacked layout missing a block (name %d, image %d, held %d):\n%s",
			name, img, held, out)
	}
	if !(name < img && img < held) {
		t.Errorf("want card details, then image, then HELD — got name %d, image %d, held %d",
			name, img, held)
	}
}

func TestAllCardsRowMergesEveryContainer(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	binderCount := len(m.cards)

	m.focus = paneContainers
	m.cursor[paneContainers] = 0
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}
	deckCards := 0
	for _, entries := range st.deckCards {
		deckCards += len(entries)
	}
	if want := binderCount + deckCards; len(m.cards) != want {
		t.Fatalf("merged cards = %d, want %d (binder %d + decks %d)",
			len(m.cards), want, binderCount, deckCards)
	}
	if out := m.View(); !strings.Contains(out, "ALL CARDS") {
		t.Errorf("header does not name the merged view:\n%s", out)
	}

	for _, banned := range []string{"+/- qty", "d remove", "R rename", "u undo"} {
		m.focus = paneContainers
		if h := m.helpLine(); strings.Contains(h, banned) {
			t.Errorf("containers help on the merged view offers %q: %s", banned, h)
		}
		m.focus = paneCards
		if h := m.helpLine(); strings.Contains(h, banned) {
			t.Errorf("cards help on the merged view offers %q: %s", banned, h)
		}
	}
	m.focus = paneContainers

	m = key(m, "tab")
	m = key(m, "+")
	if !m.statusErr || !strings.Contains(m.status, "merges every container") {
		t.Errorf("edit on the merged view: status = %q, want the refusal", m.status)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if next.(Model).detail == nil {
		t.Error("enter on a merged row did not open the detail")
	}
}

func TestAllCardsRowRefusesContainerEdits(t *testing.T) {
	m := newTestModel(t, testStore())
	m.focus = paneContainers
	m.cursor[paneContainers] = 0

	m = key(m, "R")
	if m.prompt != nil || !strings.Contains(m.status, "no name of its own") {
		t.Errorf("rename: prompt=%v status=%q", m.prompt, m.status)
	}
	m = key(m, "d")
	if m.confirm != nil || !strings.Contains(m.status, "remove its subsets") {
		t.Errorf("remove: confirm=%v status=%q", m.confirm, m.status)
	}
}

func comp(name string, value, low, buylist float64) market.Comp {
	c := market.Comp{
		Card: store.OwnedFinish{ScryfallID: "sf-" + name, Name: name,
			SetCode: "mh3", CollectorNumber: "9", Finish: finish.Nonfoil, Copies: 1, Value: value},
		Market: low, HasMarket: true, Low: low, LowFrom: "tcgplayer",
	}
	if buylist > 0 {
		c.Buylist, c.BuylistTo, c.HasBuylist = buylist, "cardkingdom", true
	}
	return c
}

func TestCompsSectionCursorDetailAndWatch(t *testing.T) {
	res := market.Result{
		Opportunities: []market.Opportunity{opp("Profitable", 2, 20)},
		Comps:         []market.Comp{comp("Sheeted", 60, 55, 44)},
		Compared:      2,
	}
	m := marketModel(t, func(context.Context, progress.Fn, float64) (market.Result, error) { return res, nil })
	for m.view != viewMarket {
		m = key(m, "v")
	}
	m = key(m, "F")
	next, _ := m.Update(marketMsg{gen: m.marketGen, res: res})
	m = next.(Model)
	m.focus = paneCards

	view := strings.Join(m.marketLines(110), "\n")
	if !strings.Contains(view, "COMPS") || !strings.Contains(view, "PRICE DISPERSION") {
		t.Fatalf("comps section missing:\n%s", view)
	}
	if a, b := strings.Index(view, "ARBITRAGE"), strings.Index(view, "COMPS"); a > b {
		t.Errorf("COMPS renders before the Kind sections")
	}

	if got := m.marketTotalRows(); got != 2 {
		t.Fatalf("total rows = %d, want the opportunity and the comp", got)
	}
	m.cursor[paneCards] = m.marketSections()[compsSection].curStart
	if c := m.selectedComp(); c == nil || c.Card.Name != "Sheeted" {
		t.Fatalf("selectedComp = %+v", c)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if next.(Model).detail == nil {
		t.Fatal("enter on a comps row must open the detail")
	}
	m = key(m, "w")
	if m.prompt == nil || !strings.Contains(m.prompt.label, "Sheeted") ||
		!strings.Contains(m.prompt.label, "$55.00") {
		t.Fatalf("w on a comps row opened %+v, want the market-anchored prompt", m.prompt)
	}
}

func TestCompsSortIsIndependent(t *testing.T) {
	agree := comp("Agree", 90, 100, 20)
	agree.CK, agree.HasCK = 101, true
	differ := comp("Differ", 50, 50, 40)
	differ.CK, differ.HasCK = 100, true
	lone := comp("Lone", 70, 30, 35)

	res := market.Result{
		Opportunities: []market.Opportunity{opp("Profitable", 2, 20)},
		Comps:         []market.Comp{agree, differ, lone},
		Compared:      4,
	}
	m := marketModel(t, func(context.Context, progress.Fn, float64) (market.Result, error) { return res, nil })
	for m.view != viewMarket {
		m = key(m, "v")
	}
	m = key(m, "F")
	next, _ := m.Update(marketMsg{gen: m.marketGen, res: res})
	m = next.(Model)
	m.focus = paneCards

	names := func() []string {
		out := make([]string, len(m.marketComps))
		for i, c := range m.marketComps {
			out[i] = c.Card.Name
		}
		return out
	}

	m.cursor[paneCards] = m.marketSections()[compsSection].curStart
	if got := m.sortLabel(); got != "comps · price dispersion" {
		t.Fatalf("default label = %q, want price dispersion", got)
	}
	if got := names(); got[0] != "Agree" || got[1] != "Differ" || got[2] != "Lone" {
		t.Fatalf("sell spread order = %v, want agreement first, undefined last", got)
	}
	m = key(m, "s")
	if got := m.sortLabel(); got != "comps · name" {
		t.Fatalf("label = %q", got)
	}

	m = key(m, "b")
	if got := m.sortLabel(); got != "comps · spread" {
		t.Fatalf("side flip should reset the sort, label = %q", got)
	}
	if got := names(); got[0] != "Lone" || got[1] != "Differ" || got[2] != "Agree" {
		t.Errorf("buy spread order = %v, want the bid-over-market row tightest", got)
	}
	if m.marketSortIdx != [3]int{} {
		t.Errorf("kind tables' sort state moved: %v", m.marketSortIdx)
	}
}

func TestCompsRespectValueFloor(t *testing.T) {
	res := market.Result{
		Comps:    []market.Comp{comp("Rich", 60, 55, 44), comp("Penny", 2, 1.5, 1)},
		Compared: 2,
	}
	m := marketModel(t, func(context.Context, progress.Fn, float64) (market.Result, error) { return res, nil })
	for m.view != viewMarket {
		m = key(m, "v")
	}
	m = key(m, "F")
	next, _ := m.Update(marketMsg{gen: m.marketGen, res: res})
	m = next.(Model)

	if len(m.marketComps) != 2 {
		t.Fatalf("comps = %d before the floor", len(m.marketComps))
	}
	m = key(m, "M")
	if len(m.marketComps) != 1 || m.marketComps[0].Card.Name != "Rich" {
		t.Errorf("floored comps = %+v, want only the rich row", m.marketComps)
	}
}

func TestQuitConfirmKeepsFrameHeight(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	before := m.visibleRows()
	if len(ui.WrapHelp(m.helpLine(), m.width)) < 2 {
		t.Skip("fixture help no longer wraps at this width; widen the line or narrow the frame")
	}
	m = key(m, "q")
	if m.confirm == nil {
		t.Fatal("q did not stage the quit confirm")
	}
	if after := m.visibleRows(); after != before {
		t.Errorf("visible rows moved %d → %d under the confirm; the frame must hold still", before, after)
	}
}

func TestMoversGradientStyles(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	st := testStore()
	st.movers = []store.PriceChange{
		{ScryfallID: "Riser-id", Name: "Riser", SetCode: "uma", CollectorNumber: "1",
			Finish: finish.Nonfoil, Copies: 2, Old: 1, New: 5},
		{ScryfallID: "Sinker-id", Name: "Sinker", SetCode: "uma", CollectorNumber: "2",
			Finish: finish.Nonfoil, Copies: 1, Old: 50, New: 10},
	}
	m := atAllCards(t, newTestModel(t, st))
	m = key(m, "v")
	if len(m.movers) != 2 {
		t.Fatalf("movers = %d, want the fixture pair", len(m.movers))
	}

	e := ui.Env{Color: true}
	out := strings.Join(m.moversLines(120), "\n")

	if want := e.Diverge(-1)(ui.SignedMoney(-40)); !strings.Contains(out, want) {
		t.Errorf("sinker IMPACT not at the loss endpoint:\n%q", out)
	}
	if want := e.Diverge(ui.DivergeFrac(-0.8, 4))(ui.SignedPercent(-0.8)); !strings.Contains(out, want) {
		t.Errorf("sinker CHANGE not mid-ramp:\n%q", out)
	}

	changeStyle := e.Diverge(ui.DivergeFrac(4, 4))("x")
	impactStyle := e.Diverge(ui.DivergeFrac(8, 40))("x")
	if changeStyle == impactStyle {
		t.Error("CHANGE and IMPACT must scale independently")
	}
}

func TestHeaderAnchorIgnoresCursorBar(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(prev)

	m := newTestModel(t, testStore())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 20})
	m = next.(Model)
	_, right := m.paneWidths()
	unfocused := maxLineWidth(m.rightLines(right))
	m = key(m, "tab")
	if focused := maxLineWidth(m.rightLines(right)); focused != unfocused {
		t.Fatalf("anchor = %d focused vs %d unfocused; the bar's padding leaked into the measure",
			focused, unfocused)
	}
}

func (f *fakeStore) setDeckEntryQuantity(ref store.EntryRef, qty int) (int, error) {
	entries := f.deckCards[ref.ContainerID]
	var previous int
	kept := entries[:0:0]
	for _, e := range entries {
		if e.Card.ScryfallID == ref.ScryfallID && e.Finish == ref.Finish &&
			e.Board == ref.Board {
			previous = e.Quantity
			if qty == 0 {
				continue
			}
			e.Quantity = qty
		}
		kept = append(kept, e)
	}
	f.deckCards[ref.ContainerID] = kept
	for i := range f.decks {
		if f.decks[i].ID != ref.ContainerID {
			continue
		}
		total := 0
		for _, e := range kept {
			total += e.Quantity
		}
		f.decks[i].TotalCopies = total
	}
	return previous, nil
}
