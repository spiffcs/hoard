package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// CardDetail is everything known about one printing: its identity, the market
// prices for each finish, and the descriptive fields derived from the stored
// Scryfall document. It is the one type for a printing's characteristics —
// the browse overlay and the JSON export read the same fields off the same
// projection, so neither can learn something the other cannot.
//
// THE EMPTY STRING IS THIS TYPE'S ONLY ABSENCE, AND IT IS DELIBERATE.
//
// A descriptive field reads "" both when the card has no such value — an
// artifact has no power, an English printing no printed name — and when hoard
// could not get the information. Those are one state here, not two, because in
// the data they were never actually two: the descriptive columns are
// json_extract over the stored document, and json_extract yields NULL for a key
// the document lacks exactly as it does when there is no document at all. A
// pointer could spell a difference the database cannot supply, which is how a
// convention that looks careful ends up lying.
//
// So there is nothing to deref and nothing to nil-check. A consumer asks
// `d.Power != ""`, and every consumer asks it the same way.
//
// Whether hoard has EVER FETCHED the printing is a separate question, about the
// printing rather than any field, and it is answered separately: Enriched on the
// single-card read, and absence from the map on the container read. That is what
// a caller checks before telling someone to run update-prices.
//
// Three fields do not follow the rule, because for them "" would be a lie
// rather than a simplification:
//
//   - CMC is *float64: zero is a real mana value that every land has, so there
//     is no spare zero to spend on absence.
//   - TCGplayerID is *int64: an id has no empty form, and 0 is a plausible-
//     looking id rather than an obvious non-answer.
//   - Card.ColorIdentity (embedded, and NOT this type's to change) is a slice
//     where empty and absent are genuinely different — colorless versus
//     never-fetched — which is the distinction 2ec5d32 exists to protect.
type CardDetail struct {
	Card // carries ColorIdentity and ManaCost for every listing row

	Rarity     string
	SetName    string
	TypeLine   string
	OracleText string
	Artist     string
	ReleasedAt string
	Layout     string
	// CMC is the mana value, a pointer for the reason the type comment gives:
	// zero is a real one. Absent only when hoard has no value at all.
	CMC *float64

	// The card-frame fields (migration v11): the stat box — Power and
	// Toughness for creatures, Loyalty for planeswalkers — the flavor
	// text, and the normal-size card image's URL. Power and toughness are
	// text because the game prints non-numbers there ("*", "1+*").
	Power      string
	Toughness  string
	Loyalty    string
	FlavorText string
	ImageURI   string

	// TCGplayerID is the marketplace's product id (migration v14), nil for
	// un-enriched cards and the few printings TCGplayer does not list —
	// what links the exact product page instead of a name search.
	TCGplayerID *int64

	// CKURL and CKFoilURL are Card Kingdom's product links (migration v15),
	// learned from the MTGJSON set files; empty both before the resolver has
	// passed this card's set and when the feed carried none.
	//
	// The COLUMN does keep those apart — NULL means never asked — and the
	// resolver depends on it, but it reads ck_url IS NOT NULL directly, in
	// KnownCardKingdomLinks. Nothing needs the difference through this type,
	// which only ever answers "is there a link to open?".
	CKURL     string
	CKFoilURL string

	// PromoTypes are Scryfall's variant tags for the printing ("surgefoil",
	// "showcase"), nil on an ordinary one. The same column behind Card.Treatment
	// beside it, kept whole here: Treatment is the single display word a listing
	// row shows, these are the tags a consumer can filter on.
	PromoTypes []string

	// PrintedName is the name in the printing's own language and script, empty
	// on an English printing.
	PrintedName string

	// Enriched is false when no Scryfall document is stored for this printing,
	// so a caller can say "run update-prices" once rather than printing unknown
	// against every field in turn. It is the ONLY way to tell an unfetched
	// printing from one that genuinely has little to say.
	Enriched bool
}

// cardDetailCols selects the identity and price columns alongside the generated
// ones, with the same fallback prices every other valuation query applies.
//
// Every text column is COALESCEd to the empty string so the scan lands in a
// plain string, which is CardDetail's absence convention expressed in SQL: a card
// with no power and a card nobody has fetched both read empty, because NULL is
// all the database can say about either. cmc and tcgplayer_id stay nullable —
// their zero values are meaningful or misleading, as the type comment explains.
//
// Both reads below share this projection and cardDetailScanDest, for the reason
// cardCols and cardScanDest sit together: two hand-maintained column lists over
// the same table drift, and the drift surfaces as a runtime scan error or, far
// worse, as one caller silently seeing less than the other.
var cardDetailCols = `
SELECT ` + cardCols(altSourceExpr) + `,
       COALESCE(c.rarity, ''), COALESCE(c.set_name, ''),
       COALESCE(c.type_line, ''), COALESCE(c.oracle_text, ''),
       COALESCE(c.artist, ''), COALESCE(c.released_at, ''),
       COALESCE(c.layout, ''), c.cmc,
       COALESCE(c.power, ''), COALESCE(c.toughness, ''),
       COALESCE(c.loyalty, ''), COALESCE(c.flavor_text, ''),
       COALESCE(c.image_uri, ''),
       c.tcgplayer_id, COALESCE(c.ck_url, ''), COALESCE(c.ck_foil_url, ''),
       COALESCE(c.printed_name, ''),
       ` + enrichedExpr + `
FROM cards c ` + altJoinCards

