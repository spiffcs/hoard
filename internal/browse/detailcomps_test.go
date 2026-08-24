package browse

import (
	"context"
	"errors"
	"fmt"
	"image"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func pp(asOf string, price float64) store.PricePoint {
	return store.PricePoint{AsOf: asOf, Price: price, Source: "test"}
}

func TestDetailOpenLoadsBidSeries(t *testing.T) {
	st := testStore()
	st.bidSeries = map[string][]store.PricePoint{
		"Bitterblossom-id|nonfoil": {pp("2026-07-01T00:00:00Z", 20), pp("2026-07-20T00:00:00Z", 24)},
	}
	m := newTestModel(t, st)
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("enter did not open the detail")
	}
	if got := m.detail.bids[finish.Nonfoil]; len(got) != 2 {
		t.Fatalf("bids = %+v, want the seeded series", m.detail.bids)
	}
	out := strings.Join(m.hoardLines(*m.detail, 100), "\n")
	if !strings.Contains(out, "buylist") || !strings.Contains(out, "$24.00") {
		t.Errorf("bid row missing:\n%s", out)
	}
}

func TestDetailBidAndSpreadRows(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	d := detail{
		series: map[finish.Finish][]store.PricePoint{
			finish.Nonfoil: {pp("2026-06-01T00:00:00Z", 10), pp("2026-07-01T00:00:00Z", 10)},
		},
		bids: map[finish.Finish][]store.PricePoint{
			finish.Nonfoil: {pp("2026-06-01T00:00:00Z", 5), pp("2026-07-01T00:00:00Z", 8)},
		},
	}
	out := strings.Join(m.hoardLines(d, 120), "\n")
	if !strings.Contains(out, "buylist") || !strings.Contains(out, "$8.00") {
		t.Fatalf("bid row missing:\n%s", out)
	}

	if !strings.Contains(out, "spread") || !strings.Contains(out, "50.0% → 20.0%") ||
		!strings.Contains(out, "tightening") {
		t.Errorf("spread trend missing or wrong:\n%s", out)
	}

	d.bids[finish.Nonfoil] = []store.PricePoint{pp("2026-01-01T00:00:00Z", 5), pp("2026-02-01T00:00:00Z", 6)}
	out = strings.Join(m.hoardLines(d, 120), "\n")
	if strings.Contains(out, "spread") {
		t.Errorf("spread row rendered without overlapping windows:\n%s", out)
	}
	if !strings.Contains(out, "buylist") {
		t.Errorf("the buylist row should survive alone:\n%s", out)
	}
}

func TestDetailBidRowWithoutRetail(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	d := detail{
		series: map[finish.Finish][]store.PricePoint{},
		bids: map[finish.Finish][]store.PricePoint{
			finish.Foil: {pp("2026-07-01T00:00:00Z", 3)},
		},
	}
	out := strings.Join(m.hoardLines(d, 120), "\n")
	if !strings.Contains(out, "buylist") || !strings.Contains(out, "$3.00") {
		t.Errorf("orphan bid row missing:\n%s", out)
	}
}

func TestDetailCompsSection(t *testing.T) {
	sheet := market.Comp{
		Market: 10, HasMarket: true,
		Manapool: 11, HasManapool: true,
		CK: 12, HasCK: true,
		Buylist: 7, BuylistTo: "cardkingdom", HasBuylist: true,
		Low: 10, LowFrom: "tcgplayer",
	}
	m := atAllCards(t, newTestModel(t, testStore()))

	d := detail{comps: map[finish.Finish]market.Comp{finish.Nonfoil: sheet}, compsOK: true}
	if out := strings.Join(m.hoardLines(d, 140), "\n"); strings.Contains(out, "COMPS") {
		t.Fatalf("COMPS rendered without the capability:\n%s", out)
	}

	m.cardComps = func(string) (map[finish.Finish]market.Comp, bool) { return nil, false }

	d = detail{compsOK: false}
	out := strings.Join(m.hoardLines(d, 140), "\n")
	if !strings.Contains(out, "COMPS") || !strings.Contains(out, "press F on the MARKET view") {
		t.Fatalf("absent-cache note missing:\n%s", out)
	}

	d = detail{
		comps: map[finish.Finish]market.Comp{finish.Nonfoil: sheet}, compsOK: true,
		holdings: []store.Holding{{ContainerName: "Binder", Finish: finish.Nonfoil, Quantity: 1}},
	}
	out = strings.Join(m.hoardLines(d, 140), "\n")
	for _, want := range []string{
		"TCG SOLD", "MP", "CK", "CK PAYS", "SPREAD",
		"$10.00", "$11.00", "$12.00", "$7.00", "30.0%",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("comps table missing %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "BUYLIST NEAR MARKET") {
		t.Errorf("liquid verdict should not render:\n%s", out)
	}

	if strings.Index(out, "PRICE") > strings.Index(out, "COMPS") {
		t.Errorf("COMPS rendered before PRICE:\n%s", out)
	}

	arb := sheet
	arb.Buylist = 10.50
	d.comps = map[finish.Finish]market.Comp{finish.Nonfoil: arb}
	out = strings.Join(m.hoardLines(d, 140), "\n")
	if strings.Contains(out, "ARBITRAGE") || strings.Contains(out, "ck pays") {
		t.Errorf("the comps verdict prose must never render:\n%s", out)
	}
}

func TestDetailPriceGroupsSeparate(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	d := detail{
		series: map[finish.Finish][]store.PricePoint{
			finish.Nonfoil: {pp("2026-06-01T00:00:00Z", 10), pp("2026-07-01T00:00:00Z", 12)},
			finish.Foil:    {pp("2026-06-01T00:00:00Z", 20), pp("2026-07-01T00:00:00Z", 24)},
		},
		bids: map[finish.Finish][]store.PricePoint{
			finish.Nonfoil: {pp("2026-06-01T00:00:00Z", 5), pp("2026-07-01T00:00:00Z", 6)},
		},
	}
	lines := m.hoardLines(d, 120)
	spreadAt, foilAt := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "spread") && spreadAt == -1 {
			spreadAt = i
		}
		if strings.Contains(l, "foil") && !strings.Contains(l, "non-foil") && foilAt == -1 {
			foilAt = i
		}
	}
	if spreadAt == -1 || foilAt == -1 || foilAt < spreadAt {
		t.Fatalf("unexpected order (spread %d, foil %d):\n%s", spreadAt, foilAt, strings.Join(lines, "\n"))
	}
	blank := false
	for _, l := range lines[spreadAt:foilAt] {
		if strings.TrimSpace(l) == "" {
			blank = true
		}
	}
	if !blank {
		t.Errorf("no blank line between the finish groups:\n%s", strings.Join(lines, "\n"))
	}
}

