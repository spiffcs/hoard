package store

import (
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// enriched is a card carrying a Scryfall document, so its generated columns
// resolve the way a refreshed catalog's do.
func enriched() scryfall.Card {
	c := ulamog()
	c.Raw = []byte(`{"id":"ulamog-id","name":"Ulamog, the Infinite Gyre",
	  "set":"uma","set_name":"Ultimate Masters","collector_number":"7",
	  "rarity":"mythic","type_line":"Legendary Creature — Eldrazi",
	  "mana_cost":"{11}","cmc":11.0,"oracle_text":"Annihilator 4",
	  "artist":"Mark Tedin","released_at":"2018-12-07","layout":"nonfoil",
	  "color_identity":[]}`)
	return c
}

func TestCardDetailResolvesDescriptiveFields(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(enriched(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	d, err := s.CardDetail("ulamog-id")
	if err != nil {
		t.Fatalf("CardDetail: %v", err)
	}
	if !d.Enriched {
		t.Fatal("Enriched = false for a card with a stored document")
	}
	if d.Rarity == nil || *d.Rarity != "mythic" {
		t.Errorf("Rarity = %v, want mythic", d.Rarity)
	}
	if d.TypeLine == nil || *d.TypeLine != "Legendary Creature — Eldrazi" {
		t.Errorf("TypeLine = %v", d.TypeLine)
	}
	if d.CMC == nil || *d.CMC != 11.0 {
		t.Errorf("CMC = %v, want 11", d.CMC)
	}
	if d.Artist == nil || *d.Artist != "Mark Tedin" {
		t.Errorf("Artist = %v", d.Artist)
	}
	// Identity carries through from the base columns, not the document.
	if d.Name != "Ulamog, the Infinite Gyre" || d.SetCode != "uma" {
		t.Errorf("identity wrong: %+v", d.Card)
	}
}

// A card with no stored document must report unknown, not empty. A detail pane
// that prints "" for rarity is indistinguishable from one printing a real value
// that happens to be blank, and hides the fact that a refresh would fix it.
func TestCardDetailReportsUnenrichedCards(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil { // no Raw
		t.Fatalf("AddCard: %v", err)
	}

	d, err := s.CardDetail("ulamog-id")
	if err != nil {
		t.Fatalf("CardDetail: %v", err)
	}
	if d.Enriched {
		t.Error("Enriched = true without a stored document")
	}
	if d.Rarity != nil || d.TypeLine != nil || d.CMC != nil {
		t.Errorf("want nil descriptive fields, got %v %v %v", d.Rarity, d.TypeLine, d.CMC)
	}
	// The prices and identity still work; only the derived fields are missing.
	if d.PriceUSD == nil || *d.PriceUSD != 10.00 {
		t.Errorf("PriceUSD = %v, want 10", d.PriceUSD)
	}
}

// Identity has three states, and two of them look alike in a slice: a
// colorless card is empty-but-known ([]), a never-enriched card is nil
// (unknown). slices.Equal treats those as equal, so nil-ness is asserted
// separately.
func TestParseColorIdentity(t *testing.T) {
	s := newTestStore(t)
	for _, tc := range []struct {
		name    string
		json    string // "" stores no document at all
		want    []string
		wantNil bool
	}{
		{"mono", `["B"]`, []string{"B"}, false},
		{"two colours", `["W","U"]`, []string{"W", "U"}, false},
		{"colourless", `[]`, []string{}, false},
		{"unknown", "", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := ulamog()
			c.ID = "c-" + tc.name
			if tc.json != "" {
				c.Raw = []byte(`{"color_identity":` + tc.json + `}`)
			}
			if err := s.UpsertPrintings([]scryfall.Card{c}); err != nil {
				t.Fatalf("upsert: %v", err)
			}
			d, err := s.CardDetail(c.ID)
			if err != nil {
				t.Fatalf("CardDetail: %v", err)
			}
			if !slices.Equal(d.ColorIdentity, tc.want) {
				t.Errorf("ColorIdentity = %v, want %v", d.ColorIdentity, tc.want)
			}
			if got := d.ColorIdentity == nil; got != tc.wantNil {
				t.Errorf("ColorIdentity nil = %v, want %v — colorless and unknown must stay distinct", got, tc.wantNil)
			}
		})
	}
}

// The card-frame fields (migration v11) resolve like the rest of the
// derived columns: root first, face 0 for multi-faced cards, nil until a
// document is stored.
func TestCardDetailCardFrameFields(t *testing.T) {
	s := newTestStore(t)
	mk := func(id, raw string) scryfall.Card {
		c := ulamog()
		c.ID = id
		if raw != "" {
			c.Raw = []byte(raw)
		}
		return c
	}
	cards := []scryfall.Card{
		mk("creature", `{"power":"10","toughness":"10","flavor_text":"A force of nature.",
		  "image_uris":{"normal":"https://img/creature.jpg"}}`),
		mk("walker", `{"loyalty":"4"}`),
		mk("dfc", `{"card_faces":[{"power":"2","toughness":"3",
		  "image_uris":{"normal":"https://img/face0.jpg"}}]}`),
		mk("bare", ""),
	}
	if err := s.UpsertPrintings(cards); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	get := func(id string) CardDetail {
		t.Helper()
		d, err := s.CardDetail(id)
		if err != nil {
			t.Fatalf("CardDetail(%s): %v", id, err)
		}
		return d
	}

	c := get("creature")
	if deref(c.Power) != "10" || deref(c.Toughness) != "10" {
		t.Errorf("creature P/T = %v/%v", c.Power, c.Toughness)
	}
	if deref(c.FlavorText) != "A force of nature." {
		t.Errorf("FlavorText = %v", c.FlavorText)
	}
	if deref(c.ImageURI) != "https://img/creature.jpg" {
		t.Errorf("ImageURI = %v", c.ImageURI)
	}

	if w := get("walker"); deref(w.Loyalty) != "4" || w.Power != nil {
		t.Errorf("walker loyalty = %v, power = %v", w.Loyalty, w.Power)
	}
	if d := get("dfc"); deref(d.Power) != "2" || deref(d.ImageURI) != "https://img/face0.jpg" {
		t.Errorf("dfc face-0 fallback: power = %v, image = %v", d.Power, d.ImageURI)
	}
	if b := get("bare"); b.Power != nil || b.Loyalty != nil || b.FlavorText != nil || b.ImageURI != nil {
		t.Errorf("unenriched card must read all-nil, got %+v", b)
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// The listing queries carry the same identity the detail view resolves, so
// the browse tables can tint names without a per-row detail read.
func TestListingsCarryColorIdentity(t *testing.T) {
	s := newTestStore(t)
	c := ulamog()
	c.Raw = []byte(`{"color_identity":["W","U"],"mana_cost":"{1}{W}{U}"}`)
	if err := s.AddCardFinish(c, "nonfoil", 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	rows, err := s.ListCollectionByFinish()
	if err != nil {
		t.Fatalf("ListCollectionByFinish: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if got := rows[0].ColorIdentity; !slices.Equal(got, []string{"W", "U"}) {
		t.Errorf("ColorIdentity = %v, want [W U]", got)
	}
	if rows[0].ManaCost == nil || *rows[0].ManaCost != "{1}{W}{U}" {
		t.Errorf("ManaCost = %v, want {1}{W}{U}", rows[0].ManaCost)
	}
}

// The question neither `list` nor `deck show` could answer: four copies of a
// card spread across the collection and two decks.
func TestHoldingsOfSpansContainers(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	for _, name := range []string{"Deck A", "Deck B"} {
		if _, err := s.UpsertDeck(
			DeckMeta{Name: name, Source: "text", SourceID: name},
			[]Entry{{ScryfallID: "ulamog-id", Finish: "nonfoil", Board: "main", Quantity: 2}},
		); err != nil {
			t.Fatalf("UpsertDeck %s: %v", name, err)
		}
	}

	holdings, err := s.HoldingsOf("ulamog-id")
	if err != nil {
		t.Fatalf("HoldingsOf: %v", err)
	}
	if len(holdings) != 3 {
		t.Fatalf("got %d holdings, want 3 (collection + two decks): %+v", len(holdings), holdings)
	}
	var total int
	var kinds []string
	for _, h := range holdings {
		total += h.Quantity
		kinds = append(kinds, h.ContainerKind)
	}
	if total != 5 {
		t.Errorf("total copies = %d, want 5", total)
	}
	// The collection sorts first, so a reader sees what they hold loose before
	// what is committed to decks.
	if kinds[0] != KindCollection {
		t.Errorf("kinds = %v, want the collection first", kinds)
	}
}

func TestHoldingsOfUnheldCard(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertPrintings([]scryfall.Card{ulamog()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	holdings, err := s.HoldingsOf("ulamog-id")
	if err != nil {
		t.Fatalf("HoldingsOf: %v", err)
	}
	if len(holdings) != 0 {
		t.Errorf("got %+v, want none for a catalogued but unheld card", holdings)
	}
}

func TestPriceSeriesReturnsTheWholeSeries(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-07-01T00:00:00Z")

	risen := ulamog()
	risen.PriceUSD = f(12.00)
	if err := s.UpsertPrintings([]scryfall.Card{risen}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}

	series, err := s.PriceSeries("ulamog-id", "nonfoil")
	if err != nil {
		t.Fatalf("PriceSeries: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("got %d points, want 2: %+v", len(series), series)
	}
	// Oldest first, so a sparkline reads left to right without reversing.
	if series[0].Price != 10.00 || series[1].Price != 12.00 {
		t.Errorf("series = %+v, want 10 then 12", series)
	}
	if series[0].AsOf >= series[1].AsOf {
		t.Errorf("series not in chronological order: %+v", series)
	}
	// The foil series is its own; the non-foil move must not appear in it.
	foil, err := s.PriceSeries("ulamog-id", "foil")
	if err != nil {
		t.Fatalf("PriceSeries foil: %v", err)
	}
	if len(foil) != 1 {
		t.Errorf("foil series = %+v, want just its baseline", foil)
	}
}

// HoldingsOfName spans printings: ten Forests across two printings and
// three containers come back one row each, printing named, collection
// first.
func TestHoldingsOfNameSpansPrintings(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	alt := ulamog()
	alt.ID = "ulamog-alt-id"
	alt.Set = "roe"
	alt.CollectorNumber = "8"
	if err := s.AddCardFinish(alt, "foil", 1); err != nil {
		t.Fatalf("AddCardFinish alt: %v", err)
	}
	if _, err := s.UpsertDeck(DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]Entry{{ScryfallID: "ulamog-alt-id", Finish: "foil", Board: "main", Quantity: 1}}); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}

	got, err := s.HoldingsOfName("Ulamog, the Infinite Gyre")
	if err != nil {
		t.Fatalf("HoldingsOfName: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("holdings = %+v, want both printings", got)
	}
	ids := map[string]bool{}
	for _, h := range got {
		if h.ScryfallID == "" || h.SetCode == "" {
			t.Errorf("holding %+v lacks its printing identity", h)
		}
		ids[h.ScryfallID] = true
	}
	if !ids["ulamog-id"] || !ids["ulamog-alt-id"] {
		t.Errorf("printings seen = %v, want both", ids)
	}
}

// v14 surfaces TCGplayer's product id from the stored Scryfall document —
// nil until enrichment stores one, the id after.
func TestCardDetailCarriesTCGplayerID(t *testing.T) {
	s := newTestStore(t)
	c := ulamog()
	c.Raw = []byte(`{"tcgplayer_id": 33365, "rarity": "mythic"}`)
	if err := s.AddCardFinish(c, "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	d, err := s.CardDetail("ulamog-id")
	if err != nil {
		t.Fatalf("CardDetail: %v", err)
	}
	if d.TCGplayerID == nil || *d.TCGplayerID != 33365 {
		t.Errorf("TCGplayerID = %v, want 33365", d.TCGplayerID)
	}

	bare := solRing()
	if err := s.AddCardFinish(bare, "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	d, err = s.CardDetail("sol-id")
	if err != nil {
		t.Fatalf("CardDetail: %v", err)
	}
	if d.TCGplayerID != nil {
		t.Errorf("TCGplayerID = %v for an un-enriched card, want nil", *d.TCGplayerID)
	}
}

// v15's null-vs-empty contract: NULL means the set file was never read
// for this card, an empty string means it was and carried no link — the
// distinction that stops absence from re-fetching the file forever.
func TestCardKingdomLinksNullVsEmpty(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	known, err := s.KnownCardKingdomLinks()
	if err != nil || known["ulamog-id"] {
		t.Fatalf("known = %v, %v; want never-asked", known, err)
	}
	if err := s.SaveCardKingdomLinks(map[string]CKLinks{"ulamog-id": {}}); err != nil {
		t.Fatalf("SaveCardKingdomLinks: %v", err)
	}
	known, err = s.KnownCardKingdomLinks()
	if err != nil || !known["ulamog-id"] {
		t.Fatalf("known = %v, %v; want asked-and-none counted as asked", known, err)
	}
	d, err := s.CardDetail("ulamog-id")
	if err != nil {
		t.Fatalf("CardDetail: %v", err)
	}
	if d.CKURL == nil || *d.CKURL != "" || d.CKFoilURL == nil || *d.CKFoilURL != "" {
		t.Errorf("links = %v/%v, want recorded-empty", d.CKURL, d.CKFoilURL)
	}
}
