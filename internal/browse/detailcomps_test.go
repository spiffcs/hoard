package browse

// The detail overlay's vendor half: bid sparklines, the spread trend, and
// the per-card comp sheet.

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func pp(asOf string, price float64) store.PricePoint {
	return store.PricePoint{AsOf: asOf, Price: price, Source: "test"}
}

// Opening a detail loads the bid series alongside the price series.
func TestDetailOpenLoadsBidSeries(t *testing.T) {
	st := testStore()
	st.bidSeries = map[string][]store.PricePoint{
		"Bitterblossom-id|nonfoil": {pp("2026-07-01T00:00:00Z", 20), pp("2026-07-20T00:00:00Z", 24)},
	}
	m := newTestModel(t, st)
	m = key(m, "tab") // into the card pane
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("enter did not open the detail")
	}
	if got := m.detail.bids["nonfoil"]; len(got) != 2 {
		t.Fatalf("bids = %+v, want the seeded series", m.detail.bids)
	}
	out := strings.Join(m.hoardLines(*m.detail, 100), "\n")
	if !strings.Contains(out, "buylist") || !strings.Contains(out, "$24.00") {
		t.Errorf("bid row missing:\n%s", out)
	}
}

// The spread row appears when the two series overlap, tracks the trend
// direction, and stays hidden when they never share a window.
func TestDetailBidAndSpreadRows(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	d := detail{
		series: map[string][]store.PricePoint{
			"nonfoil": {pp("2026-06-01T00:00:00Z", 10), pp("2026-07-01T00:00:00Z", 10)},
		},
		bids: map[string][]store.PricePoint{
			"nonfoil": {pp("2026-06-01T00:00:00Z", 5), pp("2026-07-01T00:00:00Z", 8)},
		},
	}
	out := strings.Join(m.hoardLines(d, 120), "\n")
	if !strings.Contains(out, "buylist") || !strings.Contains(out, "$8.00") {
		t.Fatalf("bid row missing:\n%s", out)
	}
	// Flat $10 retail against a bid rising 5 → 8: the spread halves and
	// then some, 50% down to 20%.
	if !strings.Contains(out, "spread") || !strings.Contains(out, "50.0% → 20.0%") ||
		!strings.Contains(out, "tightening") {
		t.Errorf("spread trend missing or wrong:\n%s", out)
	}

	// Disjoint windows: a bid series that ends before the retail one
	// begins has no shared instant to compare at.
	d.bids["nonfoil"] = []store.PricePoint{pp("2026-01-01T00:00:00Z", 5), pp("2026-02-01T00:00:00Z", 6)}
	out = strings.Join(m.hoardLines(d, 120), "\n")
	if strings.Contains(out, "spread") {
		t.Errorf("spread row rendered without overlapping windows:\n%s", out)
	}
	if !strings.Contains(out, "buylist") {
		t.Errorf("the buylist row should survive alone:\n%s", out)
	}
}

// A bid series with no retail series still renders — the two tables have
// independent eras.
func TestDetailBidRowWithoutRetail(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	d := detail{
		series: map[string][]store.PricePoint{},
		bids: map[string][]store.PricePoint{
			"foil": {pp("2026-07-01T00:00:00Z", 3)},
		},
	}
	out := strings.Join(m.hoardLines(d, 120), "\n")
	if !strings.Contains(out, "buylist") || !strings.Contains(out, "$3.00") {
		t.Errorf("orphan bid row missing:\n%s", out)
	}
}