func TestDetailLinksOpenInBrowser(t *testing.T) {
	var opened []string
	st := testStore()
	m := newTestModel(t, st)
	m.openURL = func(u string) error { opened = append(opened, u); return nil }
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("enter did not open the detail")
	}
	if len(m.detail.links) == 0 {
		t.Fatalf("no links built: %+v", m.detail)
	}
	out := strings.Join(m.hoardLines(*m.detail, 120), "\n")
	if !strings.Contains(out, "LINKS") || !strings.Contains(out, "tcgplayer.com") ||
		!strings.Contains(out, "manapool.com") || !strings.Contains(out, "cardkingdom.com") {
		t.Fatalf("links line missing:\n%s", out)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("enter with links must open the page, not close the overlay")
	}
	if len(opened) != 1 || !strings.Contains(opened[0], "tcgplayer.com/product/12345") {
		t.Fatalf("opened = %v, want tcgplayer's exact product", opened)
	}

	m = key(m, "right")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if len(opened) != 2 || !strings.Contains(opened[1], "manapool.com/card/uma/85/bitterblossom") {
		t.Fatalf("opened = %v, want manapool's exact printing", opened)
	}

	for range 10 {
		m = key(m, "right")
	}
	if m.detail.linkCursor != len(m.detail.links)-1 {
		t.Errorf("cursor = %d, want clamped at the last link", m.detail.linkCursor)
	}
	for range 10 {
		m = key(m, "left")
	}
	if m.detail.linkCursor != 0 {
		t.Errorf("cursor = %d, want clamped at the first link", m.detail.linkCursor)
	}

	m = key(m, "esc")
	if m.detail != nil {
		t.Error("esc must still close the overlay")
	}
}

func TestNameSlug(t *testing.T) {
	cases := map[string]string{
		"Sol Ring":                  "sol-ring",
		"Ulamog, the Infinite Gyre": "ulamog-the-infinite-gyre",
		"Borrowing 100,000 Arrows":  "borrowing-100-000-arrows",
		"Lim-Dûl's Vault":           "lim-d-l-s-vault",
	}
	for in, want := range cases {
		if got := nameSlug(in); got != want {
			t.Errorf("nameSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAllCardsMergesByName(t *testing.T) {
	st := testStore()

	st.deckCards[201] = append(st.deckCards[201], entry("Sol Ring", "main", finish.Nonfoil, 2, 15))
	st.deckCards[201][len(st.deckCards[201])-1].Card.ScryfallID = "Sol Ring-alt-id"
	st.deckCards[201][len(st.deckCards[201])-1].Card.SetCode = "lea"
	m := atAllCards(t, newTestModel(t, st))

	var merged *card
	for i := range m.cards {
		if m.cards[i].Name == "Sol Ring" && m.cards[i].Finish == finish.Nonfoil {
			if merged != nil {
				t.Fatalf("Sol Ring nonfoil appears twice:\n%+v", m.cards)
			}
			merged = &m.cards[i]
		}
	}
	if merged == nil {
		t.Fatal("no merged Sol Ring row")
	}
	if merged.Quantity != 5 {
		t.Errorf("quantity = %d, want the 3 binder + 2 deck copies", merged.Quantity)
	}
	if merged.SetCode != "" || merged.CollectorNumber != "" {
		t.Errorf("printing = %q/%q, want blanked across sets", merged.SetCode, merged.CollectorNumber)
	}
	if merged.Value != 60 {
		t.Errorf("value = %v, want the binder 30 plus the deck 30", merged.Value)
	}
}

func TestDetailHeldCursorSwitchesPrinting(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerName: "Binder", Finish: finish.Nonfoil, Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
			{ContainerName: "Rich Deck", Finish: finish.Nonfoil, Quantity: 1, Board: "main",
				ScryfallID: "Bitterblossom-mor-id", SetCode: "mor", CollectorNumber: "62"},
		},
	}
	st.bidSeries = map[string][]store.PricePoint{
		"Bitterblossom-mor-id|nonfoil": {pp("2026-07-01T00:00:00Z", 9)},
	}
	m := newTestModel(t, st)
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil || len(m.detail.holdings) != 2 {
		t.Fatalf("detail holdings = %+v, want both printings", m.detail)
	}
	if m.detail.heldCursor != 0 || m.detail.card.ScryfallID != "Bitterblossom-id" {
		t.Fatalf("opened at cursor %d on %s", m.detail.heldCursor, m.detail.card.ScryfallID)
	}

	m = key(m, "up")
	if m.detail.zone != zoneHeld {
		t.Fatalf("zone = %d, want the held list after up", m.detail.zone)
	}
	m = key(m, "down")
	if m.detail.heldCursor != 1 {
		t.Fatalf("cursor = %d, want the deck's printing", m.detail.heldCursor)
	}
	if m.detail.card.ScryfallID != "Bitterblossom-mor-id" {
		t.Errorf("card = %s, want the overlay re-pointed", m.detail.card.ScryfallID)
	}
	if len(m.detail.bids[finish.Nonfoil]) != 1 {
		t.Errorf("bids = %+v, want the other printing's series loaded", m.detail.bids)
	}
	out := strings.Join(m.hoardLines(*m.detail, 120), "\n")
	if !strings.Contains(out, "mor/62") || !strings.Contains(out, "uma/85") {
		t.Errorf("held rows should name their printings:\n%s", out)
	}

	m = key(m, "up")
	if m.detail.card.ScryfallID != "Bitterblossom-id" {
		t.Errorf("card = %s, want the original back", m.detail.card.ScryfallID)
	}
}

func TestCompsNegativeSpreadRenders(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	m.cardComps = func(string) (map[finish.Finish]market.Comp, bool) { return nil, true }
	d := detail{
		comps: map[finish.Finish]market.Comp{finish.Nonfoil: {
			Market: 10, HasMarket: true,
			Buylist: 10.50, BuylistTo: "cardkingdom", HasBuylist: true,
			Low: 10, LowFrom: "tcgplayer",
		}},
		compsOK: true,
	}
	out := strings.Join(m.hoardLines(d, 140), "\n")
	if !strings.Contains(out, "-5.0%") {
		t.Errorf("negative spread must render, not blank:\n%s", out)
	}
}

func TestDetailSwitchKeepsImageInPlace(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerName: "Binder", Finish: finish.Nonfoil, Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
			{ContainerName: "Rich Deck", Finish: finish.Nonfoil, Quantity: 1,
				ScryfallID: "Bitterblossom-mor-id", SetCode: "mor", CollectorNumber: "62"},
		},
	}
	m := newTestModel(t, st)
	m.imgTier = ui.ImageHalfblock
	m.imageFetch = func(context.Context, string, string) (image.Image, error) {
		return nil, errors.New("not reached synchronously")
	}
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}
	m.detail.image = []string{"OLD ART"}

	m = key(m, "up")
	m = key(m, "down")
	if m.detail.card.ScryfallID != "Bitterblossom-mor-id" {
		t.Fatalf("switch did not land: %s", m.detail.card.ScryfallID)
	}
	if len(m.detail.image) == 0 {
		t.Error("old art must hold the layout until the new art lands")
	}

	m.imageFetch = nil
	m = key(m, "up")
	if m.detail.image != nil {
		t.Error("stale art kept with no replacement coming")
	}
}

