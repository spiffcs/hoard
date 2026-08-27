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
//
// 1.0.0 is the first released version. The versions this model passed through
// before it were pre-release churn in a private repository — no document
// hoard emitted them into anyone's hands, and the $id every one of them names
// has never resolved — so the published schema starts from one file rather
// than inheriting a changelog no consumer could have read. MODEL stays at 1
// because it is the compatibility kill switch read.go enforces, and nothing
// about a first release is a break.
//
// 1.0.1 adds percent watches: a watch can now name a movement rather than a
// price, so Watch and FiredWatch carry percent, the anchor it is measured
// from, and the movement observed. Every added field is optional and no
// existing field changes meaning, so a 1.0.0 consumer reading a 1.0.1 document
// sees exactly what it saw before.
//
// The one widening is op's enum, from two values to four, and it is still an
// ADDITION on the emit side: a consumer switching on op with no drop case was
// already obliged to handle a value it did not know, and reading an enum
// widening as a REVISION would make every future Kind addition a REVISION too,
// which the Kind enum's own history contradicts.
//
// 1.1.0 adds oldAsOf to a movers change, and it is a REVISION rather than an
// ADDITION because oldUsd changed meaning underneath it. A movers window is now
// a range to measure across rather than a bar a printing has to clear: where a
// printing's record begins inside the window it is reported, measured from its
// own first price, instead of being dropped for having no price at the cutoff.
// So a 1.0.1 consumer reading a 1.1.0 document does not see what it saw before
// — it sees more changes, and some of them span less time than since implies.
// oldAsOf is what makes that legible, and it is required rather than optional
// because every change carries one; a consumer that reads it can date every
// figure it is given, and one that ignores it is the consumer this bump exists
// to warn.
//
// MODEL stays at 1: no field is removed or retyped, and read.go's compatibility
// gate is about being able to parse the document, which is untouched.
//
// 1.1.1 adds three kinds and one field, every one of them purely additive, so
// a 1.1.0 consumer reading a 1.1.1 document sees exactly what it saw before.
//
// The kinds are watches, binders and guessed, and they close one shape of gap.
// Each was previously drawn only as a table, while the rest of the CLI takes
// the ids in those tables as arguments: `watch rm`, `--binder` and `--checked`
// are all typed against values a caller could otherwise reach only by parsing
// columns. watches carries the standing state of every watch, which is the half
// of the story a check cannot tell — firing is edge triggered, so a supervisor
// reading exit codes alone cannot tell a threshold that never crossed from one
// that crossed and was already reported.
//
// The field is a movers change's pctChange, which the text view has always
// shown and the document has always omitted.
//
// It is a bump rather than a widening of 1.1.0 because 1.1.0 is already
// committed on main: any document a build from HEAD has emitted carries that
// number, and a version meaning one thing yesterday and another today is the
// one thing a version must never be.
//
// 1.1.2 adds the refused kind, purely additive on the same terms as 1.1.1 and
// bumped for the same reason: 1.1.1 is committed on main.
//
// refused lists the prices hoard declined to report and the figure it used
// instead, and it exists because the holdings document alone cannot say so. A
// corrected price appears there as an ordinary priceUsd, indistinguishable
// from one the catalog supplied — which is precisely the confusion a
// correction must not create. A consumer totalling holdings can now ask which
// of its figures hoard substituted, and what each one replaced.
//
// 1.1.3 adds settling to a movers change, optional and absent where the row
// counts normally, so a 1.1.2 consumer reading a 1.1.3 document sees exactly
// what it saw before. Bumped rather than folded into 1.1.2 for the reason
// given above: 1.1.2 is committed on main.
//
// The field marks a row whose set is too new for its price to mean anything —
// a market price averages completed sales, and a freshly released set has an
// average over none. hoard's own net holds those rows out, so without the
// field a consumer summing impactUsd would reach a number hoard never prints
// and have no way to see why. The rows stay in the document: they are real
// movement, and which totals they belong in is the consumer's call to make
// with the same fact hoard used.
// 1.1.4 adds containerId to a holdings row, purely additive: a 1.1.3 consumer
// reading a 1.1.4 document sees exactly what it saw before. Bumped rather than
// folded into 1.1.3 for the reason given above: 1.1.3 is committed on main.
//
// The field closes the same gap the binders kind closed, one layer down. A
// holdings row named its container only by the pair (container, containerKind),
// which is enough to print and not enough to address: a binder can be renamed
// between a document being written and being read, and two containers of
// different kinds can share a name. So a tool could report on rows it found
// here but could not hand them back and be sure of meaning the same holdings.
// With the id it can, which is what makes a holdings document a work list
// rather than only a report.
//
// The id belongs to the database that emitted the document and to no other.
// hoard merge, which reads a hoard document written by a different database,
// goes on resolving containers by name, because an id minted there names a
// different container here or none at all.
const SchemaVersion = "1.2.0"

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
	KindWatches  Kind = "watches"
	KindHoard    Kind = "hoard"
	KindBinders  Kind = "binders"
	KindGuessed  Kind = "guessed"
	KindRefused  Kind = "refused"
)