// The COMPS section renders the sheet in the market view's vocabulary,
// notes the missing day cache, and stays absent without the capability.
func TestDetailCompsSection(t *testing.T) {
	sheet := market.Comp{
		Market: 10, HasMarket: true,
		Manapool: 11, HasManapool: true,
		CK: 12, HasCK: true,
		Buylist: 7, BuylistTo: "cardkingdom", HasBuylist: true,
		Low: 10, LowFrom: "tcgplayer",
	}
	m := atAllCards(t, newTestModel(t, testStore()))

	// Uninjected: no section at all.
	d := detail{comps: map[string]market.Comp{"nonfoil": sheet}, compsOK: true}
	if out := strings.Join(m.hoardLines(d, 140), "\n"); strings.Contains(out, "COMPS") {
		t.Fatalf("COMPS rendered without the capability:\n%s", out)
	}

	m.cardComps = func(string) (map[string]market.Comp, bool) { return nil, false }
	// No day cache: the section says how to get one.
	d = detail{compsOK: false}
	out := strings.Join(m.hoardLines(d, 140), "\n")
	if !strings.Contains(out, "COMPS") || !strings.Contains(out, "press F on the MARKET view") {
		t.Fatalf("absent-cache note missing:\n%s", out)
	}

	// A full sheet, laid out as the aligned table.
	d = detail{
		comps: map[string]market.Comp{"nonfoil": sheet}, compsOK: true,
		holdings: []store.Holding{{ContainerName: "Binder", Finish: "nonfoil", Quantity: 1}},
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
	// A 70%-pays bid earns no line of its own — the CK PAYS column already
	// says it, and the EASY TO SELL verdict was cut as noise.
	if strings.Contains(out, "EASY TO SELL") {
		t.Errorf("liquid verdict should not render:\n%s", out)
	}
	// PRICE always precedes COMPS.
	if strings.Index(out, "PRICE") > strings.Index(out, "COMPS") {
		t.Errorf("COMPS rendered before PRICE:\n%s", out)
	}

	// A bid over the sales price is the one verdict that still speaks.
	arb := sheet
	arb.Buylist = 10.50
	d.comps = map[string]market.Comp{"nonfoil": arb}
	out = strings.Join(m.hoardLines(d, 140), "\n")
	if !strings.Contains(out, "ARBITRAGE") || !strings.Contains(out, "+$0.50 over tcg last-sold") {
		t.Errorf("arbitrage verdict missing:\n%s", out)
	}
}

// An unheld finish's sheet renders without a verdict — the verdict is
// about your copies, the numbers are about the card.
func TestDetailCompsVerdictNeedsAHolding(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	m.cardComps = func(string) (map[string]market.Comp, bool) { return nil, true }
	d := detail{
		comps: map[string]market.Comp{"foil": {
			Market: 10, HasMarket: true, Buylist: 11, BuylistTo: "cardkingdom", HasBuylist: true,
			Low: 10, LowFrom: "tcgplayer",
		}},
		compsOK:  true,
		holdings: []store.Holding{{ContainerName: "Binder", Finish: "nonfoil", Quantity: 1}},
	}
	out := strings.Join(m.hoardLines(d, 140), "\n")
	if !strings.Contains(out, "$11.00") {
		t.Fatalf("foil sheet missing:\n%s", out)
	}
	if strings.Contains(out, "ARBITRAGE") {
		t.Errorf("verdict granted for an unheld finish:\n%s", out)
	}
}

// The finish groups separate with a blank line — non-foil's spread row
// must not read as foil's opening act.
func TestDetailPriceGroupsSeparate(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	d := detail{
		series: map[string][]store.PricePoint{
			"nonfoil": {pp("2026-06-01T00:00:00Z", 10), pp("2026-07-01T00:00:00Z", 12)},
			"foil":    {pp("2026-06-01T00:00:00Z", 20), pp("2026-07-01T00:00:00Z", 24)},
		},
		bids: map[string][]store.PricePoint{
			"nonfoil": {pp("2026-06-01T00:00:00Z", 5), pp("2026-07-01T00:00:00Z", 6)},
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

// The detail's LINKS line: arrows move the cursor, enter opens the
// selected vendor page, esc still closes — and without an opener the old
// enter-closes behavior stands.
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

	// TCGplayer first: the stored product id links the exact page, not a
	// name search.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("enter with links must open the page, not close the overlay")
	}
	if len(opened) != 1 || !strings.Contains(opened[0], "tcgplayer.com/product/12345") {
		t.Fatalf("opened = %v, want tcgplayer's exact product", opened)
	}

	// Walk to manapool and open it: the exact printing, slugged name.
	m = key(m, "right")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if len(opened) != 2 || !strings.Contains(opened[1], "manapool.com/card/uma/85/bitterblossom") {
		t.Fatalf("opened = %v, want manapool's exact printing", opened)
	}

	// The cursor clamps at both ends.
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

// nameSlug survives the names that need it.
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

// The All cards list merges same-name same-finish printings into one row:
// quantities and values sum, and the printing columns survive only when
// every merged row agrees.
func TestAllCardsMergesByName(t *testing.T) {
	st := testStore()
	// A second Sol Ring printing in a deck: same name+finish, another set.
	st.deckCards[201] = append(st.deckCards[201], entry("Sol Ring", "main", "nonfoil", 2, 15))
	st.deckCards[201][len(st.deckCards[201])-1].Card.ScryfallID = "Sol Ring-alt-id"
	st.deckCards[201][len(st.deckCards[201])-1].Card.SetCode = "lea"
	m := atAllCards(t, newTestModel(t, st))

	var merged *card
	for i := range m.cards {
		if m.cards[i].Name == "Sol Ring" && m.cards[i].Finish == "nonfoil" {
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

// Scrolling the held list onto another printing re-points the overlay:
// card, series and links follow the selection.
func TestDetailHeldCursorSwitchesPrinting(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerName: "Binder", Finish: "nonfoil", Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
			{ContainerName: "Rich Deck", Finish: "nonfoil", Quantity: 1, Board: "main",
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

	m = key(m, "down")
	if m.detail.heldCursor != 1 {
		t.Fatalf("cursor = %d, want the deck's printing", m.detail.heldCursor)
	}
	if m.detail.card.ScryfallID != "Bitterblossom-mor-id" {
		t.Errorf("card = %s, want the overlay re-pointed", m.detail.card.ScryfallID)
	}
	if len(m.detail.bids["nonfoil"]) != 1 {
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

// A bid at or over the low ask is a zero-or-negative spread — the sheet's
// best news, which used to render as a blank cell (ui.Percent is empty at
// or below zero, observed live with all four vendor numbers showing).
func TestCompsNegativeSpreadRenders(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	m.cardComps = func(string) (map[string]market.Comp, bool) { return nil, true }
	d := detail{
		comps: map[string]market.Comp{"nonfoil": {
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

// A printing switch keeps the old art in place until the replacement
// arrives — blanking it collapsed the beside-image layout for a frame and
// every section jumped (observed live). When no replacement is coming,
// the stale art clears instead of captioning the wrong card.
func TestDetailSwitchKeepsImageInPlace(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerName: "Binder", Finish: "nonfoil", Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
			{ContainerName: "Rich Deck", Finish: "nonfoil", Quantity: 1,
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

	m = key(m, "down") // a fetch is possible: the old art holds the layout
	if m.detail.card.ScryfallID != "Bitterblossom-mor-id" {
		t.Fatalf("switch did not land: %s", m.detail.card.ScryfallID)
	}
	if len(m.detail.image) == 0 {
		t.Error("old art must hold the layout until the new art lands")
	}

	m.imageFetch = nil // no replacement possible: stale art must go
	m = key(m, "up")
	if m.detail.image != nil {
		t.Error("stale art kept with no replacement coming")
	}
}

// The Card Kingdom link follows the held finish: foil holdings get the
// foil page, a missing foil page falls back to the plain product page,
// and a never-resolved card falls back to the name search.
func TestCardLinksCKPerFinish(t *testing.T) {
	var c store.CardDetail
	c.Name = "Sol Ring"
	c.SetCode = "c21"
	c.CollectorNumber = "125"

	if got := cardLinks(c, false)[2].url; !strings.Contains(got, "catalog/search") {
		t.Errorf("unresolved card = %q, want the name search", got)
	}

	plain, foil := "https://mtgjson.com/links/aa", "https://mtgjson.com/links/bb"
	c.CKURL, c.CKFoilURL = &plain, &foil
	if got := cardLinks(c, false)[2].url; got != plain {
		t.Errorf("nonfoil link = %q, want the plain page", got)
	}
	if got := cardLinks(c, true)[2].url; got != foil {
		t.Errorf("foil link = %q, want the foil page", got)
	}
	c.CKFoilURL = nil
	if got := cardLinks(c, true)[2].url; got != plain {
		t.Errorf("foil holding with no foil page = %q, want the plain page", got)
	}
}

// Scrolling HELD between finishes of the same printing refreshes the
// links — Card Kingdom's page is per finish even when nothing else about
// the overlay changes.
func TestHeldCursorRefreshesLinksPerFinish(t *testing.T) {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{
		"Bitterblossom": {
			{ContainerName: "Binder", Finish: "nonfoil", Quantity: 4,
				ScryfallID: "Bitterblossom-id", SetCode: "uma", CollectorNumber: "85"},
			{ContainerName: "Rich Deck", Finish: "foil", Quantity: 1,
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
	m = key(m, "down") // same printing, foil finish
	if m.detail.heldCursor != 1 {
		t.Fatalf("cursor = %d", m.detail.heldCursor)
	}
	if !strings.Contains(ckAt(), "links/foil") {
		t.Errorf("foil row should link the foil page, got %q", ckAt())
	}
}

// While the art fetch runs, the layout reserves its footprint: HELD and
// everything under it render in their final positions from the first
// frame, and the art lands without moving them. A failed fetch answers
// with empty lines, which releases the space.
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
	blank := blankImage(m.detailImageCols())
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

	// The art lands at the reserved height: nothing moves.
	lines := make([]string, len(blank))
	for i := range lines {
		lines[i] = "ART"
	}
	next, _ = m.Update(imageMsg{scryfallID: m.detail.card.ScryfallID, lines: lines})
	m = next.(Model)
	if after := heldAt(); after != before {
		t.Errorf("HELD moved %d → %d when the art landed", before, after)
	}

	// A failure answer releases the reservation.
	next, _ = m.Update(imageMsg{scryfallID: m.detail.card.ScryfallID})
	m = next.(Model)
	if m.detail.imagePending || m.detail.image != nil {
		t.Errorf("failure must clear the reservation (pending=%v image=%v)",
			m.detail.imagePending, m.detail.image)
	}
}

// A price refresh run from the detail palette reports in the overlay's
// status slot — progress while it runs, the summary after — and the
// overlay's numbers reload when it lands. Silence read as the command not
// firing (observed live).
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

	// Run Update prices through the overlay's palette.
	m.openPalette()
	m.palette.query = "update prices"
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

	// Land the op: the summary shows in the overlay, and the detail's
	// series reload — prove it by growing the bid history underneath.
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
	if len(m.detail.bids["nonfoil"]) != 1 {
		t.Errorf("detail did not reload after the op: bids = %+v", m.detail.bids)
	}
}

// The price and buylist rows share one display window per finish: the two
// tables backfilled on different dates, and sparks captioned "since 29
// Apr" over "since 1 May" read as deliberately skewed (observed live).
func TestPriceAndBuylistShareTheirWindow(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	d := detail{
		series: map[string][]store.PricePoint{
			"nonfoil": {pp("2026-04-29T00:00:00Z", 30), pp("2026-07-01T00:00:00Z", 33)},
		},
		bids: map[string][]store.PricePoint{
			"nonfoil": {pp("2026-05-01T00:00:00Z", 18), pp("2026-07-01T00:00:00Z", 20)},
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

// Modal abilities wrap with a hanging indent: the continuation aligns
// under the mode's text, not under the bullet, where it read as another
// mode.
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
	// Plain paragraphs wrap flat, no phantom indent.
	if flat := wrapHang("Choose three. You may choose the same mode more than once.", 30); strings.HasPrefix(flat[1], " ") {
		t.Errorf("plain continuation %q must not indent", flat[1])
	}
}