func TestCardLinksCKPerFinish(t *testing.T) {
	var c store.CardDetail
	c.Name = "Sol Ring"
	c.SetCode = "c21"
	c.CollectorNumber = "125"

	if got := cardLinks(c, false)[2].url; !strings.Contains(got, "catalog/search") {
		t.Errorf("unresolved card = %q, want the name search", got)
	}

	plain, foil := "https://mtgjson.com/links/aa", "https://mtgjson.com/links/bb"
	c.CKURL, c.CKFoilURL = plain, foil
	if got := cardLinks(c, false)[2].url; got != plain {
		t.Errorf("nonfoil link = %q, want the plain page", got)
	}
	if got := cardLinks(c, true)[2].url; got != foil {
		t.Errorf("foil link = %q, want the foil page", got)
	}
	c.CKFoilURL = ""
	if got := cardLinks(c, true)[2].url; got != plain {
		t.Errorf("foil holding with no foil page = %q, want the plain page", got)
	}
}

func TestHeldCursorRefreshesLinksPerFinish(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerName: "Binder", Finish: finish.Nonfoil, Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
			{ContainerName: "Rich Deck", Finish: finish.Foil, Quantity: 1,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
		},
	}
	m := newTestModel(t, st)
	m.openURL = func(string) error { return nil }
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil || len(m.detail.links) == 0 {
		t.Fatal("no links")
	}
	ckAt := func() string { return m.detail.links[2].url }
	if !strings.Contains(ckAt(), "links/plain") {
		t.Fatalf("nonfoil row should link the plain page, got %q", ckAt())
	}
	m = key(m, "up")
	m = key(m, "down")
	if m.detail.heldCursor != 1 {
		t.Fatalf("cursor = %d", m.detail.heldCursor)
	}
	if !strings.Contains(ckAt(), "links/foil") {
		t.Errorf("foil row should link the foil page, got %q", ckAt())
	}
}

func TestDetailReservesImageSpace(t *testing.T) {
	m := newTestModel(t, testStore())
	m.imgTier = ui.ImageHalfblock
	m.imageFetch = func(context.Context, string, string) (image.Image, error) {
		return nil, errors.New("not reached synchronously")
	}
	m = key(m, "tab")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil || cmd == nil || !m.detail.imagePending {
		t.Fatalf("detail open must mark the fetch pending (pending=%v cmd=%v)",
			m.detail != nil && m.detail.imagePending, cmd != nil)
	}

	next, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 45})
	m = next.(Model)
	blank := m.blankArt(m.detailImageCols())
	if len(blank) == 0 {
		t.Fatal("no space reserved")
	}

	heldAt := func() int {
		for i, l := range strings.Split(m.detailView(), "\n") {
			if strings.Contains(l, "HELD") {
				return i
			}
		}
		return -1
	}
	before := heldAt()
	if before < 0 {
		t.Fatalf("HELD not visible:\n%s", m.detailView())
	}

	lines := make([]string, len(blank))
	for i := range lines {
		lines[i] = "ART"
	}
	next, _ = m.Update(imageMsg{scryfallID: m.detail.card.ScryfallID, lines: lines})
	m = next.(Model)
	if after := heldAt(); after != before {
		t.Errorf("HELD moved %d → %d when the art landed", before, after)
	}

	next, _ = m.Update(imageMsg{scryfallID: m.detail.card.ScryfallID})
	m = next.(Model)
	if m.detail.imagePending || m.detail.image != nil {
		t.Errorf("failure must clear the reservation (pending=%v image=%v)",
			m.detail.imagePending, m.detail.image)
	}
}

func TestDetailShowsOpProgressAndReloads(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m.opUpdatePrices = func(ctx context.Context, p progress.Fn) (string, error) { return "done", nil }
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}

	m.openPalette()
	m.palette.Query = "update prices"
	m.refreshPalette()
	if len(m.palette.matches) == 0 {
		t.Fatal("no match for update prices over the detail")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.op == nil {
		t.Fatal("the command did not start an operation")
	}
	if !strings.Contains(m.detailView(), "updating prices") {
		t.Fatalf("overlay hides the running op:\n%s", m.detailView())
	}

	st.bidSeries = map[string][]store.PricePoint{
		"Bitterblossom-id|nonfoil": {pp("2026-07-01T00:00:00Z", 9)},
	}
	if cmd == nil {
		t.Fatal("no op command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if got := c(); got != nil {
				if _, isTick := got.(spinner.TickMsg); !isTick {
					msg = got
				}
			}
		}
	}
	next, _ = m.Update(msg)
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("detail closed by the op")
	}
	if !strings.Contains(m.detailView(), "done") {
		t.Errorf("overlay hides the op summary:\n%s", m.detailView())
	}
	if len(m.detail.bids[finish.Nonfoil]) != 1 {
		t.Errorf("detail did not reload after the op: bids = %+v", m.detail.bids)
	}
}

func TestPriceAndBuylistShareTheirWindow(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	d := detail{
		series: map[finish.Finish][]store.PricePoint{
			finish.Nonfoil: {pp("2026-04-29T00:00:00Z", 30), pp("2026-07-01T00:00:00Z", 33)},
		},
		bids: map[finish.Finish][]store.PricePoint{
			finish.Nonfoil: {pp("2026-05-01T00:00:00Z", 18), pp("2026-07-01T00:00:00Z", 20)},
		},
	}
	var sinces []string
	for _, l := range m.hoardLines(d, 130) {
		if _, after, ok := strings.Cut(l, "checks since "); ok {
			sinces = append(sinces, after)
		}
	}
	if len(sinces) != 2 || sinces[0] != sinces[1] {
		t.Errorf("captions = %v, want the retail and buylist rows sharing one window", sinces)
	}
}

func TestWrapHangsBulletContinuations(t *testing.T) {
	got := wrapHang("• Exile target nonland permanent, then return it to the battlefield tapped.", 40)
	if len(got) < 2 {
		t.Fatalf("expected a wrap, got %v", got)
	}
	if !strings.HasPrefix(got[0], "• Exile") {
		t.Errorf("first line = %q, want the bullet kept", got[0])
	}
	for _, l := range got[1:] {
		if !strings.HasPrefix(l, "  ") || strings.HasPrefix(l, "• ") {
			t.Errorf("continuation %q must hang under the text", l)
		}
	}

	if flat := wrapHang("Choose three. You may choose the same mode more than once.", 30); strings.HasPrefix(flat[1], " ") {
		t.Errorf("plain continuation %q must not indent", flat[1])
	}
}