// Document is the envelope every hoard JSON emission shares: a schema version,
// a kind, and the one payload the kind names.
type Document struct {
	SchemaVersion string `json:"schemaVersion"`
	Kind          Kind   `json:"kind" jsonschema:"enum=summary,enum=holdings,enum=unpriced,enum=movers,enum=market,enum=report,enum=watch,enum=hoard,enum=watches,enum=binders,enum=guessed,enum=refused"`

	Summary  *Summary    `json:"summary,omitempty"`
	Holdings *Holdings   `json:"holdings,omitempty"`
	Unpriced *Unpriced   `json:"unpriced,omitempty"`
	Movers   *Movers     `json:"movers,omitempty"`
	Market   *Market     `json:"market,omitempty"`
	Report   *Report     `json:"report,omitempty"`
	Watch    *WatchCheck `json:"watch,omitempty"`
	Hoard    *Hoard      `json:"hoard,omitempty"`
	Watches  *Watches    `json:"watches,omitempty"`
	Binders  *Binders    `json:"binders,omitempty"`
	Guessed  *Guessed    `json:"guessed,omitempty"`
	Refused  *Refused    `json:"refused,omitempty"`
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
	ColorIdentity *[]string `json:"colorIdentity,omitempty"` // pointer: see jsonIdentity
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
	Card Card `json:"card"`
	// Detail is what the printing's Scryfall document says the card is —
	// rarity, type line, mana value, oracle text and the rest — enough to
	// compute a curve, a rarity breakdown or a text search without fetching
	// anything. It is absent when hoard has stored no document for the
	// printing; a field inside it is absent when the card has no such value,
	// so an artifact carries no power and an English printing no printedName.
	//
	// A hoard document never carries it: every printing there embeds its
	// Scryfall document verbatim in printings[].raw, which is strictly more
	// than this, and adding derived copies would move the content hash `hoard
	// merge` uses to refuse a source it has already applied.
	Detail *CardDetail `json:"detail,omitempty"`

	Count int `json:"count"`
	// ContainerID is the container's row id in the database that emitted this
	// document, and it is what makes a row addressable: a name can be renamed
	// between a document being written and being read, an id cannot. A tool
	// that selects rows here can hand them back to hoard and mean exactly the
	// holdings it saw.
	//
	// It is meaningful only in that database. A document read into a different
	// hoard resolves containers by name instead, because an id minted elsewhere
	// names a different container here, or none at all.
	ContainerID   int64    `json:"containerId"`
	Container     string   `json:"container"`
	ContainerKind string   `json:"containerKind" jsonschema:"enum=binder,enum=deck"`
	Board         string   `json:"board"`
	PriceUsd      *float64 `json:"priceUsd,omitempty"`
	// Paid is what the holder recorded paying per copy, in USD, absent when
	// nobody recorded it — which is most holdings, and absent is not free.
	// Like Condition it describes the copies rather than the printing, so two
	// entries of one printing can differ by it, and hoard keeps them as
	// separate rows rather than averaging them away.
	Paid *float64 `json:"paid,omitempty"`
}

