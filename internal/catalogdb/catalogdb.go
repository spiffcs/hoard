package catalogdb

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/boundedio"
	"github.com/spiffcs/hoard/internal/buildinfo"
	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

const defaultListingURL = "https://api.scryfall.com/bulk-data"

const bulkType = "default_cards"

const batchSize = 5000

const listingTimeout = 10 * time.Second

var knownRarities = []string{"common", "uncommon", "rare", "special", "mythic", "bonus"}

var httpClient = &http.Client{Transport: func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 30 * time.Second
	return t
}()}

type Options struct {
	Since      int
	Sets       []string
	Rarities   []string
	PricedOnly bool
	Days       int

	BulkListingURL string
	PriceBaseURL   string
	CacheDir       string
}

type Result struct {
	Printings int
	Entries   int
	Mapped    int

	Observations int
	Bids         int
}

func (o Options) Validate() error {
	_, err := newFilter(o)
	return err
}

func Build(ctx context.Context, st *store.Store, o Options, p progress.Fn) (Result, error) {
	var res Result

	cid, err := st.CollectionID()
	if err != nil {
		return res, err
	}

	f, err := newFilter(o)
	if err != nil {
		return res, err
	}
	if err := st.SetCatalogMode(false); err != nil {
		return res, err
	}

	res.Printings, res.Entries, err = seedPrintings(ctx, st, cid, o, f, p)
	if err != nil {
		return res, err
	}
	if res.Printings == 0 {
		return res, fmt.Errorf("no printings matched; nothing to price")
	}

	if res.Mapped, err = mapIdentifiers(ctx, st, o, p); err != nil {
		return res, err
	}

	days := o.Days
	if days <= 0 {
		days = 30
	}
	back, err := action.BackfillPrices(ctx, action.Deps{
		Store: st, CacheDir: o.CacheDir, PriceBaseURL: o.PriceBaseURL,
	}, p, days)
	if err != nil {
		return res, err
	}
	res.Observations, res.Bids = back.Inserted, back.BidInserted
	return res, st.SetCatalogMode(true)
}

type bulkCard struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Set             string   `json:"set"`
	SetName         string   `json:"set_name"`
	CollectorNumber string   `json:"collector_number"`
	ScryfallURI     string   `json:"scryfall_uri"`
	ReleasedAt      string   `json:"released_at"`
	Rarity          string   `json:"rarity"`
	Lang            string   `json:"lang"`
	Finishes        []string `json:"finishes"`
	Games           []string `json:"games"`
	Prices          struct {
		USD       string `json:"usd"`
		USDFoil   string `json:"usd_foil"`
		USDEtched string `json:"usd_etched"`
	} `json:"prices"`
}