func TestDetailHeldEditAndRemove(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerID: 1, ContainerName: "Binder", ContainerKind: store.KindCollection,
				Finish: finish.Nonfoil, Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
			{ContainerID: 202, ContainerName: "Rich Deck", ContainerKind: store.KindDeck,
				Finish: finish.Nonfoil, Quantity: 1, Board: "main",
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
		},
	}
	m := newTestModel(t, st)
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}
	if !strings.Contains(m.helpLine(), "+/- qty · d remove") {
		t.Errorf("help = %q, want the edit keys advertised on a binder row", m.helpLine())
	}

	binderQty := func() int {
		for _, r := range st.collection {
			if r.ScryfallID == "Bitterblossom-id" && r.Finish == finish.Nonfoil {
				return r.Quantity
			}
		}
		return 0
	}

	m = key(m, "+")
	if binderQty() != 5 {
		t.Errorf("binder quantity = %d after +, want 5", binderQty())
	}
	if !strings.Contains(m.status, "×5 in Binder") {
		t.Errorf("status = %q, want the receipt naming the container", m.status)
	}
	m = key(m, "-")
	if binderQty() != 4 {
		t.Errorf("binder quantity = %d after -, want 4", binderQty())
	}

	m = key(m, "d")
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "from Binder") {
		t.Fatalf("confirm = %+v, want the removal staged against the binder row", m.confirm)
	}
	m = key(m, "y")
	if binderQty() != 0 {
		t.Errorf("binder quantity = %d after removal, want gone", binderQty())
	}
	if !strings.Contains(m.status, "removed Bitterblossom (nonfoil) from Binder") {
		t.Errorf("status = %q, want the removal receipt", m.status)
	}
	if m.detail == nil {
		t.Fatal("removal must not close the overlay")
	}

	if m.undoStack == nil {
		t.Error("removal recorded no undo")
	}

	m = key(m, "down")
	m.status, m.statusErr = "", false
	m = key(m, "+")
	if m.statusErr {
		t.Errorf("+ on a deck row: status = %q, want the edit to go through", m.status)
	}
	if !strings.Contains(m.status, "×2 in Rich Deck") {
		t.Errorf("status = %q, want the receipt naming the deck", m.status)
	}
	if !strings.Contains(m.helpLine(), "+/- qty · d remove") {
		t.Errorf("help = %q, want the edit keys advertised on a deck row too", m.helpLine())
	}
	m = key(m, "d")
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "from Rich Deck") {
		t.Fatalf("confirm = %+v, want the removal staged against the deck row", m.confirm)
	}
}

func TestDetailClosesWhenTheLastCopyGoes(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerID: 1, ContainerName: "Binder", ContainerKind: store.KindCollection,
				Finish: finish.Nonfoil, Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
		},
	}
	m := newTestModel(t, st)
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}

	m = key(m, "d")
	if m.confirm == nil {
		t.Fatal("d staged no removal")
	}
	m = key(m, "y")
	if m.detail != nil {
		t.Error("the overlay outlived the card's last copy")
	}
	if !strings.Contains(m.status, "removed Bitterblossom") {
		t.Errorf("status = %q, want the removal receipt to survive the close", m.status)
	}

	st2 := testStore()
	st2.holdingsByName = st.holdingsByName
	m2 := newTestModel(t, st2)
	m2 = key(m2, "tab")
	next, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 = next.(Model)
	for range 4 {
		m2 = key(m2, "-")
	}
	if m2.detail != nil {
		t.Error("the overlay outlived a count edited down to nothing")
	}
}

func TestDetailHeldFieldEdit(t *testing.T) {
	st := testStore()
	st.binders = map[int64]string{7: "Trades"}
	st.binderRows = map[int64][]store.CollectionRow{7: {}}

	mor := row("Bitterblossom", "mor", "62", finish.Nonfoil, 4, 100)
	mor.ScryfallID = "Bitterblossom-mor-id"
	st.collection = append(st.collection, mor)
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerID: 1, ContainerName: "Binder", ContainerKind: store.KindCollection,
				Finish: finish.Nonfoil, Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
			{ContainerID: 1, ContainerName: "Binder", ContainerKind: store.KindCollection,
				Finish: finish.Nonfoil, Quantity: 4,
				ScryfallID: "Bitterblossom-mor-id", SetCode: "mor", CollectorNumber: "62"},
		},
	}
	m := newTestModel(t, st)
	m.printSearch = func(_ context.Context, name string) ([]scryfall.Card, error) {
		return []scryfall.Card{
			{ID: "Bitterblossom-id", Set: "uma", CollectorNumber: "85", Name: name},
			{ID: "Bitterblossom-mor-id", Set: "mor", CollectorNumber: "62", Name: name},
		}, nil
	}
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}

	m = key(m, "up")
	if m.detail.zone != zoneHeld || m.detail.heldField != fieldQty {
		t.Fatalf("zone/field = %d/%d, want held zone on quantity", m.detail.zone, m.detail.heldField)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.prompt == nil || m.prompt.text != "4" {
		t.Fatalf("prompt = %+v, want the quantity prefilled", m.prompt)
	}
	if err := m.prompt.validate("three"); err == nil {
		t.Error("validate accepted a non-number")
	}
	m.prompt.commit(&m, "7")
	m.prompt = nil
	qtyOf := func(sid string) int {
		for _, r := range st.collection {
			if r.ScryfallID == sid && r.Finish == finish.Nonfoil {
				return r.Quantity
			}
		}
		return 0
	}
	if qtyOf("Bitterblossom-id") != 7 {
		t.Errorf("quantity = %d, want 7", qtyOf("Bitterblossom-id"))
	}

	m = key(m, "right")
	if m.detail.heldField != fieldSet {
		t.Fatalf("field = %d, want set", m.detail.heldField)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.prompt == nil || m.prompt.text != "uma" {
		t.Fatalf("prompt = %+v, want the set prefilled", m.prompt)
	}
	m.prompt.commit(&m, "xyz")
	if !m.statusErr || !strings.Contains(m.status, "no Bitterblossom printing in XYZ") {
		t.Errorf("unknown set: status = %q err=%v", m.status, m.statusErr)
	}
	m.prompt = nil
	m.status, m.statusErr = "", false
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	m.prompt.commit(&m, "mor")
	m.prompt = nil
	if qtyOf("Bitterblossom-id") != 0 {
		t.Errorf("old printing still holds %d", qtyOf("Bitterblossom-id"))
	}
	if qtyOf("Bitterblossom-mor-id") != 11 {
		t.Errorf("mor printing = %d, want the moved 7 merged with the fixture 4", qtyOf("Bitterblossom-mor-id"))
	}
	if !strings.Contains(m.status, "now mor/62") {
		t.Errorf("status = %q, want the new printing named", m.status)
	}
	if len(st.upserted) == 0 || st.upserted[0].ID != "Bitterblossom-mor-id" {
		t.Errorf("upserted = %+v, want the picked printing stored", st.upserted)
	}
	if m.detail.card.ScryfallID != "Bitterblossom-mor-id" {
		t.Errorf("overlay = %s, want re-pointed at the corrected printing", m.detail.card.ScryfallID)
	}
	if m.undoStack == nil {
		t.Error("set change recorded no undo")
	}

	m = key(m, "right")
	if m.detail.heldField != fieldFinish {
		t.Fatalf("field = %d, want finish next", m.detail.heldField)
	}
	m = key(m, "right")
	if m.detail.heldField != fieldCondition {
		t.Fatalf("field = %d, want condition next", m.detail.heldField)
	}
	m = key(m, "right")
	if m.detail.heldField != fieldWhere {
		t.Fatalf("field = %d, want location", m.detail.heldField)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.prompt == nil || m.prompt.text != "Binder" {
		t.Fatalf("prompt = %+v, want the location prefilled", m.prompt)
	}
	m.prompt.commit(&m, "Nowhere")
	if !m.statusErr || !strings.Contains(m.status, `no binder named "Nowhere"`) {
		t.Errorf("unknown binder: status = %q err=%v", m.status, m.statusErr)
	}
	m.prompt = nil
	m.status, m.statusErr = "", false
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	m.prompt.commit(&m, "Trades")
	m.prompt = nil
	if qtyOf("Bitterblossom-mor-id") != 0 {
		t.Errorf("source binder still holds %d", qtyOf("Bitterblossom-mor-id"))
	}
	moved := false
	for _, r := range st.binderRows[7] {
		if r.ScryfallID == "Bitterblossom-mor-id" && r.Quantity == 11 {
			moved = true
		}
	}
	if !moved {
		t.Errorf("Trades rows = %+v, want the moved holding", st.binderRows[7])
	}
	if !strings.Contains(m.status, "to Trades") {
		t.Errorf("status = %q, want the move receipt", m.status)
	}
}

func TestDetailImagePinsToRightEdge(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 220, Height: 45})
	m = next.(Model)
	if got := m.detailImageCols(); got != artColsMax {
		t.Fatalf("detailImageCols = %d, want the %d cap on a wide tall window", got, artColsMax)
	}
	m.detail.image = []string{"ARTBLOCK"}
	for _, line := range strings.Split(m.View(), "\n") {
		if i := strings.Index(line, "ARTBLOCK"); i >= 0 {
			if want := 220 - artColsMax; i != want {
				t.Fatalf("art starts at col %d, want %d (right-edge pinned)", i, want)
			}
			return
		}
	}
	t.Fatal("art not rendered")
}