// CardDetail is one printing's card characteristics, every one of them derived
// from the Scryfall document hoard has stored for it.
//
// Only this first sentence reaches the schema: AddGoComments runs a type's
// comment through go/doc's Synopsis, so everything below is for a reader of
// this file. What a consumer must know is on Holding.Detail instead, which is
// a field and therefore carried whole.
//
// Everything here is a property of the printing, never of the copies held.
// That is also the line between this and Card beside it: Card carries the
// identifiers hoard itself stores and joins on — and Finish and Condition,
// which describe the copies — while these are what hoard reads back out of
// the printing's document. tcgplayerId sits here for exactly that reason,
// identifier though it is: TCGplayer mints its product ids per printing, not
// per finish, so beside the finish-exact ids in Card it would mislead.
//
// Coverage varies enormously — type line on everything, loyalty on the
// planeswalkers, printedName on nothing an English-language collection holds.
// That is the data and not a gap, which is why every field is omitempty: the
// cost of a field is proportional to how many cards actually have one.
type CardDetail struct {
	// Rarity is Scryfall's: common, uncommon, rare, mythic, special or bonus.
	Rarity string `json:"rarity,omitempty"`
	// TypeLine is the printed type line ("Legendary Creature — Elf Druid"), the front face's on a two-faced card.
	TypeLine string `json:"typeLine,omitempty"`
	// Cmc is the mana value. Zero is a real value — every land has it — so this is absent only when unknown.
	Cmc *float64 `json:"cmc,omitempty"`
	// ManaCost is the printed cost in Scryfall's brace notation ("{2}{W}{U}"), absent for a card with no cost.
	ManaCost string `json:"manaCost,omitempty"`
	// OracleText is the current rules text, newline-separated, with reminder text as Scryfall stores it.
	OracleText string `json:"oracleText,omitempty"`
	// Power is the creature's, as printed — text because the game prints "*" and "1+*" there.
	Power string `json:"power,omitempty"`
	// Toughness is the creature's, as printed, and text for the same reason as power.
	Toughness string `json:"toughness,omitempty"`
	// Loyalty is the planeswalker's starting loyalty, as printed.
	Loyalty string `json:"loyalty,omitempty"`
	// SetName is the set's full name ("Commander 2021"), so a by-set breakdown reads without a code table.
	SetName string `json:"setName,omitempty"`
	// ReleasedAt is the set's release date (YYYY-MM-DD), which sorts chronologically where a set code does not.
	ReleasedAt string `json:"releasedAt,omitempty"`
	// Artist is the illustration's credited artist, as Scryfall spells it.
	Artist string `json:"artist,omitempty"`
	// Layout is Scryfall's layout ("normal", "split", "transform", "modal_dfc"), which says how many faces to expect.
	Layout string `json:"layout,omitempty"`
	// FlavorText is the printed flavor text, absent on a printing with none.
	FlavorText string `json:"flavorText,omitempty"`
	// PromoTypes are Scryfall's variant tags ("surgefoil", "showcase"), absent on an ordinary printing.
	PromoTypes []string `json:"promoTypes,omitempty"`
	// PrintedName is the name in the card's own language and script, absent on an English printing.
	PrintedName string `json:"printedName,omitempty"`
	// TcgplayerID is TCGplayer's product id for the printing — a marketplace join key, and printing-level, so it does not distinguish finishes.
	TcgplayerID *int64 `json:"tcgplayerId,omitempty"`
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
	Card   Card    `json:"card"`
	Copies int     `json:"copies"`
	OldUsd float64 `json:"oldUsd"`
	NewUsd float64 `json:"newUsd"`
	// OldAsOf is when oldUsd was observed (RFC 3339), which is not always the
	// document's since: a printing whose record begins inside the window is
	// measured from its own earliest price, so this row spans from here rather
	// than from the window's start. Read a change against this, not against
	// since, or rows with younger records are misread as covering the whole
	// window.
	OldAsOf   string  `json:"oldAsOf"`
	ImpactUsd float64 `json:"impactUsd"`
	// PctChange is the move as a fraction of oldUsd, signed: 1.1458 is up
	// 114.58%. It is derivable from oldUsd and newUsd and is here for the same
	// reason impactUsd beside it is — the text view prints it, so a consumer
	// reading the document was reproducing a figure hoard had already computed.
	//
	// It is absent, not zero, when oldUsd is 0: a rise from nothing is an
	// infinite percentage rather than a large one, and the whole point of
	// carrying the field is that hoard decides that once instead of every
	// caller deciding it differently. Absent is the model's word for "no such
	// value" throughout — priceUsd, spread and belowMarket all use it — and a
	// zero here would be indistinguishable from a printing that did not move.
	PctChange *float64 `json:"pctChange,omitempty"`
	// Source names where the new price came from: "scryfall", or the vendor
	// behind a fallback.
	Source string `json:"source"`
	// Settling marks a row whose set is too new for its prices to carry
	// information — a market price averages completed sales, and a set with
	// none has an average over nothing. hoard's own totals hold these rows
	// out; the field is here so a consumer summing impactUsd can reach the
	// same figure instead of a different one.
	//
	// Absent rather than false when the row counts normally, which keeps it
	// optional in the emitted schema — schemagen makes a field without
	// omitempty required, and every document already written lacks it.
	Settling bool `json:"settling,omitempty"`
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

// FiredWatch is one alert.
//
// An absolute watch crossed a line and reports it. A percent watch reports a
// movement, which a threshold cannot express — AnchorUsd is the price the move
// is measured from and AnchorAt is when that price was observed, so a reader
// can say "down 11% from its 3 July high" without going back to the history
// table to find out what the alert was even about.
type FiredWatch struct {
	Card         Card    `json:"card"`
	Op           string  `json:"op" jsonschema:"enum=under,enum=over,enum=drop,enum=rise"`
	ThresholdUsd float64 `json:"thresholdUsd,omitempty"`
	PriceUsd     float64 `json:"priceUsd"`
	// Percent is the movement the watch asks about, as a fraction.
	Percent   float64  `json:"percent,omitempty"`
	AnchorUsd *float64 `json:"anchorUsd,omitempty"`
	AnchorAt  string   `json:"anchorAt,omitempty"`
	// MovedPct is the movement actually observed, signed: -0.113 is down
	// 11.3%. It is derivable from PriceUsd and AnchorUsd and is here anyway,
	// because it is the entire content of the alert and a consumer should not
	// have to recompute the thing being reported to it.
	MovedPct float64 `json:"movedPct,omitempty"`
}

// Watches is every standing watch and where each one stands — the machine
// reading of `hoard watch list`.
//
// It is a separate kind from watch rather than a field on it because the two
// answer different questions at different moments. A watch document is the
// result of one check: what crossed, just now, and nothing else. This is the
// standing state, readable at any time and without side effects — no crossing
// is consumed by reading it.
//
// That distinction is what makes the pair usable from a cron. A watch fires
// once per crossing and then holds; an agent that misses one exit 3 has lost
// the event, and only this document can tell it that a threshold is still met
// and was merely already reported.
type Watches struct {
	Rows []WatchRow `json:"rows"`
}

// WatchRow is one standing watch: the rule, the price it is judged against,
// and where it stands.
//
// Op names both the rule and its units, as it does everywhere else: under and
// over are dollar lines and read thresholdUsd, drop and rise are movements and
// read percent, and exactly one of the two is meaningful.
type WatchRow struct {
	// ID is the handle `hoard watch rm` takes. The text table deliberately has
	// no ID column — an ambiguous name fragment prints the ids at the moment
	// they are needed, which is better targeted than a column on every row —
	// but that reasoning is about a terminal's width budget and does not reach
	// a document, where the id is the only unambiguous way to name a row.
	ID   int64  `json:"id"`
	Card Card   `json:"card"`
	Op   string `json:"op" jsonschema:"enum=under,enum=over,enum=drop,enum=rise"`
	// Display is the name the alert prints, as resolved when the watch was set.
	Display      string  `json:"display"`
	ThresholdUsd float64 `json:"thresholdUsd,omitempty"`
	// Percent is the movement that fires the alert, as a fraction: 0.1 is ten
	// percent, matching Watch.Percent rather than the flag's "10%".
	Percent    float64 `json:"percent,omitempty"`
	MinMoveUsd float64 `json:"minMoveUsd,omitempty"`
	SinceDays  int     `json:"sinceDays,omitempty"`
	// PriceUsd is hoard's effective price for the watched finish, absent when
	// no source prices it — absent is not free, and a watch with no price
	// answers none of the four questions.
	PriceUsd *float64 `json:"priceUsd,omitempty"`
	// AnchorUsd is the extreme a movement is measured from and AnchorAt when it
	// was observed; both absent for an absolute watch, and for a movement whose
	// series has no observation to anchor on.
	AnchorUsd *float64 `json:"anchorUsd,omitempty"`
	AnchorAt  string   `json:"anchorAt,omitempty"`
	// State is where the watch stands right now: "met" when the condition
	// holds, "waiting" when it does not, "waiting-on-history" for a movement
	// whose record does not reach back as far as its own window — which can
	// never fire until it does — and "unpriced" when nothing prices the card.
	State string `json:"state" jsonschema:"enum=unpriced,enum=waiting,enum=waiting-on-history,enum=met"`
	// WouldFire reports whether the next `hoard watch` would alert on this row.
	//
	// It is not the same question as state == "met", and the difference is the
	// one thing about watches that surprises everybody: firing is edge
	// triggered, so a watch that crossed yesterday is still met today and will
	// say nothing. A supervisor loop reading exit codes alone cannot tell those
	// apart. This field can.
	WouldFire bool `json:"wouldFire"`
	// LastFiredAt is when the alert last went out (RFC 3339), absent on a watch
	// that has never fired. For a movement it is also the lower bound the next
	// anchor is drawn from — firing re-anchors — so it dates the baseline as
	// well as the alert.
	LastFiredAt string `json:"lastFiredAt,omitempty"`
	// CreatedAt is when the watch was set (RFC 3339). A movement cannot measure
	// a move that predates it, so this bounds the anchor too.
	CreatedAt string `json:"createdAt,omitempty"`
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

// Watch is one standing alert on one printing and finish, in one direction.
//
// Op names both the comparison and its units: under and over are dollar lines
// and read Threshold; drop and rise are movements and read Percent. Exactly
// one of the two is meaningful, which is why both are omitempty — a document
// carrying both would not say which one the alert obeys.
//
// Threshold gaining omitempty is a change to an existing field, and it is safe
// in exactly one direction: a threshold of zero is not a meaningful absolute
// watch, so omitempty can only ever elide a value that was already impossible.
//
// It carries no last-fired state. A watch arriving in a hoard that has never
// alerted on it is new there, and hoard's rule is that a condition already met
// is worth exactly one alert — so a merged watch evaluates fresh and may fire
// on the receiving hoard's next check. For a percent watch that is stronger
// than convenience: without the fire moment it anchors from the receiving
// hoard's own history, which is the only history it can honestly speak about.
type Watch struct {
	Card      Card    `json:"card"`
	Op        string  `json:"op" jsonschema:"enum=under,enum=over,enum=drop,enum=rise"`
	Threshold float64 `json:"threshold,omitempty"`
	// Percent is the movement that fires the alert, as a fraction: 0.1 is a
	// ten percent move. A fraction rather than 10, because the document is
	// read by scripts and a percent sign's worth of ambiguity in a number that
	// multiplies prices is not worth the readability.
	Percent    float64 `json:"percent,omitempty"`
	MinMoveUsd float64 `json:"minMoveUsd,omitempty"`
	SinceDays  int     `json:"sinceDays,omitempty"`
	// Display is the name the alert prints, kept as the source hoard wrote it.
	Display string `json:"display"`
	// CreatedAt is when the watch was first set (RFC 3339).
	CreatedAt string `json:"createdAt,omitempty"`
}

// Binders is every binder with its rolled-up counts and value, the default
// binder first and the rest by name — the order `hoard binder list` prints.
type Binders struct {
	Rows []Binder `json:"rows"`
}

// Binder is one binder, carrying the id the rest of the CLI takes as an
// argument. --binder on export, import and add accepts an id, a name or a
// unique fragment; the id is the only one of the three that cannot become
// ambiguous when a second binder is created, so a script that reads this
// document and passes ids back is the stable form.
//
// The counts are the same three the summary reports for a container:
// distinctCards is printings and totalCopies is physical cards, so a binder
// holding a card in two finishes counts once and twice respectively.
type Binder struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// IsDefault marks the built-in binder every database starts with — the one
	// `hoard binder rm` refuses. It is on the row rather than implied by
	// position so a consumer need not rely on the ordering to know which.
	IsDefault bool `json:"isDefault"`
	Totals
}

// Guessed is the audit queue the hands-free scanner's nonfoil default creates:
// scanned rows committed on a default rather than on evidence, newest first.
type Guessed struct {
	Rows []GuessedRow `json:"rows"`
}

// GuessedRow is one unchecked guess. ID is what `hoard guessed --checked` takes.
//
// Two rows can be identical in every field but ID, and that is the data rather
// than a duplicate to collapse: the queue is per commit, so two copies of one
// printing scanned on the same default are two cards to physically check and
// two rows to retire. A consumer that keys this list by card loses one of them.
//
// The card carries no colorIdentity or lang: the queue is read from the guess
// log joined to the catalog on identity alone, so those are unknown here in the
// model's sense of the word rather than absent from the printing.
type GuessedRow struct {
	ID   int64 `json:"id"`
	Card Card  `json:"card"`
	// GuessedAt is when the scanner committed the row, as the guess log stores
	// it — the column the text listing prints beside each row.
	GuessedAt string `json:"guessedAt"`
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

// Refused is the price-correction worklist: every figure hoard declined to
// report because the asks on the card's own TCGplayer listing contradicted it.
type Refused struct {
	Rows []RefusedRow `json:"rows"`
}

// RefusedRow is one correction in force.
//
// Both figures are carried, and refusedUsd is not optional, because a consumer
// reading only the price hoard settled on cannot tell a correction from an
// ordinary quote — which is the whole failure this surface exists to make
// visible.
type RefusedRow struct {
	Card Card `json:"card"`
	// Copies is how many are held in this finish.
	Copies int `json:"copies"`
	// PriceUsd is the figure now in use, and RefusedUsd the one it replaced.
	PriceUsd   float64 `json:"priceUsd"`
	RefusedUsd float64 `json:"refusedUsd"`
	// Source names where PriceUsd came from and Reason why the refusal
	// happened, both stable identifiers rather than prose.
	Source string `json:"source"`
	Reason string `json:"reason"`
	// AsOf is when the correction was recorded, RFC 3339.
	AsOf string `json:"asOf"`
}
