package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/spiffcs/hoard/internal/finish"
)

const (
	KindCollection = "collection"
	KindFolder     = "folder"
	KindDeck       = "deck"
)

const LooseName = "Binder"

var ReservedBinderNames = []string{LooseName, "Collection"}

func IsReservedBinderName(name string) bool {
	for _, r := range ReservedBinderNames {
		if strings.EqualFold(strings.TrimSpace(name), r) {
			return true
		}
	}
	return false
}

const collectionSourceID = "__collection__"

type Card struct {
	ScryfallID string

	MTGJSONUUID     string
	SetCode         string
	CollectorNumber string
	Name            string
	PriceUSD        *float64
	PriceUSDFoil    *float64

	PriceUSDEtched *float64
	ScryfallURL    string
	UpdatedAt      string

	AltSource string

	ColorIdentity []string

	ManaCost *string

	Lang string

	Treatment string
}

type Entry struct {
	ScryfallID string
	Finish     finish.Finish
	Condition  string
	Board      string
	Quantity   int

	PurchasePrice *float64
}

type Container struct {
	ID        int64
	Kind      string
	Name      string
	Source    string
	SourceID  string
	SourceURL string
	Format    string
	ParentID  int64
}

type DeckMeta struct {
	Name      string
	Source    string
	SourceID  string
	SourceURL string
	Format    string
}

type EntryView struct {
	Card      Card
	Finish    finish.Finish
	Condition string
	Board     string
	Quantity  int

	PurchasePrice *float64
}

func (e EntryView) Price() *float64 {
	return e.Finish.EffectivePrice(e.Card.PriceUSD, e.Card.PriceUSDFoil, e.Card.PriceUSDEtched)
}

func (e EntryView) Value() float64 {
	if p := e.Price(); p != nil {
		return float64(e.Quantity) * *p
	}
	return 0
}

type DeckSummary struct {
	Container
	DistinctCards int
	TotalCopies   int
	Value         float64

	IsDefault bool
	Counted   bool
}

const (
	altJoinCards = `LEFT JOIN card_prices_alt a ON a.scryfall_id = c.scryfall_id
    LEFT JOIN card_price_overrides o ON o.scryfall_id = c.scryfall_id`
	altJoinEntries = `LEFT JOIN card_prices_alt a ON a.scryfall_id = e.scryfall_id
    LEFT JOIN card_price_overrides o ON o.scryfall_id = e.scryfall_id`

	effPriceUSD    = `COALESCE(o.price_usd, ` + catalogPriceUSD + `)`
	effPriceFoil   = `COALESCE(o.price_usd_foil, ` + catalogPriceFoil + `)`
	effPriceEtched = `COALESCE(o.price_usd_etched, c.price_usd_etched,
        o.price_usd_foil, c.price_usd_foil, a.price_usd_foil)`

	catalogPriceUSD    = `COALESCE(c.price_usd, a.price_usd)`
	catalogPriceFoil   = `COALESCE(c.price_usd_foil, a.price_usd_foil)`
	catalogPriceEtched = `COALESCE(c.price_usd_etched, c.price_usd_foil, a.price_usd_foil)`

	altSourceExpr = `COALESCE(CASE
        WHEN o.price_usd IS NOT NULL OR o.price_usd_foil IS NOT NULL
             OR o.price_usd_etched IS NOT NULL THEN o.source
        WHEN c.price_usd IS NULL AND a.price_usd IS NOT NULL THEN a.source_usd
        WHEN c.price_usd_foil IS NULL AND a.price_usd_foil IS NOT NULL THEN a.source_usd_foil
    END, '')`

	altSourceForEntry = `COALESCE(CASE
        WHEN e.finish = 'etched' THEN
            CASE WHEN o.price_usd_etched IS NOT NULL THEN o.source
                 WHEN c.price_usd_etched IS NOT NULL THEN NULL
                 WHEN o.price_usd_foil IS NOT NULL THEN o.source
                 WHEN c.price_usd_foil IS NULL THEN a.source_usd_foil END
        WHEN e.finish = 'foil' THEN
            CASE WHEN o.price_usd_foil IS NOT NULL THEN o.source
                 WHEN c.price_usd_foil IS NULL THEN a.source_usd_foil END
        WHEN e.finish = 'nonfoil' THEN
            CASE WHEN o.price_usd IS NOT NULL THEN o.source
                 WHEN c.price_usd IS NULL THEN a.source_usd END
    END, '')`

	entryValue = `COALESCE(CASE
        WHEN e.finish = 'etched' THEN ` + effPriceEtched + `
        WHEN e.finish = 'foil'    THEN ` + effPriceFoil + `
        WHEN e.finish = 'nonfoil' THEN ` + effPriceUSD + ` END, 0)`

	unpricedPredicate = `(e.finish = 'etched'
         AND o.price_usd_etched IS NULL AND c.price_usd_etched IS NULL
         AND o.price_usd_foil IS NULL AND c.price_usd_foil IS NULL
         AND a.price_usd_foil IS NULL)
   OR (e.finish = 'foil'
         AND o.price_usd_foil IS NULL AND c.price_usd_foil IS NULL
         AND a.price_usd_foil IS NULL)
   OR (e.finish = 'nonfoil'
         AND o.price_usd IS NULL AND c.price_usd IS NULL AND a.price_usd IS NULL)`

	enrichedExpr = `c.raw_json IS NOT NULL`
)

