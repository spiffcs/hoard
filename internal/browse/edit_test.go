package browse

import (
	"context"
	"encoding/json"
	"image"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func TestParseCondition(t *testing.T) {
	for in, want := range map[string]string{

		"nm": "nm", "lp": "lp", "mp": "mp", "hp": "hp", "dmg": "dmg",

		"Near Mint":         "nm",
		"Lightly Played":    "lp",
		"Slightly Played":   "lp",
		"Excellent":         "lp",
		"Moderately Played": "mp",
		"Played":            "mp",
		"Heavily Played":    "hp",
		"Damaged":           "dmg",
		"Poor":              "dmg",

		"-": "unknown", "": "unknown", "unknown": "unknown", "?": "unknown",
	} {
		got, err := parseCondition(in)
		if err != nil {
			t.Errorf("parseCondition(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseCondition(%q) = %q, want %q", in, got, want)
		}
	}

	for _, bad := range []string{"PSA 10", "BGS 9.5", "pristine", "gem mint"} {
		if _, err := parseCondition(bad); err == nil {
			t.Errorf("parseCondition(%q) = nil error, want a refusal", bad)
		}
	}
}

func TestConditionInputPrefill(t *testing.T) {
	if got := conditionInput("unknown"); got != "" {
		t.Errorf("conditionInput(unknown) = %q, want empty", got)
	}
	if got := conditionInput(""); got != "" {
		t.Errorf("conditionInput(zero) = %q, want empty", got)
	}
	if got := conditionInput("lp"); got != "lp" {
		t.Errorf("conditionInput(lp) = %q, want lp", got)
	}
}

func TestDetailHeldConditionEdit(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerID: 1, ContainerName: "Binder", ContainerKind: store.KindCollection,
				Finish: finish.Nonfoil, Condition: store.ConditionUnknown, Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
		},
	}
	m := newTestModel(t, st)
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("detail did not open")
	}
	m = key(m, "up")
	if m.detail.zone != zoneHeld {
		t.Fatalf("zone = %d, want the held zone", m.detail.zone)
	}

	for range 3 {
		m = key(m, "right")
	}
	if m.detail.heldField != fieldCondition {
		t.Fatalf("field = %d, want condition", m.detail.heldField)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.prompt == nil {
		t.Fatal("enter on the condition field opened no prompt")
	}

	if m.prompt.text != "" {
		t.Errorf("prompt text = %q, want empty for an unassessed row", m.prompt.text)
	}
	if !strings.Contains(m.prompt.label, "condition") {
		t.Errorf("prompt label = %q, want it to name the field", m.prompt.label)
	}

	m.prompt.text = "Lightly Played"
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if got := st.movedCondition; got != "unknown→lp" {
		t.Errorf("store saw %q, want the row's own condition on both sides", got)
	}
	if m.undoStack == nil {
		t.Error("a condition change recorded no undo")
	}
}

func TestEmptyCollectionHeaderNamesThePane(t *testing.T) {
	st := &fakeStore{
		binders:    map[int64]string{},
		binderRows: map[int64][]store.CollectionRow{},
	}
	m, err := New(st, WithEnv(ui.Env{Color: true}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = next.(Model)

	header := strings.Split(m.View(), "\n")[0]
	if !strings.Contains(header, "CARDS · ALL CARDS") {
		t.Errorf("header = %q, want the pane named in full", header)
	}
	if strings.Contains(header, "…") {
		t.Errorf("header truncated the title away: %q", header)
	}

	if !strings.Contains(header, "0 · $0.00") {
		t.Errorf("header = %q, want the totals kept", header)
	}
}

func TestHeaderTotalsStillHugAWideTable(t *testing.T) {
	m := newTestModel(t, testStore())
	header := strings.Split(m.View(), "\n")[0]
	_, right := m.paneWidths()
	if got := lipgloss.Width(strings.TrimRight(ansi.Strip(header), " ")); got >= right+containerPaneWidth+paneGap {
		t.Errorf("header spans %d columns, want the totals short of the pane's far edge:\n%q",
			got, header)
	}
}

func TestHeldEditCarriesTheCompsRefetch(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerID: 1, ContainerName: "Binder", ContainerKind: store.KindCollection,
				Finish: finish.Nonfoil, Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
		},
	}
	m := newTestModel(t, st)
	m.cardComps = func(id string) (map[finish.Finish]market.Comp, bool) {
		return map[finish.Finish]market.Comp{finish.Nonfoil: {}}, true
	}
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil || !m.detail.compsPending {
		t.Fatal("setup: want an open detail with its comp sheet pending")
	}
	id := m.detail.card.ScryfallID

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	m = next.(Model)
	if !m.detail.compsPending {
		t.Fatal("setup drift: the edit's reload should re-mark the sheet pending")
	}
	if cmd == nil {
		t.Fatal("the held edit dropped reloadDetail's command — the sheet pends forever")
	}
	msg, ok := cmd().(detailCompsMsg)
	if !ok || msg.scryfallID != id {
		t.Fatalf("edit command yielded %+v, want this printing's comp read", msg)
	}
	next, _ = m.Update(msg)
	m = next.(Model)
	if m.detail.compsPending || !m.detail.compsOK {
		t.Errorf("pending %v ok %v after the read landed, want the sheet answered",
			m.detail.compsPending, m.detail.compsOK)
	}
}