// cardDetailScanDest is the scan targets matching cardDetailCols. After the
// scan, aux.applyDetail(d) completes the shimmed fields.
func cardDetailScanDest(d *CardDetail, aux *cardAux) []any {
	return append(cardScanDest(&d.Card, aux),
		&d.Rarity, &d.SetName, &d.TypeLine, &d.OracleText,
		&d.Artist, &d.ReleasedAt, &d.Layout, &d.CMC,
		&d.Power, &d.Toughness, &d.Loyalty, &d.FlavorText, &d.ImageURI,
		&d.TCGplayerID, &d.CKURL, &d.CKFoilURL, &d.PrintedName,
		&d.Enriched)
}

// applyDetail finishes a cardDetailScanDest scan: the Card shims, plus the
// promo tags this type carries whole beside the display word Card derives from
// the very same column.
func (a cardAux) applyDetail(d *CardDetail) {
	a.apply(&d.Card)
	d.PromoTypes = parsePromoTypes(a.promoTypes)
}

// CardDetail returns one printing with its descriptive fields resolved.
//
// Unlike the container read below, this one answers for a printing hoard may
// never have fetched — so it does not filter on raw_json, and reports that
// state in Enriched rather than by returning nothing.
func (s *Store) CardDetail(scryfallID string) (CardDetail, error) {
	row := s.db.QueryRow(cardDetailCols+` WHERE c.scryfall_id = ?`, scryfallID)

	var d CardDetail
	var aux cardAux
	if err := row.Scan(cardDetailScanDest(&d, &aux)...); err != nil {
		return CardDetail{}, fmt.Errorf("reading card %s: %w", scryfallID, err)
	}
	aux.applyDetail(&d)
	return d, nil
}

// CardDetailsInContainer returns the detail of every printing held in one
// container, keyed by Scryfall id.
//
// Scoped to a container rather than read per row: the descriptive columns are
// computed by json_extract over the whole stored document, so reading them once
// per printing instead of once per holding is the difference that keeps an
// export cheap.
//
// EVERY held printing comes back, fetched or not, and Enriched on each row is
// what says which. Map membership answers "is this printing held here?" and
// nothing else — a caller asking whether hoard has card data for a printing
// reads Enriched, exactly as the single-card read's callers do.
//
// It deliberately does not filter on enrichedExpr. It could — every row it
// returned would then have Enriched true — but that would make presence in the
// map a SECOND way to ask the same question, and two mechanisms for one fact is
// how the export path and the browse overlay came to branch on different ones.
func (s *Store) CardDetailsInContainer(containerID int64) (map[string]CardDetail, error) {
	rows, err := s.db.Query(cardDetailCols+`
WHERE c.scryfall_id IN (SELECT e.scryfall_id FROM card_entries e WHERE e.container_id = ?)`,
		containerID)
	if err != nil {
		return nil, fmt.Errorf("reading card details: %w", err)
	}
	defer rows.Close()

	out := make(map[string]CardDetail)
	for rows.Next() {
		var d CardDetail
		var aux cardAux
		if err := rows.Scan(cardDetailScanDest(&d, &aux)...); err != nil {
			return nil, fmt.Errorf("reading card details: %w", err)
		}
		aux.applyDetail(&d)
		out[d.ScryfallID] = d
	}
	return out, rows.Err()
}

// parsePromoTypes decodes the stored promo_types array into its tags, nil when
// the printing carries none.
//
// Decoded properly rather than hand-split like parseColorIdentity: colour
// identities are single letters from a closed set, but a promo tag is an
// arbitrary string WotC invents at will ("stepandcompleat"), and a hand-split
// would have to guess at escaping it does not control. A value that will not
// decode yields no tags rather than an error — a caller can do nothing better
// with one, and the tags are descriptive, never load-bearing.
func parsePromoTypes(v sql.NullString) []string {
	if !v.Valid || v.String == "" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(v.String), &tags); err != nil {
		return nil
	}
	return tags
}