func TestDetailImageOverflowsBesideHeld(t *testing.T) {
	st := testStore()
	st.bidSeries = map[string][]store.PricePoint{
		"Bitterblossom-id|nonfoil": {pp("2026-05-01T00:00:00Z", 20), pp("2026-07-20T00:00:00Z", 24)},
	}
	m := newTestModel(t, st)
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 45})
	m = next.(Model)
	if !m.artOverflows() {
		t.Fatal("140 cols should engage the overflow layout")
	}
	rows := m.artRows(m.detailImageCols())
	art := make([]string, rows)
	for i := range art {
		art[i] = "ARTROW"
	}
	m.detail.image = art
	out := m.detailView()
	held := -1
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "HELD") {
			held = strings.Index(line, "ARTROW")
			break
		}
	}
	if held < 0 {
		t.Fatalf("the HELD line must carry an art row beside it:\n%s", out)
	}
	if !strings.Contains(out, "checks since") {
		t.Errorf("price captions must survive the narrowed column:\n%s", out)
	}
}

func TestDetailImageReservationMatchesOverflowCols(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 45})
	m = next.(Model)
	cols := m.detailImageCols()
	wantRows := m.artRows(cols)
	if got := len(m.blankArt(cols)); got != wantRows {
		t.Fatalf("reservation = %d rows, want %d", got, wantRows)
	}
	heldRow := func() int {
		m.detail.imagePending = len(m.detail.image) == 0
		for i, line := range strings.Split(m.detailView(), "\n") {
			if strings.Contains(line, "HELD") {
				return i
			}
		}
		return -1
	}
	m.detail.image = nil
	before := heldRow()
	art := make([]string, wantRows)
	for i := range art {
		art[i] = "ARTROW"
	}
	m.detail.image = art
	if after := heldRow(); before < 0 || before != after {
		t.Errorf("HELD moved when the art landed: row %d → %d", before, after)
	}
}

func TestDetailImageStacksBelowOverflowWidth(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 60})
	m = next.(Model)
	if m.artOverflows() {
		t.Fatal("110 cols must not use the overflow layout")
	}
	if want := m.detailImageCols(); want != artColsMax {
		t.Fatalf("detailImageCols = %d, want the full %d in the vertical layout", want, artColsMax)
	}
	m.detail.image = []string{"ARTBLOCK"}
	out := strings.Split(m.detailView(), "\n")
	artAt, heldAt := -1, -1
	for i, line := range out {
		if col := strings.Index(line, "ARTBLOCK"); col >= 0 {
			if col != 0 {
				t.Fatalf("vertical art starts at col %d, want the left margin", col)
			}
			artAt = i
		}
		if strings.Contains(line, "HELD") && heldAt == -1 {
			heldAt = i
		}
	}
	if artAt == -1 || heldAt == -1 {
		t.Fatalf("art %d, HELD %d — both must render", artAt, heldAt)
	}
	if artAt > heldAt {
		t.Errorf("art at %d renders after HELD at %d — it belongs between the details and HELD", artAt, heldAt)
	}
}

func TestDetailImageRerendersOnResize(t *testing.T) {
	m := newTestModel(t, testStore())
	m.imgTier = ui.ImageHalfblock
	fetches := 0
	m.imageFetch = func(context.Context, string, string) (image.Image, error) {
		fetches++
		return image.NewRGBA(image.Rect(0, 0, 8, 12)), nil
	}
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 220, Height: 45})
	m = next.(Model)

	land := func() {
		t.Helper()
		cmd := m.fetchDetailImage()
		if cmd == nil {
			t.Fatal("no fetch command")
		}
		msg, ok := cmd().(imageMsg)
		if !ok {
			t.Fatal("fetch did not yield an imageMsg")
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	land()
	if m.detail.imageColsDrawn != artColsMax {
		t.Fatalf("drawn cols = %d, want %d", m.detail.imageColsDrawn, artColsMax)
	}

	next, cmd := m.Update(tea.WindowSizeMsg{Width: 220, Height: 20})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("shrinking must trigger a re-render")
	}
	var msg imageMsg
	var ok bool
	switch v := cmd().(type) {
	case imageMsg:
		msg, ok = v, true
	case tea.BatchMsg:
		for _, c := range v {
			if im, isImg := c().(imageMsg); isImg {
				msg, ok = im, true
			}
		}
	}
	if !ok {
		t.Fatal("resize command did not yield an imageMsg")
	}
	if want := m.detailImageCols(); msg.cols != want {
		t.Fatalf("re-render at %d cols, want %d", msg.cols, want)
	}
	next, _ = m.Update(msg)
	m = next.(Model)
	if m.detail.imageColsDrawn != m.detailImageCols() {
		t.Errorf("drawn cols = %d after resize, want %d", m.detail.imageColsDrawn, m.detailImageCols())
	}

	stale := imageMsg{scryfallID: m.detail.card.ScryfallID, lines: []string{"x"}, cols: artColsMax}
	before := fetches
	next, cmd = m.Update(stale)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("a stale-size landing must re-render")
	}
	if _, ok := cmd().(imageMsg); !ok || fetches != before+1 {
		t.Errorf("stale landing: fetches %d → %d, want one corrective fetch", before, fetches)
	}
}

func TestDetailQuitKeyAsksFirst(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}
	m = key(m, "q")
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "quit hoard?") {
		t.Fatalf("confirm = %+v, want the quit question staged", m.confirm)
	}

	m = key(m, "n")
	if m.confirm != nil || m.detail == nil {
		t.Fatalf("after n: confirm=%v detail=%v, want the overlay back", m.confirm, m.detail)
	}

	m = key(m, "q")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("y must quit")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Fatalf("confirm ran %v, want tea.Quit", msg)
	}
}

