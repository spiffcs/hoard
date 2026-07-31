// Package hoardjson defines hoard's machine-readable output: one versioned
// document envelope shared by every command that can emit JSON.
//
// The types here are the schema. The files under schema/json/ are generated
// from them (make generate-json-schema), a drift test keeps the two in step,
// and field names are a compatibility promise once released — renaming one is
// a breaking change and a new MODEL version, per schema/json/README.md.
//
// Vocabulary is borrowed wherever an ecosystem already has one: card
// references carry both a Scryfall id and an MTGJSON uuid, finishes use
// Scryfall's spelling (nonfoil|foil|etched), and set codes and collector
// numbers mean what those ecosystems mean by them. Only what no external
// schema models — containers, boards, movers, opportunities — is hoard's own.
package hoardjson

import (
	"encoding/json"
	"io"
)

// SchemaVersion is the SchemaVer (MODEL.REVISION.ADDITION) of the document
// this package emits. MODEL increments on breaking changes, REVISION on
// compatible reshapes, ADDITION on new optional fields; the matching
// schema-X.Y.Z.json is immutable once released.
const SchemaVersion = "1.0.0"

// Kind names which payload a document carries; exactly the one field of the
// same name is present.
type Kind string

const (
	KindSummary   Kind = "summary"
	KindHoldings  Kind = "holdings"
	KindUnpriced  Kind = "unpriced"
	KindMovers    Kind = "movers"
	KindArbitrage Kind = "arbitrage"
)

// Document is the envelope every hoard JSON emission shares: a schema version,
// a kind, and the one payload the kind names.
type Document struct {
	SchemaVersion string `json:"schemaVersion"`
	Kind          Kind   `json:"kind" jsonschema:"enum=summary,enum=holdings,enum=unpriced,enum=movers,enum=arbitrage"`

	Summary   *Summary   `json:"summary,omitempty"`
	Holdings  *Holdings  `json:"holdings,omitempty"`
	Unpriced  *Unpriced  `json:"unpriced,omitempty"`
	Movers    *Movers    `json:"movers,omitempty"`
	Arbitrage *Arbitrage `json:"arbitrage,omitempty"`
}

// Card identifies one printing in one finish. ScryfallID is always present;
// MTGJSONUUID is present when the printing has an MTGJSON mapping. SetCode and
// Number are the ecosystem's own values, so a document joins directly against
// Scryfall bulk data or MTGJSON's AllPrintings.
type Card struct {
	Name        string `json:"name"`
	ScryfallID  string `json:"scryfallId"`
	MTGJSONUUID string `json:"mtgjsonUuid,omitempty"`
	SetCode     string `json:"setCode"`
	Number      string `json:"number"`
	Finish      string `json:"finish" jsonschema:"enum=nonfoil,enum=foil,enum=etched"`
}

// Summary is the hoard's totals: the loose collection, each deck, and the
// grand total, as the summary table reports them.
type Summary struct {
	Binder Totals       `json:"binder"`
	Decks  []DeckTotals `json:"decks"`
	Total  GrandTotal   `json:"total"`
}

// Totals rolls one container group up: how many distinct printings, how many
// physical copies, and their market value.
type Totals struct {
	DistinctCards int     `json:"distinctCards"`
	TotalCopies   int     `json:"totalCopies"`
	ValueUsd      float64 `json:"valueUsd"`
}

// DeckTotals is one deck's totals, ordered by value as the summary lists them.
type DeckTotals struct {
	Name string `json:"name"`
	Totals
}

// GrandTotal is the whole hoard. It carries no distinct-card count because the
// binder and the decks can hold the same printing, and summing their counts
// would double-count it.
type GrandTotal struct {
	TotalCopies int     `json:"totalCopies"`
	ValueUsd    float64 `json:"valueUsd"`
}

// Holdings is every requested holding, one row per printing-finish-container,
// in the export's canonical order — the same data as the canonical CSV.
type Holdings struct {
	Rows []Holding `json:"rows"`
}

