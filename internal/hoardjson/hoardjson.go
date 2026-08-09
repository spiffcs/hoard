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
const SchemaVersion = "1.1.5"

// Kind names which payload a document carries; exactly the one field of the
// same name is present.
type Kind string

const (
	KindSummary  Kind = "summary"
	KindHoldings Kind = "holdings"
	KindUnpriced Kind = "unpriced"
	KindMovers   Kind = "movers"
	KindMarket   Kind = "market"
	KindReport   Kind = "report"
	KindWatch    Kind = "watch"
	KindHoard    Kind = "hoard"
)

// Document is the envelope every hoard JSON emission shares: a schema version,
// a kind, and the one payload the kind names.
type Document struct {
	SchemaVersion string `json:"schemaVersion"`
	Kind          Kind   `json:"kind" jsonschema:"enum=summary,enum=holdings,enum=unpriced,enum=movers,enum=market,enum=report,enum=watch,enum=hoard"`

	Summary  *Summary    `json:"summary,omitempty"`
	Holdings *Holdings   `json:"holdings,omitempty"`
	Unpriced *Unpriced   `json:"unpriced,omitempty"`
	Movers   *Movers     `json:"movers,omitempty"`
	Market   *Market     `json:"market,omitempty"`
	Report   *Report     `json:"report,omitempty"`
	Watch    *WatchCheck `json:"watch,omitempty"`
	Hoard    *Hoard      `json:"hoard,omitempty"`
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
	// Condition is the copy's wear — "nm", "lp", "mp", "hp", "dmg" — absent
	// when nobody has assessed it, which is most holdings. It describes the
	// copies held rather than the printing, so two entries of one printing can
	// differ by it.
	//
	// It never affects a value in these documents: no source hoard reads
	// publishes a per-condition price, so every copy is valued at the
	// printing's price whatever its condition.
	Condition string `json:"condition,omitempty"`
	// Lang is Scryfall's language code for the printing ("en", "ja", "zhs"),
	// absent when hoard has not stored the card's document. Language is part
	// of a printing's identity — Scryfall mints a distinct id per language —
	// so this names which of them ScryfallID refers to rather than adding a
	// dimension to it.
	Lang string `json:"lang,omitempty"`
	// ColorIdentity is Scryfall's color_identity: WUBRG letters, empty for a
	// colorless card, absent when the identity is not known to hoard (the
	// card's document was never fetched, or the emitting query predates it).
	ColorIdentity []string `json:"colorIdentity,omitempty"`
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
type Market struct {
	// ComparedPrintings is how many owned printings had two or more vendors,
	// so a consumer knows how much ground the analysis covered.
	ComparedPrintings int           `json:"comparedPrintings"`
	Opportunities     []Opportunity `json:"opportunities"`
	// Comps is every compared printing's per-vendor sheet, ordered by
	// valueUsd descending — the data behind the COMPS table.
	Comps []Comp `json:"comps"`
}

// Comp is one owned printing's comp sheet: each vendor's USD figure for
// the owned finish. Vendor fields are present only when that vendor
// quoted the card; spread — (lowUsd − buylistUsd) / lowUsd, a fraction —
// is present only when a buylist bid exists.
type Comp struct {
	Card     Card    `json:"card"`
	Copies   int     `json:"copies"`
	ValueUsd float64 `json:"valueUsd"`

	// MarketUsd is tcgplayer's sales-derived market price.
	MarketUsd      *float64 `json:"marketUsd,omitempty"`
	CardKingdomUsd *float64 `json:"cardKingdomUsd,omitempty"`
	ManapoolUsd    *float64 `json:"manapoolUsd,omitempty"`
	LowUsd         float64  `json:"lowUsd"`
	LowFrom        string   `json:"lowFrom"`
	BuylistUsd     *float64 `json:"buylistUsd,omitempty"`
	BuylistTo      string   `json:"buylistTo,omitempty"`
	Spread         *float64 `json:"spread,omitempty"`
}

// Opportunity is one owned printing-and-finish with the vendor quotes that
// make it interesting, tagged with the question it answers: "arbitrage" (a
// shop pays more than the cheapest retail), "liquid" (buylist close to
// retail), or "spread" (vendors disagree). A card answering several questions
// appears once per question.
//
// The sell-side fields are present only when some buylist quoted the card.
// Spread and Liquidity are fractions, not percentages: a spread of 0.5 means
// each row carries the tcgplayer sales-derived market price as its anchor.
type Opportunity struct {
	Card     Card    `json:"card"`
	Copies   int     `json:"copies"`
	ValueUsd float64 `json:"valueUsd"`
	Kind     string  `json:"kind" jsonschema:"enum=arbitrage,enum=liquid,enum=below-market"`

	BuyUsd  float64 `json:"buyUsd"`
	BuyFrom string  `json:"buyFrom"`
	// MarketUsd is tcgplayer's market price — computed from actual sales —
	// when the printing has one; every ranking anchors on it.
	MarketUsd *float64 `json:"marketUsd,omitempty"`
	// BelowMarket is the fraction the cheapest ask sits under marketUsd.
	BelowMarket *float64 `json:"belowMarket,omitempty"`
	SellUsd     *float64 `json:"sellUsd,omitempty"`
	SellTo      string   `json:"sellTo,omitempty"`
	// ProfitUsd is sellUsd − marketUsd per copy: what a buylist pays over
	// the last-sold price. Positive is genuine arbitrage.
	ProfitUsd *float64 `json:"profitUsd,omitempty"`
	// Liquidity is the fraction of marketUsd a shop will pay.
	Liquidity *float64 `json:"liquidity,omitempty"`
}

// Report is the dated valuation: the totals, each binder, the most valuable
// holdings, and where every price came from. TopHoldings is the display's
// top-N — the full itemized list is the holdings document's job.
type Report struct {
	// AsOf is when the prices were observed (RFC 3339); absent when nothing
	// has ever been priced.
	AsOf        string          `json:"asOf,omitempty"`
	Total       GrandTotal      `json:"total"`
	Binder      Totals          `json:"binder"`
	Decks       DeckAggregate   `json:"decks"`
	Binders     []DeckTotals    `json:"binders"`
	TopHoldings []ReportHolding `json:"topHoldings"`
	Sources     []SourceCount   `json:"sources"`
	Unpriced    UnpricedCount   `json:"unpriced"`
}

// DeckAggregate rolls every deck into one line, as the report's totals show
// them; the per-deck split lives in the summary document.
type DeckAggregate struct {
	Count       int     `json:"count"`
	TotalCopies int     `json:"totalCopies"`
	ValueUsd    float64 `json:"valueUsd"`
}

// ReportHolding is one itemized holding: the per-copy price and what the
// copies are worth together. PriceUsd is absent when no source prices the
// card — its copies are in the totals at $0.00.
type ReportHolding struct {
	Card     Card     `json:"card"`
	Copies   int      `json:"copies"`
	PriceUsd *float64 `json:"priceUsd,omitempty"`
	ValueUsd float64  `json:"valueUsd"`
}

// SourceCount is one price source's coverage: distinct printing-finish
// combinations and the physical copies they account for. Source is
// "scryfall" or a fallback vendor's name.
type SourceCount struct {
	Source    string `json:"source"`
	Printings int    `json:"printings"`
	Copies    int    `json:"copies"`
}

// UnpricedCount is the residue no source covers, counted the same way as
// SourceCount.
type UnpricedCount struct {
	Printings int `json:"printings"`
	Copies    int `json:"copies"`
}

// WatchCheck is one pass over the price watches: how many could be evaluated
// (an unpriced card answers neither "under" nor "over" and is not counted),
// and the watches that just crossed into their condition. A crossing fires
// once; a threshold merely still-met fires nothing.
type WatchCheck struct {
	Checked int          `json:"checked"`
	Fired   []FiredWatch `json:"fired"`
}

// FiredWatch is one alert: the card, the threshold it crossed, and the price
// that crossed it.
type FiredWatch struct {
	Card         Card    `json:"card"`
	Op           string  `json:"op" jsonschema:"enum=under,enum=over"`
	ThresholdUsd float64 `json:"thresholdUsd"`
	PriceUsd     float64 `json:"priceUsd"`
}

// Hoard is a whole hoard as one interchange document — the payload `hoard
// merge` moves between two databases. Where the holdings document answers
// "what do you own", this one answers "what is in that database", and carries
// the printing catalog alongside the holdings so the receiving hoard needs no
// network to accept it.
//
// It is deliberately not the whole SQLite file. Price and bid history, alt
// prices, gap records, finish guesses and settings are omitted; so are
// value_snapshots, which are one hoard's dated totals and cannot be combined
// with another's, and the import ledger, which records what a *particular*
// database has ingested.
// A hoard document is content and nothing else: it names neither the file it
// was read from nor the moment it was taken. That is deliberate. `hoard merge`
// identifies a merge by hashing these bytes and refuses a repeat, because
// holdings accumulate and a second merge would double every quantity — so a
// path or a timestamp in here would change the hash on every run and quietly
// disable the guard. Provenance belongs to the receipt, which records the file
// and the date it was applied.
type Hoard struct {
	// DatabaseVersion is the source database's SQLite user_version — hoard's
	// storage schema, which versions independently of this document's
	// schemaVersion.
	DatabaseVersion int         `json:"databaseVersion"`
	Printings       []Printing  `json:"printings"`
	Containers      []Container `json:"containers"`
	Holdings        Holdings    `json:"holdings"`
	Watches         []Watch     `json:"watches"`
}

// Printing is one row of the card catalog in full, so a hoard receiving this
// document can accept a card it has never seen without asking Scryfall.
//
// Raw is the card's Scryfall document verbatim. It is the field that makes the
// catalog complete rather than merely sufficient: hoard derives rarity, type
// line, oracle text, mana cost, artist, art URI and color identity from it as
// generated columns, so a printing that arrives without one is held, priced
// and counted correctly but reads as blank everywhere those appear.
type Printing struct {
	ScryfallID  string `json:"scryfallId"`
	Name        string `json:"name"`
	SetCode     string `json:"setCode"`
	Number      string `json:"number"`
	ScryfallURL string `json:"scryfallUrl"`
	// UpdatedAt is when this row was last refreshed (RFC 3339). A merge keeps
	// whichever side's row is newer, so a stale database cannot overwrite
	// fresher prices in the one receiving it.
	UpdatedAt   string `json:"updatedAt"`
	MTGJSONUUID string `json:"mtgjsonUuid,omitempty"`
	// Prices are per copy in the named finish, absent when no source prices
	// that finish — absent is not free.
	PriceUsd       *float64        `json:"priceUsd,omitempty"`
	PriceUsdFoil   *float64        `json:"priceUsdFoil,omitempty"`
	PriceUsdEtched *float64        `json:"priceUsdEtched,omitempty"`
	Raw            json.RawMessage `json:"raw,omitempty" jsonschema:"type=object"`
}

// Container is one binder or deck, carrying the identity a flat holdings row
// cannot. Binders are identified by name; decks are identified by the
// (source, sourceId) pair of the site they were imported from, which is what
// makes the same decklist in two hoards recognizable as one deck.
type Container struct {
	Name string `json:"name"`
	Kind string `json:"kind" jsonschema:"enum=binder,enum=deck"`
	// Source is the provider a container came from — "manual" for a binder,
	// the site's slug for a deck.
	Source    string `json:"source,omitempty"`
	SourceID  string `json:"sourceId,omitempty"`
	SourceURL string `json:"sourceUrl,omitempty"`
	Format    string `json:"format,omitempty"`
}

// Watch is one standing price alert: a threshold on one printing and finish,
// in one direction.
//
// It carries no last-fired state. A watch arriving in a hoard that has never
// alerted on it is new there, and hoard's rule is that a threshold already met
// is worth exactly one alert — so a merged watch evaluates fresh and may fire
// on the receiving hoard's next check.
type Watch struct {
	Card      Card    `json:"card"`
	Op        string  `json:"op" jsonschema:"enum=under,enum=over"`
	Threshold float64 `json:"threshold"`
	// Display is the name the alert prints, kept as the source hoard wrote it.
	Display string `json:"display"`
	// CreatedAt is when the watch was first set (RFC 3339).
	CreatedAt string `json:"createdAt,omitempty"`
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