func TestHeldSetChangeShowsTheNewPrintingsImage(t *testing.T) {
	const oldID, newID = "Wasteland-id", "wasteland-tmp-id"

	st := testStore()
	st.collection = []store.CollectionRow{
		row("Wasteland", "exp", "1", finish.Nonfoil, 1, 100),
	}
	st.holdingsByName = map[string][]store.Holding{
		"Wasteland": {
			{ContainerID: defaultBinderID, ContainerName: "Binder", ContainerKind: store.KindCollection,
				Finish: finish.Nonfoil, Condition: store.ConditionUnknown, Quantity: 1,
				ScryfallID: oldID, SetCode: "exp", CollectorNumber: "1"},
		},
	}
	st.undocumented = map[string]bool{newID: true}

	m := newTestModel(t, st)
	m.ctx = context.Background()
	m.imgTier = ui.ImageHalfblock

	var asked []string
	m.imageFetch = func(_ context.Context, _, url string) (image.Image, error) {
		asked = append(asked, url)
		return image.NewRGBA(image.Rect(0, 0, 2, 4)), nil
	}
	m.cardDocument = func(_ context.Context, id string) (scryfall.Card, error) {
		return scryfall.Card{
			ID: id, Name: "Wasteland", Set: "tmp", CollectorNumber: "330",
			Raw: json.RawMessage(`{"image_uris":{"normal":"http://img.test/` + id + `"}}`),
		}, nil
	}
	m.printSearch = func(context.Context, string) ([]scryfall.Card, error) {
		return []scryfall.Card{{
			ID: newID, Name: "Wasteland", Set: "tmp", CollectorNumber: "330",
			ScryfallURL: "https://scryfall.com/card/tmp/330/wasteland",
		}}, nil
	}

	m = key(m, "tab")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = pump(t, next.(Model), cmd)
	if m.detail == nil {
		t.Fatal("setup: detail did not open")
	}
	if len(m.detail.image) == 0 {
		t.Fatal("setup: the original printing showed no image")
	}

	m = key(m, "up")
	m = key(m, "right")
	if m.detail.heldField != fieldSet {
		t.Fatalf("setup: field = %d, want the set field", m.detail.heldField)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.prompt == nil {
		t.Fatal("setup: enter on the set field opened no prompt")
	}
	m.prompt.text = "tmp"
	asked = nil

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = pump(t, next.(Model), cmd)

	if m.detail == nil {
		t.Fatal("the detail closed on a set change")
	}
	if m.detail.card.ScryfallID != newID {
		t.Fatalf("detail is pinned to %q, want the new printing %q",
			m.detail.card.ScryfallID, newID)
	}
	if len(m.detail.image) == 0 {
		t.Error("the card image vanished after the set change")
	}
	want := "http://img.test/" + newID
	if len(asked) == 0 || asked[len(asked)-1] != want {
		t.Errorf("image fetched from %v, want the new printing's art at %q", asked, want)
	}
}

func TestShallowHistoryIsCaptionedHonestly(t *testing.T) {
	m := newTestModel(t, testStore())

	fresh := detail{
		card: store.CardDetail{
			Card:     store.Card{Name: "Wasteland", SetCode: "tmp", CollectorNumber: "330"},
			TypeLine: "Land", Rarity: "rare", Enriched: true,
		},
		series: map[finish.Finish][]store.PricePoint{
			finish.Nonfoil: {{AsOf: "2026-08-25T03:53:26Z", Price: 49.78, Source: "scryfall"}},
		},
	}
	got := strings.Join(m.detailLines(fresh, 80), "\n")

	if strings.Contains(got, "1 checks") {
		t.Errorf("a single observation renders as %q:\n%s", "1 checks", got)
	}
	if strings.Contains(got, "$49.78–$49.78") {
		t.Errorf("a single observation renders a range against itself:\n%s", got)
	}
	if !strings.Contains(got, "backfill") {
		t.Errorf("a card with no history does not say how to get some:\n%s", got)
	}

	deep := fresh
	deep.series = map[finish.Finish][]store.PricePoint{finish.Nonfoil: {
		{AsOf: "2026-08-21T00:00:00Z", Price: 48.00},
		{AsOf: "2026-08-22T00:00:00Z", Price: 48.50},
		{AsOf: "2026-08-23T00:00:00Z", Price: 49.10},
		{AsOf: "2026-08-24T00:00:00Z", Price: 49.78},
	}}
	if got := strings.Join(m.detailLines(deep, 80), "\n"); strings.Contains(got, "backfill") {
		t.Errorf("a card that already has history is nagged about backfill:\n%s", got)
	}
}

func TestHeldSetChangeBackfillsTheNewPrintingsHistory(t *testing.T) {
	const oldID, newID = "Wasteland-id", "wasteland-tmp-id"

	st := testStore()
	st.collection = []store.CollectionRow{
		row("Wasteland", "exp", "1", finish.Nonfoil, 1, 100),
	}
	st.holdingsByName = map[string][]store.Holding{
		"Wasteland": {
			{ContainerID: defaultBinderID, ContainerName: "Binder", ContainerKind: store.KindCollection,
				Finish: finish.Nonfoil, Condition: store.ConditionUnknown, Quantity: 1,
				ScryfallID: oldID, SetCode: "exp", CollectorNumber: "1"},
		},
	}
	st.priceSeries = map[string][]store.PricePoint{
		oldID + "|nonfoil": {
			{AsOf: "2026-08-23T00:00:00Z", Price: 28.00},
			{AsOf: "2026-08-24T00:00:00Z", Price: 28.78},
		},
	}

	m := newTestModel(t, st)
	m.ctx = context.Background()

	var asked [][2]string
	m.historyBackfill = func(_ context.Context, id, set string) (int, error) {
		asked = append(asked, [2]string{id, set})
		st.priceSeries[id+"|nonfoil"] = []store.PricePoint{
			{AsOf: "2026-08-22T00:00:00Z", Price: 49.00},
			{AsOf: "2026-08-23T00:00:00Z", Price: 49.40},
			{AsOf: "2026-08-24T00:00:00Z", Price: 49.78},
		}
		return 3, nil
	}
	m.printSearch = func(context.Context, string) ([]scryfall.Card, error) {
		return []scryfall.Card{{ID: newID, Name: "Wasteland", Set: "tmp",
			CollectorNumber: "330", ScryfallURL: "http://x"}}, nil
	}

	m = key(m, "tab")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = pump(t, next.(Model), cmd)
	if m.detail == nil {
		t.Fatal("setup: detail did not open")
	}
	m = key(m, "up")
	m = key(m, "right")
	if m.detail.heldField != fieldSet {
		t.Fatalf("setup: field = %d, want the set field", m.detail.heldField)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.prompt == nil {
		t.Fatal("setup: no prompt on the set field")
	}
	m.prompt.text = "tmp"

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = pump(t, next.(Model), cmd)

	if len(asked) != 1 || asked[0] != [2]string{newID, "tmp"} {
		t.Fatalf("backfill asked %v, want one call for the new printing in tmp", asked)
	}
	if got := m.detail.series[finish.Nonfoil]; len(got) != 3 {
		t.Errorf("detail shows %d price points after the set change, want the backfilled 3", len(got))
	}
}