func TestDetailStaleArtNeverOverflows(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}

	next, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 45})
	m = next.(Model)
	m.detail.image = []string{strings.Repeat("A", artColsMax)}
	m.detail.imageColsDrawn = artColsMax
	if want := m.detailImageCols(); want >= artColsMax {
		t.Fatalf("fixture broken: wanted cols %d should be under the stale %d", want, artColsMax)
	}

	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "AAA") {
			t.Fatalf("stale over-wide art rendered on a 30-col window:\n%s", line)
		}
	}
}

func TestDetailRetransmitAfterResize(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}
	m.detail.image = []string{"ART"}
	m.detail.imageColsDrawn = m.detailImageCols()
	m.detail.imageTransmit = "TRANSMIT-BYTES"
	m.detail.transmitSent = true

	next, cmd1 := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)
	gen1 := m.resizeGen
	if cmd1 == nil || m.detail.transmitSent {
		t.Fatal("a resize must dirty the upload and schedule a settle tick")
	}
	m.detail.imageColsDrawn = m.detailImageCols()
	for i := range 2 {
		if out := m.View(); !strings.Contains(out, "TRANSMIT-BYTES") {
			t.Fatalf("dirty frame %d must embed the transmit", i)
		}
	}

	next, _ = m.Update(tea.WindowSizeMsg{Width: 118, Height: 40})
	m = next.(Model)
	if m.resizeGen != gen1+1 {
		t.Fatalf("resizes must bump the generation: %d then %d", gen1, m.resizeGen)
	}
	next, _ = m.Update(retransmitMsg{gen: gen1})
	m = next.(Model)
	if m.detail.transmitSent {
		t.Fatal("a superseded tick declared delivery")
	}

	next, _ = m.Update(retransmitMsg{gen: m.resizeGen})
	m = next.(Model)
	if !m.detail.transmitSent {
		t.Fatal("the newest tick must declare delivery")
	}
	if out := m.View(); strings.Contains(out, "TRANSMIT-BYTES") {
		t.Fatal("a settled frame must not re-upload")
	}
}

func TestDetailScrolls(t *testing.T) {
	st := testStore()
	st.bidSeries = map[string][]store.PricePoint{
		"Bitterblossom-id|nonfoil": {pp("2026-05-01T00:00:00Z", 20), pp("2026-07-20T00:00:00Z", 24)},
	}
	m := newTestModel(t, st)
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}

	next, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "pgdn scrolls") {
		t.Fatalf("overflow must advertise scrolling:\n%s", out)
	}
	firstLine := strings.SplitN(out, "\n", 2)[0]

	m = key(m, "pgdown")
	out = m.View()
	if m.detail.scroll == 0 {
		t.Fatal("pgdn did not scroll")
	}
	if got := strings.SplitN(out, "\n", 2)[0]; got == firstLine {
		t.Error("scrolled view must start on a different line")
	}
	if !strings.Contains(out, "lines above") {
		t.Errorf("scrolled view must name the lines above:\n%s", out)
	}

	for range 20 {
		m = key(m, "pgdown")
	}
	m.View()
	if m.detail.scroll == 0 {
		t.Fatal("over-scroll clamped to the top")
	}
	for range 30 {
		m = key(m, "pgup")
	}
	out = m.View()
	if m.detail.scroll != 0 || strings.SplitN(out, "\n", 2)[0] != firstLine {
		t.Errorf("pgup must return to the top (scroll=%d)", m.detail.scroll)
	}

	m.detail.image = []string{"ART"}
	m.detail.imageTransmit = "TRANSMIT-BYTES"
	m.detail.transmitSent = false
	m = key(m, "pgdown")
	if out := m.View(); !strings.Contains(out, "TRANSMIT-BYTES") {
		t.Error("a scrolled dirty frame must still embed the transmit")
	}
}

func TestFoilTreatmentDisplays(t *testing.T) {
	st := testStore()
	ripple := row("Eldrazi Confluence", "m3c", "32", finish.Foil, 1, 75)
	ripple.Treatment = "ripple"
	st.collection = append(st.collection, ripple)
	st.holdingsByName = map[string][]store.Holding{
		"Eldrazi Confluence": {
			{ContainerID: 1, ContainerName: "Binder", ContainerKind: store.KindCollection,
				Finish: finish.Foil, Quantity: 1, Treatment: "ripple",
				ScryfallID: "Eldrazi Confluence-id", SetCode: "m3c", CollectorNumber: "32"},
		},
	}
	m := newTestModel(t, st)
	out := strings.Join(m.cardLines(120), "\n")
	if !strings.Contains(out, "ripple") {
		t.Errorf("holdings FINISH cell must name the treatment:\n%s", out)
	}

	d := detail{holdings: st.holdingsByName["Eldrazi Confluence"]}
	held := strings.Join(m.hoardLines(d, 120), "\n")
	if !strings.Contains(held, "ripple") {
		t.Errorf("HELD row must name the treatment:\n%s", held)
	}

	tcg := int64(553171)
	c := store.CardDetail{}
	c.Name, c.SetCode, c.CollectorNumber = "Eldrazi Confluence", "m3c", "32"
	c.TCGplayerID = &tcg
	c.Treatment = "ripple"
	links := cardLinks(c, true)
	if !strings.Contains(links[0].url, "search/magic/product?q=") ||
		!strings.Contains(links[0].url, "ripple") {
		t.Errorf("treated foil must link the TCG search, got %q", links[0].url)
	}
	if links = cardLinks(c, false); !strings.Contains(links[0].url, "/product/553171") {
		t.Errorf("the nonfoil side keeps the product link, got %q", links[0].url)
	}
	c.Treatment = ""
	if links = cardLinks(c, true); !strings.Contains(links[0].url, "/product/553171") {
		t.Errorf("an untreated foil keeps the product link, got %q", links[0].url)
	}
}

func TestDetailZoneArrowsAndTabOrder(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerName: "Binder", Finish: finish.Nonfoil, Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
			{ContainerName: "Trade", Finish: finish.Foil, Quantity: 1,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
		},
	}
	m := newTestModel(t, st)
	m.openURL = func(string) error { return nil }
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil || m.detail.zone != zoneLinks {
		t.Fatalf("setup: detail should open in the links zone")
	}

	for range len(m.detail.links) + 1 {
		if m.detail.zone == zoneHeld {
			break
		}
		m = key(m, "tab")
	}
	if m.detail.zone != zoneHeld || !m.detail.scrollHeldIntoView {
		t.Fatalf("tab should walk out of the links into the held zone and request a scroll: zone=%d scroll=%v",
			m.detail.zone, m.detail.scrollHeldIntoView)
	}

	was := m.detail.linkCursor
	m = key(m, "right")
	if m.detail.heldField != fieldSet || m.detail.linkCursor != was {
		t.Errorf("right in the held zone: field=%d link=%d, want the field cursor moved",
			m.detail.heldField, m.detail.linkCursor)
	}

	for range heldFieldCount + 1 {
		if m.detail.zone == zoneLinks {
			break
		}
		m = key(m, "tab")
	}
	if m.detail.zone != zoneLinks {
		t.Fatalf("tab should walk out of the fields to the links zone, got %d", m.detail.zone)
	}
	if m.detail.scroll == 0 {
		t.Errorf("tab to links should scroll toward them (clamped at render)")
	}

	atLinks := m.detail.linkCursor
	m = key(m, "right")
	if m.detail.linkCursor != atLinks+1 {
		t.Errorf("right in the links zone moved link %d→%d, want %d",
			atLinks, m.detail.linkCursor, atLinks+1)
	}
}