func seedPrintings(ctx context.Context, st *store.Store, cid int64, o Options, f filter,
	p progress.Fn) (printings, entries int, err error) {

	entry, err := listing(ctx, o)
	if err != nil {
		return 0, 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.DownloadURI, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("downloading the card bundle: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("card bundle returned %d", resp.StatusCode)
	}

	cr := &countingReader{r: resp.Body}
	bc := &boundedio.Counter{R: cr}
	zr, err := gzip.NewReader(bc)
	if err != nil {
		return 0, 0, fmt.Errorf("decompressing the card bundle: %w", err)
	}
	defer zr.Close()
	bundle := boundedio.LimitRatio(zr, bc, "the card bundle")

	sc := bufio.NewScanner(bundle)
	sc.Buffer(make([]byte, 0, 256*1024), 4*1024*1024)

	batch := make([]store.CatalogPrinting, 0, batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		n, e, err := st.SeedCatalogPrintings(cid, batch)
		if err != nil {
			return err
		}
		printings += n
		entries += e
		batch = batch[:0]
		p.Emit(progress.Event{Step: "seeding printings",
			Done: int64(printings), Unit: progress.UnitCards})
		return nil
	}

	var seen int
	for sc.Scan() {
		if seen%2000 == 0 {
			if err := ctx.Err(); err != nil {
				return printings, entries, err
			}
			p.Emit(progress.Event{Step: "downloading catalog",
				Done: cr.n, Total: entry.CompressedSize, Unit: progress.UnitBytes})
		}
		seen++

		line := trimJSONLine(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var c bulkCard
		if err := json.Unmarshal(line, &c); err != nil {
			continue
		}
		if !f.keep(c) {
			continue
		}
		batch = append(batch, printing(c, line))
		if len(batch) == cap(batch) {
			if err := flush(); err != nil {
				return printings, entries, err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return printings, entries, fmt.Errorf("reading the card bundle: %w", err)
	}
	return printings, entries, flush()
}

func printing(c bulkCard, line []byte) store.CatalogPrinting {
	raw := make([]byte, len(line))
	copy(raw, line)

	usd := parsePrice(c.Prices.USD)
	foil := parsePrice(c.Prices.USDFoil)
	etched := parsePrice(c.Prices.USDEtched)

	card := scryfall.Card{
		ID:              c.ID,
		Name:            c.Name,
		Set:             c.Set,
		CollectorNumber: c.CollectorNumber,
		ScryfallURL:     c.ScryfallURI,
		SetName:         c.SetName,
		ReleasedAt:      c.ReleasedAt,
		Lang:            c.Lang,
		Finishes:        c.Finishes,
		PriceUSD:        usd,
		PriceUSDFoil:    scryfall.FoilPrice(foil, etched),
		PriceUSDEtched:  etched,
		Raw:             raw,
	}
	return store.CatalogPrinting{Card: card, Finishes: scryfall.Finishes(card)}
}

type filter struct {
	sets     map[string]bool
	rarities map[string]bool
	since    int
	priced   bool
}

func newFilter(o Options) (filter, error) {
	f := filter{sets: lowered(o.Sets), since: o.Since, priced: o.PricedOnly}

	for _, r := range o.Rarities {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" {
			continue
		}
		if !slices.Contains(knownRarities, r) {
			return filter{}, fmt.Errorf("unknown rarity %q; want one of %s",
				r, strings.Join(knownRarities, ", "))
		}
		if f.rarities == nil {
			f.rarities = map[string]bool{}
		}
		f.rarities[r] = true
	}
	return f, nil
}

func (f filter) keep(c bulkCard) bool {
	if c.ID == "" || !hasGame(c.Games, "paper") || len(c.Finishes) == 0 {
		return false
	}
	if len(f.sets) > 0 && !f.sets[strings.ToLower(c.Set)] {
		return false
	}
	if len(f.rarities) > 0 && !f.rarities[strings.ToLower(strings.TrimSpace(c.Rarity))] {
		return false
	}
	if f.since > 0 {
		if len(c.ReleasedAt) < 4 {
			return false
		}
		year, err := strconv.Atoi(c.ReleasedAt[:4])
		if err != nil || year < f.since {
			return false
		}
	}
	if f.priced &&
		c.Prices.USD == "" && c.Prices.USDFoil == "" && c.Prices.USDEtched == "" {
		return false
	}
	return true
}

func mapIdentifiers(ctx context.Context, st *store.Store, o Options, p progress.Fn) (int, error) {
	p.Emit(progress.Event{Step: "mapping card ids",
		Note: "fetching every set's identifiers from MTGJSON in one file"})

	ids, err := mtgjson.AllIdentifiers(ctx, mtgjson.Options{
		CacheDir: o.CacheDir,
		BaseURL:  o.PriceBaseURL,
		Progress: func(done, total int64) {
			p.Emit(progress.Event{Step: "mapping card ids",
				Done: done, Total: total, Unit: progress.UnitBytes})
		},
	})
	if err != nil {
		return 0, err
	}

	seeded, err := st.ActivePrintingIDs()
	if err != nil {
		return 0, err
	}

	uuids := make(map[string]string, len(seeded))
	links := make(map[string]store.CKLinks, len(seeded))
	alt := make(map[string]string, len(seeded))
	etched := make(map[string]string, len(seeded))
	vendor := make(map[string]store.VendorProductIDs, len(seeded))

	var mapped int
	for _, sid := range seeded {
		sc, ok := ids[sid]
		if ok {
			uuids[sid] = sc.UUID
			mapped++
		}
		links[sid] = store.CKLinks{URL: sc.CKURL, FoilURL: sc.CKFoilURL}
		alt[sid] = ""
		etched[sid] = ""
		vendor[sid] = store.VendorProductIDs{
			TCGProduct: sc.TCGProductID,
			CKFoil:     sc.CKFoilID,
			CKEtched:   sc.CKEtchedID,
		}
	}

	if err := st.SaveMTGJSONUUIDs(uuids); err != nil {
		return mapped, err
	}
	if err := st.SaveCardKingdomLinks(links); err != nil {
		return mapped, err
	}
	if err := st.SaveTCGAltProducts(alt, etched); err != nil {
		return mapped, err
	}
	if err := st.SaveVendorProductIDs(vendor); err != nil {
		return mapped, err
	}
	p.Emit(progress.Event{Step: "mapping card ids",
		Done: int64(mapped), Total: int64(len(seeded)), Unit: progress.UnitCards})
	return mapped, nil
}

type bundle struct {
	Type           string `json:"type"`
	UpdatedAt      string `json:"updated_at"`
	DownloadURI    string `json:"jsonl_download_uri"`
	CompressedSize int64  `json:"compressed_size"`
}

func listing(ctx context.Context, o Options) (bundle, error) {
	var zero bundle

	url := o.BulkListingURL
	if url == "" {
		url = defaultListingURL
	}

	ctx, cancel := context.WithTimeout(ctx, listingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return zero, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return zero, fmt.Errorf("reading the bulk-data listing: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("bulk-data listing returned %d", resp.StatusCode)
	}

	var l struct {
		Data []bundle `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&l); err != nil {
		return zero, fmt.Errorf("decoding the bulk-data listing: %w", err)
	}
	for _, e := range l.Data {
		if e.Type == bulkType {
			return e, nil
		}
	}
	return zero, fmt.Errorf("bulk-data listing has no %q bundle", bulkType)
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func lowered(sets []string) map[string]bool {
	if len(sets) == 0 {
		return nil
	}
	out := make(map[string]bool, len(sets))
	for _, s := range sets {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out[s] = true
		}
	}
	return out
}

func hasGame(games []string, want string) bool {
	for _, g := range games {
		if g == want {
			return true
		}
	}
	return false
}

func parsePrice(s string) *float64 {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

func trimJSONLine(b []byte) []byte {
	b = bytes.TrimSpace(b)
	b = bytes.TrimSuffix(b, []byte(","))
	if len(b) == 0 || b[0] != '{' {
		return nil
	}
	return b
}