// Holding is a quantity of one card in one container. PriceUsd is per copy and
// absent when no source can price the card — absent is not free.
type Holding struct {
	Card          Card     `json:"card"`
	Count         int      `json:"count"`
	Container     string   `json:"container"`
	ContainerKind string   `json:"containerKind" jsonschema:"enum=binder,enum=deck"`
	Board         string   `json:"board"`
	PriceUsd      *float64 `json:"priceUsd,omitempty"`
}

// Unpriced is every holding no source can price — the copies counting as
// $0.00 in every total.
type Unpriced struct {
	Rows []UnpricedRow `json:"rows"`
}

// UnpricedRow is one unpriced card-and-finish and the containers holding it.
type UnpricedRow struct {
	Card       Card     `json:"card"`
	Copies     int      `json:"copies"`
	Containers []string `json:"containers"`
}

// Movers is the price changes between two observations, every change rather
// than the display's top-N, ordered by absolute impact.
type Movers struct {
	// Since is the comparison cutoff (RFC 3339): each card's newest price
	// against its last price recorded on or before this instant.
	Since string `json:"since"`
	// RecordedSince is the oldest price observation held (RFC 3339). When it
	// is later than Since, history simply does not reach as far back as the
	// question asked.
	RecordedSince string        `json:"recordedSince,omitempty"`
	Changes       []PriceChange `json:"changes"`
}

// PriceChange is one printing-and-finish whose per-copy price moved.
// ImpactUsd is the move across every copy held (copies × (new − old)) — the
// figure the ordering sorts on.
type PriceChange struct {
	Card      Card    `json:"card"`
	Copies    int     `json:"copies"`
	OldUsd    float64 `json:"oldUsd"`
	NewUsd    float64 `json:"newUsd"`
	ImpactUsd float64 `json:"impactUsd"`
	// Source names where the new price came from: "scryfall", or the vendor
	// behind a fallback.
	Source string `json:"source"`
}

// Arbitrage is one pass of vendor disagreement over everything held: the full
// ranking behind all three tables, not the display's top-N.
type Arbitrage struct {
	// ComparedPrintings is how many owned printings had two or more vendors;
	// IgnoredListings is how many quotes were discarded as unsupported
	// outliers. Reported so a consumer knows what was left out.
	ComparedPrintings int           `json:"comparedPrintings"`
	IgnoredListings   int           `json:"ignoredListings"`
	Opportunities     []Opportunity `json:"opportunities"`
}

// Opportunity is one owned printing-and-finish with the vendor quotes that
// make it interesting, tagged with the question it answers: "arbitrage" (a
// shop pays more than the cheapest retail), "liquid" (buylist close to
// retail), or "spread" (vendors disagree). A card answering several questions
// appears once per question.
//
// The sell-side fields are present only when some buylist quoted the card.
// Spread and Liquidity are fractions, not percentages: a spread of 0.5 means
// the dearest corroborated retail is 50% above the cheapest.
type Opportunity struct {
	Card     Card    `json:"card"`
	Copies   int     `json:"copies"`
	ValueUsd float64 `json:"valueUsd"`
	Kind     string  `json:"kind" jsonschema:"enum=arbitrage,enum=liquid,enum=spread"`

	BuyUsd   float64  `json:"buyUsd"`
	BuyFrom  string   `json:"buyFrom"`
	DearUsd  float64  `json:"dearUsd"`
	DearFrom string   `json:"dearFrom"`
	Spread   float64  `json:"spread"`
	SellUsd  *float64 `json:"sellUsd,omitempty"`
	SellTo   string   `json:"sellTo,omitempty"`
	// ProfitUsd is sellUsd − buyUsd per copy; positive is genuine arbitrage.
	ProfitUsd *float64 `json:"profitUsd,omitempty"`
	// Liquidity is the fraction of the cheapest retail a shop will pay.
	Liquidity *float64 `json:"liquidity,omitempty"`
}

// Write emits one document: two-space indented, HTML left unescaped (card
// names contain '&', not markup), one trailing newline. Field order follows
// the struct definitions, so the same data always produces the same bytes.
func Write(w io.Writer, doc Document) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