func TestDetailCompsLoadAsync(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	calls := 0
	m.cardComps = func(id string) (map[finish.Finish]market.Comp, bool) {
		calls++
		return map[finish.Finish]market.Comp{finish.Nonfoil: {}}, true
	}
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("setup: no detail opened")
	}
	id := m.detail.card.ScryfallID
	if !m.detail.compsPending {
		t.Fatal("opening must not read comps synchronously")
	}
	if out := strings.Join(m.compLines(*m.detail, 120), "\n"); !strings.Contains(out, "reading today's vendor quotes") {
		t.Errorf("pending comps should hold the section's place:\n%s", out)
	}

	cmd := m.fetchDetailComps(id)
	if cmd == nil {
		t.Fatal("no fetch command for an unanswered printing")
	}
	msg, ok := cmd().(detailCompsMsg)
	if !ok || msg.scryfallID != id || calls != 1 {
		t.Fatalf("fetch = %+v (calls %d), want this printing read once", msg, calls)
	}
	next, _ = m.Update(msg)
	m = next.(Model)
	if m.detail.compsPending || !m.detail.compsOK || len(m.detail.comps) != 1 {
		t.Fatalf("landed comps = pending %v ok %v %+v", m.detail.compsPending,
			m.detail.compsOK, m.detail.comps)
	}

	if m.fetchDetailComps(id) != nil {
		t.Error("an answered printing should not fetch again")
	}

	next, _ = m.Update(detailCompsMsg{scryfallID: "someone-else", ok: true})
	m = next.(Model)
	if m.detail.card.ScryfallID != id || !m.detail.compsOK {
		t.Errorf("a stale answer must not touch the open overlay")
	}

	m = key(m, "esc")
	if m.detail != nil || m.detailComps != nil {
		t.Errorf("esc should close the overlay and clear the comp memo")
	}
}

func TestDetailHeldFinishAlwaysRenders(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	d := detail{holdings: []store.Holding{
		{ContainerName: "Binder", Finish: finish.Nonfoil, Quantity: 40,
			ScryfallID: "mtn-a", SetCode: "mh3", CollectorNumber: "300"},
		{ContainerName: "Binder", Finish: finish.Foil, Treatment: "ripple", Quantity: 1,
			ScryfallID: "mtn-b", SetCode: "mh3", CollectorNumber: "301"},
	}}
	out := strings.Join(m.hoardLines(d, 140), "\n")

	if !strings.Contains(out, "mh3/300 · -      · — · Binder") {
		t.Errorf("plain nonfoil should render a padded dash in its finish slot:\n%s", out)
	}
	if !strings.Contains(out, "mh3/301 · ripple · — · Binder") {
		t.Errorf("treated foil should keep its treatment word:\n%s", out)
	}

	if !strings.Contains(out, "  ×40 ·") || !strings.Contains(out, "   ×1 ·") {
		t.Errorf("quantities should right-align:\n%s", out)
	}
}

func TestCompsVerdictGoneAndArbitrageEntrySuppressesSpread(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	m.cardComps = func(string) (map[finish.Finish]market.Comp, bool) { return nil, true }
	d := detail{
		holdings: []store.Holding{{ContainerName: "Binder", Finish: finish.Nonfoil, Quantity: 1}},
		comps: map[finish.Finish]market.Comp{finish.Nonfoil: {
			HasMarket: true, Market: 2.29, HasBuylist: true, Buylist: 3.50,
		}},
		compsOK: true,
		series: map[finish.Finish][]store.PricePoint{
			finish.Nonfoil: {pp("2026-06-01T00:00:00Z", 10), pp("2026-07-01T00:00:00Z", 10)},
		},
		bids: map[finish.Finish][]store.PricePoint{
			finish.Nonfoil: {pp("2026-06-01T00:00:00Z", 5), pp("2026-07-01T00:00:00Z", 8)},
		},
	}
	out := strings.Join(m.hoardLines(d, 140), "\n")
	if strings.Contains(out, "ck pays") {
		t.Fatalf("the comps verdict prose must never render:\n%s", out)
	}
	if !strings.Contains(out, "spread") {
		t.Fatalf("an ordinary entry should keep the PRICE spread trend:\n%s", out)
	}
	d.fromArbitrage = true
	out = strings.Join(m.hoardLines(d, 140), "\n")
	if strings.Contains(out, "spread") {
		t.Errorf("an arbitrage-section entry should suppress the spread trend:\n%s", out)
	}
	if !strings.Contains(out, "buylist") {
		t.Errorf("the buylist row itself should survive:\n%s", out)
	}
}

func TestDetailHeldFinishEdit(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerID: 1, ContainerName: "Binder", ContainerKind: store.KindCollection,
				Finish: finish.Nonfoil, Quantity: 4, ScryfallID: "Bitterblossom-id",
				SetCode: "uma", CollectorNumber: "85"},
		},
	}
	m := newTestModel(t, st)
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	m = key(m, "up")
	m = key(m, "right")
	m = key(m, "right")
	if m.detail.heldField != fieldFinish {
		t.Fatalf("field = %d, want finish", m.detail.heldField)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.prompt == nil || m.prompt.text != "-" {
		t.Fatalf("prompt = %+v, want prefilled with the display dash", m.prompt)
	}
	if err := m.prompt.validate("cardboard"); err == nil {
		t.Error("garbage finish validated")
	}
	if err := m.prompt.validate("foil"); err != nil {
		t.Errorf("foil refused: %v", err)
	}
	m.prompt.commit(&m, "foil")
	m.prompt = nil
	if !strings.Contains(m.status, "- → foil") {
		t.Errorf("status = %q, want the re-key named", m.status)
	}
	found := false
	for _, r := range st.collection {
		if r.ScryfallID == "Bitterblossom-id" && r.Finish == finish.Foil && r.Quantity == 4 {
			found = true
		}
		if r.ScryfallID == "Bitterblossom-id" && r.Finish == finish.Nonfoil {
			t.Errorf("source finish row survived: %+v", r)
		}
	}
	if !found {
		t.Errorf("no foil row after the re-key: %+v", st.collection)
	}
	if m.undoStack == nil {
		t.Error("finish change recorded no undo")
	}
}