// Holding is a quantity of one printing-and-finish sitting in one container.
//
// The printing identity fields are filled by the by-name query, which
// spans printings; the by-id query leaves them zero — its caller already
// knows the printing it asked about.
type Holding struct {
	ContainerID   int64
	ContainerName string
	ContainerKind string // collection | deck
	Finish        string
	// Condition is the card's wear, 'unknown' when nobody has said. It is part
	// of what identifies the holding, so an undo that dropped it would restore
	// several distinct rows as one unassessed bucket.
	Condition string
	Board     string
	Quantity  int

	ScryfallID      string
	SetCode         string
	CollectorNumber string
	// Treatment is the foil treatment's display word, empty for plain —
	// same semantics as Card.Treatment. Filled by the by-name query.
	Treatment string
	// Guessed says a scanner guess is still standing against this
	// container+printing+finish: the finish was committed by default, not
	// evidence, and nobody has checked the card yet. It is display truth,
	// not holding identity — undo/restore ignores it.
	Guessed bool
}

// HoldingsOf reports every container holding a printing, and how many.
//
// This is the question the CLI could never answer: `list` sees the loose
// collection, `deck show` sees one deck, and neither can say that the four
// copies of a card are one loose and three spread across two decks.
func (s *Store) HoldingsOf(scryfallID string) ([]Holding, error) {
	rows, err := s.db.Query(`
SELECT ct.id, ct.name, ct.kind, e.finish, e.condition, e.board, e.quantity,
       EXISTS(SELECT 1 FROM finish_guesses g
              WHERE g.container_id = e.container_id
                AND g.scryfall_id = e.scryfall_id
                AND g.finish = e.finish) AS guessed
FROM card_entries e
JOIN containers ct ON ct.id = e.container_id
WHERE e.scryfall_id = ?
-- The loose collection first, then decks by name: what you hold free to use
-- ranks above what is already committed to a list. Spelled as a CASE rather
-- than leaning on 'collection' sorting before 'deck' by luck of the alphabet.
ORDER BY CASE ct.kind WHEN '`+KindCollection+`' THEN 0 ELSE 1 END,
         ct.name, e.finish, e.condition, e.board`, scryfallID)
	if err != nil {
		return nil, fmt.Errorf("reading holdings of %s: %w", scryfallID, err)
	}
	defer rows.Close()

	var out []Holding
	for rows.Next() {
		var h Holding
		if err := rows.Scan(&h.ContainerID, &h.ContainerName, &h.ContainerKind,
			&h.Finish, &h.Condition, &h.Board, &h.Quantity, &h.Guessed); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// HoldingsOfName reports every container holding any printing of a card
// name, each row naming its exact printing — the physical reality behind
// a name-merged row: ten Forests can be four printings, each with its own
// price and art. Ordered like HoldingsOf, then by printing, so a detail's
// held list groups stably.
func (s *Store) HoldingsOfName(name string) ([]Holding, error) {
	rows, err := s.db.Query(`
SELECT ct.id, ct.name, ct.kind, e.finish, e.condition, e.board, e.quantity,
       c.scryfall_id, c.set_code, c.collector_number, c.promo_types,
       EXISTS(SELECT 1 FROM finish_guesses g
              WHERE g.container_id = e.container_id
                AND g.scryfall_id = e.scryfall_id
                AND g.finish = e.finish) AS guessed
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
JOIN containers ct ON ct.id = e.container_id
WHERE c.name = ?
ORDER BY CASE ct.kind WHEN '`+KindCollection+`' THEN 0 ELSE 1 END,
         ct.name, c.set_code, c.collector_number, e.finish, e.condition, e.board`, name)
	if err != nil {
		return nil, fmt.Errorf("reading holdings of %q: %w", name, err)
	}
	defer rows.Close()

	var out []Holding
	for rows.Next() {
		var h Holding
		var promos sql.NullString
		if err := rows.Scan(&h.ContainerID, &h.ContainerName, &h.ContainerKind,
			&h.Finish, &h.Condition, &h.Board, &h.Quantity,
			&h.ScryfallID, &h.SetCode, &h.CollectorNumber, &promos, &h.Guessed); err != nil {
			return nil, err
		}
		h.Treatment = FoilTreatment(promos)
		out = append(out, h)
	}
	return out, rows.Err()
}

// PricePoint is one observation in a card's price history.
type PricePoint struct {
	AsOf   string // RFC3339
	Price  float64
	Source string
}

// PriceSeries returns every price recorded for one printing and finish, oldest
// first.
//
// Movers reads two endpoints of a series; this reads the whole thing, which is
// what a sparkline needs. Only the days a price actually moved are stored, so
// the points are irregularly spaced — a renderer that assumes one point per day
// will compress a quiet month into the same width as a volatile week.
func (s *Store) PriceSeries(scryfallID, finish string) ([]PricePoint, error) {
	rows, err := s.db.Query(`
SELECT as_of, price_usd, source
FROM card_price_history
WHERE scryfall_id = ? AND finish = ?
ORDER BY as_of`, scryfallID, finish)
	if err != nil {
		return nil, fmt.Errorf("reading price series for %s: %w", scryfallID, err)
	}
	defer rows.Close()

	var out []PricePoint
	for rows.Next() {
		var p PricePoint
		if err := rows.Scan(&p.AsOf, &p.Price, &p.Source); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