func cardCols(altSource string) string {
	return `c.scryfall_id, COALESCE(c.mtgjson_uuid, ''), c.set_code, c.collector_number, c.name,
       ` + effPriceUSD + `, ` + effPriceFoil + `, ` + effPriceEtched + `,
       c.scryfall_url, c.updated_at,
       ` + altSource + `, c.color_identity, c.mana_cost, c.promo_types,
       COALESCE(c.lang, '')`
}

type cardAux struct {
	colorIdentity sql.NullString
	promoTypes    sql.NullString
}

func (a cardAux) apply(c *Card) {
	c.ColorIdentity = parseColorIdentity(a.colorIdentity)
	c.Treatment = FoilTreatment(a.promoTypes)
}

func cardScanDest(c *Card, aux *cardAux) []any {
	return []any{&c.ScryfallID, &c.MTGJSONUUID, &c.SetCode, &c.CollectorNumber, &c.Name,
		&c.PriceUSD, &c.PriceUSDFoil, &c.PriceUSDEtched, &c.ScryfallURL, &c.UpdatedAt,
		&c.AltSource, &aux.colorIdentity, &c.ManaCost, &aux.promoTypes, &c.Lang}
}

func parseColorIdentity(v sql.NullString) []string {
	if !v.Valid || len(v.String) < 2 {
		return nil
	}
	out := []string{}
	for _, r := range v.String {
		if r >= 'A' && r <= 'Z' {
			out = append(out, string(r))
		}
	}
	return out
}

var foilTreatmentWords = map[string]string{

	"textured":        "textured",
	"embossed":        "embossed",
	"gilded":          "gilded",
	"neonink":         "neon",
	"oilslick":        "oilslick",
	"glossy":          "glossy",
	"doublerainbow":   "dbl rainbow",
	"doubleexposure":  "dbl exposure",
	"invisibleink":    "invis ink",
	"stepandcompleat": "compleat",

	"firstplacefoil":   "1st place",
	"chocobotrackfoil": "chocobo",
}

func foilTreatmentOf(tag string) string {
	if word, ok := foilTreatmentWords[tag]; ok {
		return word
	}
	if len(tag) > len("foil") && strings.HasSuffix(tag, "foil") {
		return strings.TrimSuffix(tag, "foil")
	}
	return ""
}

func FoilTreatment(promoTypes sql.NullString) string {
	if !promoTypes.Valid || len(promoTypes.String) < 2 {
		return ""
	}
	return FoilTreatmentOf(strings.Split(strings.Trim(promoTypes.String, "[]"), ","))
}

func FoilTreatmentOf(promoTypes []string) string {
	for _, tag := range promoTypes {
		tag = strings.Trim(strings.TrimSpace(tag), `"`)
		if word := foilTreatmentOf(tag); word != "" {
			return word
		}
	}
	return ""
}

type Store struct {
	db     *sql.DB
	reader *sql.DB

	path string
	file os.FileInfo
	lock *buildLock
}

const (
	writerPragmas = "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)" +
		"&_pragma=cache_size(-65536)&_pragma=temp_store(2)" +
		"&_pragma=mmap_size(268435456)&_txlock=immediate"

	readerPragmas = "?_pragma=query_only(1)&_pragma=busy_timeout(5000)" +
		"&_pragma=cache_size(-65536)&_pragma=temp_store(2)" +
		"&_pragma=mmap_size(268435456)"
)

func Open(path string) (*Store, error) { return open(path, false) }

func OpenExclusive(path string) (*Store, error) { return open(path, true) }

func open(path string, exclusive bool) (*Store, error) {

	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating database directory %q: %w", dir, err)
		}
	}

	lock, err := lockDatabase(path, exclusive)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path+writerPragmas)
	if err != nil {
		_ = lock.release()
		return nil, fmt.Errorf("opening database %q: %w", path, err)
	}

	db.SetMaxOpenConns(1)

	s := &Store{db: db, path: path, lock: lock}
	if err := s.migrate(path); err != nil {
		db.Close()
		_ = lock.release()
		return nil, err
	}
	if _, err := s.collectionID(); err != nil {
		db.Close()
		_ = lock.release()
		return nil, err
	}

	reader, err := openReaders(path)
	if err != nil {
		db.Close()
		_ = lock.release()
		return nil, err
	}
	s.reader = reader
	if lockable(path) {
		if info, err := os.Stat(path); err == nil {
			s.file = info
		}
	}
	return s, nil
}

const backgroundReaders = 4

func openReaders(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+readerPragmas)
	if err != nil {
		return nil, fmt.Errorf("opening database %q for reading: %w", path, err)
	}
	db.SetMaxOpenConns(backgroundReaders)
	return db, nil
}

func (s *Store) reads() *sql.DB {
	if s.reader != nil {
		return s.reader
	}
	return s.db
}

func (s *Store) Close() error {
	err := s.db.Close()
	if s.reader != nil {
		if rerr := s.reader.Close(); err == nil {
			err = rerr
		}
	}
	if rerr := s.lock.release(); err == nil {
		err = rerr
	}
	return err
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func (s *Store) collectionID() (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM containers WHERE source='manual' AND source_id=?`,
		collectionSourceID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	ts := now()
	res, err := s.db.Exec(`
INSERT INTO containers (kind, name, source, source_id, created_at, updated_at)
VALUES (?, ?, 'manual', ?, ?, ?)`,
		KindCollection, LooseName, collectionSourceID, ts, ts)
	if err != nil {
		return 0, fmt.Errorf("creating collection container: %w", err)
	}
	return res.LastInsertId()
}