func TestDetailCompsRowPerSheetOnly(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	m.cardComps = func(string) (map[finish.Finish]market.Comp, bool) { return nil, false }

	d := detail{compsOK: true, comps: map[finish.Finish]market.Comp{
		finish.Nonfoil: {Market: 34.47, HasMarket: true, Low: 34.47, LowFrom: "tcgplayer"},
		finish.Foil: {Market: 45.56, HasMarket: true, CK: 54.99, HasCK: true,
			Buylist: 27.50, BuylistTo: "cardkingdom", HasBuylist: true,
			Low: 45.56, LowFrom: "tcgplayer"},
	}}
	out := strings.Join(m.hoardLines(d, 140), "\n")
	if !strings.Contains(out, "non-foil") || !strings.Contains(out, "$45.56") {
		t.Fatalf("the two real rows are missing:\n%s", out)
	}
	if strings.Contains(out, "etched") {
		t.Errorf("etched row for a nonfoil/foil printing:\n%s", out)
	}

	d.comps = map[finish.Finish]market.Comp{
		finish.Etched: {Market: 11.55, HasMarket: true, CK: 6.99, HasCK: true,
			Buylist: 3.50, BuylistTo: "cardkingdom", HasBuylist: true,
			Low: 6.99, LowFrom: "cardkingdom"},
	}
	out = strings.Join(m.hoardLines(d, 140), "\n")
	if !strings.Contains(out, "etched") || !strings.Contains(out, "$6.99") {
		t.Errorf("a real etched row must still render:\n%s", out)
	}
}

func TestDetailScrollsOnWheel(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	m = next.(Model)

	wheel := func(m Model, b tea.MouseButton) Model {
		next, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: b})
		return next.(Model)
	}

	m = wheel(m, tea.MouseButtonWheelDown)
	if m.detail.scroll == 0 {
		t.Fatal("wheel down did not scroll the overlay")
	}
	down := m.detail.scroll

	m = wheel(m, tea.MouseButtonWheelUp)
	if m.detail.scroll >= down {
		t.Errorf("wheel up left scroll at %d, want less than %d", m.detail.scroll, down)
	}

	for range 10 {
		m = wheel(m, tea.MouseButtonWheelUp)
	}
	if m.detail.scroll != 0 {
		t.Errorf("scroll = %d after over-scrolling up, want 0", m.detail.scroll)
	}

	m.detail = nil
	_ = wheel(m, tea.MouseButtonWheelDown)
}

func TestPanesScrollOnWheel(t *testing.T) {
	m := newTestModel(t, testStore())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = next.(Model)

	wheel := func(m Model, b tea.MouseButton) Model {
		next, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: b})
		return next.(Model)
	}

	start := m.cursor[paneContainers]
	m = wheel(m, tea.MouseButtonWheelDown)
	if m.cursor[paneContainers] == start {
		t.Fatalf("wheel down left the cursor at %d — the panes are not scrolling", start)
	}
	moved := m.cursor[paneContainers]

	m = wheel(m, tea.MouseButtonWheelUp)
	if m.cursor[paneContainers] >= moved {
		t.Errorf("wheel up left the cursor at %d, want less than %d", m.cursor[paneContainers], moved)
	}

	for range 20 {
		m = wheel(m, tea.MouseButtonWheelUp)
	}
	if got := m.cursor[paneContainers]; got < 0 {
		t.Errorf("cursor = %d after over-scrolling up, want it clamped", got)
	}
}

func TestMouseCaptureFollowsTheOverlay(t *testing.T) {

	msgs := func(cmd tea.Cmd) []string {
		var out []string
		if cmd == nil {
			return out
		}
		switch v := cmd().(type) {
		case tea.BatchMsg:
			for _, c := range v {
				if c == nil {
					continue
				}
				out = append(out, fmt.Sprintf("%T", c()))
			}
		default:
			out = append(out, fmt.Sprintf("%T", v))
		}
		return out
	}
	has := func(got []string, want string) bool {
		return slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, want) })
	}

	m := newTestModel(t, testStore())
	m = key(m, "tab")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}
	if got := msgs(cmd); !has(got, "enableMouseCellMotion") {
		t.Errorf("opening the overlay sent %v, want the mouse enabled", got)
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.detail != nil {
		t.Fatal("overlay still open")
	}
	if got := msgs(cmd); !has(got, "disableMouse") {
		t.Errorf("closing the overlay sent %v, want the mouse released", got)
	}
}

func TestDetailTabWalksFieldsThenLinks(t *testing.T) {

	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {{
			ContainerID: 1, ContainerName: "Binder", ContainerKind: store.KindCollection,
			Finish: finish.Nonfoil, Quantity: 4,
			ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85",
		}},
	}
	m := newTestModel(t, st)
	m.openURL = func(string) error { return nil }
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("no detail")
	}
	if len(m.detail.holdings) == 0 {
		t.Fatal("no holdings — the fixture is wrong, not the tab order")
	}
	links := len(m.detail.links)
	if links < 2 {
		t.Fatalf("fixture has %d links; need at least 2 to prove tab walks them", links)
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)

	if m.detail.zone != zoneLinks || m.detail.linkCursor != 0 {
		t.Fatalf("opened at zone=%d link=%d, want the first link", m.detail.zone, m.detail.linkCursor)
	}

	for want := 1; want < links; want++ {
		m = key(m, "tab")
		if m.detail.zone != zoneLinks || m.detail.linkCursor != want {
			t.Fatalf("tab gave zone=%d link=%d, want link %d", m.detail.zone, m.detail.linkCursor, want)
		}
	}

	m = key(m, "tab")
	if m.detail.zone != zoneHeld || m.detail.heldField != fieldQty {
		t.Fatalf("zone=%d field=%d after the last link, want the row's first field",
			m.detail.zone, m.detail.heldField)
	}

	for want := fieldSet; want < heldFieldCount; want++ {
		m = key(m, "tab")
		if m.detail.zone != zoneHeld || m.detail.heldField != want {
			t.Fatalf("tab gave zone=%d field=%d, want field %d", m.detail.zone, m.detail.heldField, want)
		}
	}

	m = key(m, "tab")
	if m.detail.zone != zoneLinks || m.detail.linkCursor != 0 {
		t.Fatalf("zone=%d link=%d after the last field, want the first link",
			m.detail.zone, m.detail.linkCursor)
	}

	m = key(m, "shift+tab")
	if m.detail.zone != zoneHeld || m.detail.heldField != heldFieldCount-1 {
		t.Fatalf("shift+tab gave zone=%d field=%d, want the row's last field",
			m.detail.zone, m.detail.heldField)
	}
	for range heldFieldCount - 1 {
		m = key(m, "shift+tab")
	}
	if m.detail.zone != zoneHeld || m.detail.heldField != fieldQty {
		t.Fatalf("shift+tab gave zone=%d field=%d, want the row's first field",
			m.detail.zone, m.detail.heldField)
	}
	m = key(m, "shift+tab")
	if m.detail.zone != zoneLinks || m.detail.linkCursor != links-1 {
		t.Fatalf("shift+tab gave zone=%d link=%d, want the last link",
			m.detail.zone, m.detail.linkCursor)
	}

	before := m.detail.linkCursor
	m = key(m, "left")
	if m.detail.linkCursor != before-1 {
		t.Errorf("← gave link %d, want %d — the arrows must keep working", m.detail.linkCursor, before-1)
	}
}
