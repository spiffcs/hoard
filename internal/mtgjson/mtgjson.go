// Package mtgjson fetches card prices from MTGJSON, as a fallback for printings
// Scryfall cannot price.
//
// Scryfall's USD prices come from TCGplayer alone, so a printing TCGplayer has
// no record of is simply unpriced there. That is not rare: the Modern Horizons 3
// Commander ripple foils have no `usd_foil` at all, which leaves whole decks
// valued at zero. MTGJSON aggregates several vendors and prices those cards.
//
// This package deliberately reads only what that gap needs: today's USD paper
// retail prices, and the Scryfall-ID-to-MTGJSON-UUID map required to look them
// up. It is not a general MTGJSON client.
package mtgjson

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// userAgent identifies this tool to MTGJSON, matching the Scryfall client.
const userAgent = "hoard/0.1"

// apiBase is the MTGJSON v5 file root. It is a var (not const) so tests can
// point the client at a local httptest server.
var apiBase = "https://mtgjson.com/api/v5"

// providerOrder is the preference order for USD paper prices.
//
// TCGplayer first because it is the source Scryfall itself uses, so where it
// exists the number is consistent with every other price in the hoard. Card
// Kingdom and Manapool follow as genuine USD retailers that cover printings
// TCGplayer misses. Cardmarket is absent on purpose: it quotes EUR, and a
// second currency inside a USD total would be a lie.
var providerOrder = []string{"tcgplayer", "cardkingdom", "manapool"}

// Price is one card's USD paper prices and the vendor behind each.
//
// The two finishes carry separate sources because they are resolved
// independently: a shop that prices the non-foil printing often has no figure
// for the foil, so the two routinely come from different vendors.
type Price struct {
	UUID       string
	USD        *float64
	Foil       *float64
	USDSource  string
	FoilSource string
}

// httpClient is shared across requests. It carries no timeout of its own: the
// files here range from a sub-megabyte set file to the ~150 MB price archive,
// and any single deadline is either too tight for the archive on a slow link or
// too slack to be worth setting for the rest. Every call takes a context, so
// cancellation belongs to the caller, which knows what it asked for.
var httpClient = &http.Client{}

// CacheDir, when set, keeps downloaded files so repeated runs on the same day
// don't re-fetch them. This matters because a card no source can price stays a
// gap forever, and without a cache every single update-prices would re-download
// the whole bundle chasing it.
//
// MTGJSON rebuilds nightly, so entries are keyed by date and yesterday's are
// simply never read again.
var CacheDir string

// today is a var so tests can pin the cache key.
var today = func() string { return time.Now().Format("2006-01-02") }

// fetch returns the bytes of one MTGJSON file, from the cache when possible.
// The caller closes the returned reader.
func fetch(ctx context.Context, name string) (io.ReadCloser, error) {
	var cachePath string
	if CacheDir != "" {
		cachePath = filepath.Join(CacheDir, today()+"-"+name)
		if f, err := os.Open(cachePath); err == nil {
			return f, nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNoSuchSet
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("mtgjson %s returned %d", name, resp.StatusCode)
	}
	if cachePath == "" {
		return resp.Body, nil
	}

	// Write through to the cache, then serve from it. A partial download must
	// not be left behind looking complete, so write to a temp file and rename.
	defer resp.Body.Close()
	if err := os.MkdirAll(CacheDir, 0o755); err != nil {
		return resp.Body, nil //nolint:nilerr // caching is best-effort
	}
	pruneCache()
	tmp, err := os.CreateTemp(CacheDir, "dl-*")
	if err != nil {
		return io.NopCloser(resp.Body), nil //nolint:nilerr
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), cachePath); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	return os.Open(cachePath)
}

// pruneCache deletes entries from previous days. Without it the cache grows by
// the size of a full bundle every day it is used, and yesterday's files are
// never read again anyway. Best-effort: failures are not worth reporting.
func pruneCache() {
	entries, err := os.ReadDir(CacheDir)
	if err != nil {
		return
	}
	prefix := today() + "-"
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			os.Remove(filepath.Join(CacheDir, e.Name()))
		}
	}
}

// setFile is the shape of a per-set MTGJSON file, narrowed to the identifier
// mapping. Everything else in the file is ignored.
type setFile struct {
	Data struct {
		Cards []struct {
			UUID        string `json:"uuid"`
			Identifiers struct {
				ScryfallID string `json:"scryfallId"`
			} `json:"identifiers"`
		} `json:"cards"`
	} `json:"data"`
}

// ErrNoSuchSet reports a set code MTGJSON does not publish. Scryfall and MTGJSON
// mostly agree on set codes but diverge on some promo and supplemental sets, so
// callers should skip the set rather than fail.
var ErrNoSuchSet = errors.New("no such MTGJSON set")

// SetIdentifiers maps Scryfall IDs to MTGJSON UUIDs for one set.
//
// Per-set files are small (under a megabyte gzipped), which is why this is done
// set by set: the equivalent whole-catalog file, AllIdentifiers, is over 200 MB.
func SetIdentifiers(ctx context.Context, setCode string) (map[string]string, error) {
	body, err := fetch(ctx, strings.ToUpper(setCode)+".json.gz")
	if err != nil {
		if errors.Is(err, ErrNoSuchSet) {
			return nil, err
		}
		return nil, fmt.Errorf("fetching %s: %w", setCode, err)
	}
	defer body.Close()

	zr, err := gzip.NewReader(body)
	if err != nil {
		return nil, fmt.Errorf("decompressing %s: %w", setCode, err)
	}
	defer zr.Close()

	var sf setFile
	if err := json.NewDecoder(zr).Decode(&sf); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", setCode, err)
	}

	out := make(map[string]string, len(sf.Data.Cards))
	for _, c := range sf.Data.Cards {
		if c.Identifiers.ScryfallID != "" && c.UUID != "" {
			out[c.Identifiers.ScryfallID] = c.UUID
		}
	}
	return out, nil
}

// byFinish is a provider's prices for one side of the counter, keyed by date.
type byFinish struct {
	Normal map[string]float64 `json:"normal"`
	Foil   map[string]float64 `json:"foil"`
}

// vendor is one provider's prices for a card, as MTGJSON nests them:
// data[uuid].paper.<vendor>.{retail,buylist}.{normal,foil}.<date> = price
//
// Buylist is what the shop pays you, retail what it charges. Only Card Kingdom
// publishes a buylist through MTGJSON, so the sell side is one shop's offer
// rather than a market.
type vendor struct {
	Currency string   `json:"currency"`
	Retail   byFinish `json:"retail"`
	Buylist  byFinish `json:"buylist"`
}

type priceRecord struct {
	Paper map[string]vendor `json:"paper"`
}

// Quote is one vendor's price for one finish, on one side of the counter.
type Quote struct {
	Provider string // tcgplayer | cardkingdom | manapool
	Kind     string // retail (what it charges) | buylist (what it pays)
	Finish   string // normal | foil
	Price    float64
}

// Quote kinds.
const (
	Retail  = "retail"
	Buylist = "buylist"
)

// TodayQuotes returns every USD paper quote for the requested UUIDs, rather than
// the single best price TodayPrices settles on.
//
// Comparing vendors is the whole point here, so nothing is collapsed: a card
// with three retail quotes and a buylist offer yields four Quotes. Cardmarket is
// still excluded, since a euro price cannot be compared against dollar ones.
func TodayQuotes(ctx context.Context, want map[string]bool) (map[string][]Quote, error) {
	out := map[string][]Quote{}
	err := streamPrices(ctx, todayFile, want, func(uuid string, rec priceRecord) {
		var qs []Quote
		for _, name := range providerOrder {
			v, ok := rec.Paper[name]
			if !ok || v.Currency != "USD" {
				continue
			}
			for kind, side := range map[string]byFinish{Retail: v.Retail, Buylist: v.Buylist} {
				for finish, byDate := range map[string]map[string]float64{
					"normal": side.Normal, "foil": side.Foil,
				} {
					if p := latest(byDate); p != nil {
						qs = append(qs, Quote{
							Provider: name, Kind: kind, Finish: finish, Price: *p,
						})
					}
				}
			}
		}
		if len(qs) > 0 {
			out[uuid] = qs
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Price archives. Today's file holds one observation per card; the full one
// holds the last 90 days, and is thirty times the size for it.
const (
	todayFile   = "AllPricesToday.json.gz"
	archiveFile = "AllPrices.json.gz"
)

// streamPrices walks one of the price archives, handing each wanted record to
// visit.
//
// Even today's document is ~50 MB decoded and covers every card in Magic, so
// records not asked for are skipped without being built. Scan cost is the same
// whether one card is wanted or every card is, which is why callers need not
// keep want small.
func streamPrices(ctx context.Context, file string, want map[string]bool, visit func(string, priceRecord)) error {
	if len(want) == 0 {
		return nil
	}
	body, err := fetch(ctx, file)
	if err != nil {
		return fmt.Errorf("fetching prices: %w", err)
	}
	defer body.Close()

	zr, err := gzip.NewReader(body)
	if err != nil {
		return fmt.Errorf("decompressing prices: %w", err)
	}
	defer zr.Close()

	dec := json.NewDecoder(zr)
	if err := seekToData(dec); err != nil {
		return err
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("reading price key: %w", err)
		}
		uuid, _ := keyTok.(string)
		if !want[uuid] {
			if err := skipValue(dec); err != nil {
				return err
			}
			continue
		}
		var rec priceRecord
		if err := dec.Decode(&rec); err != nil {
			return fmt.Errorf("decoding prices for %s: %w", uuid, err)
		}
		visit(uuid, rec)
	}
	return nil
}

// TodayPrices returns USD paper retail prices for the requested UUIDs.
//
// AllPricesToday is ~5 MB gzipped but around 50 MB decoded and covers every card
// in Magic, so it is streamed and filtered against `want` rather than decoded
// whole. Only UUIDs asked for are retained.
func TodayPrices(ctx context.Context, want map[string]bool) (map[string]Price, error) {
	out := make(map[string]Price, len(want))
	err := streamPrices(ctx, todayFile, want, func(uuid string, rec priceRecord) {
		if p, ok := bestUSD(uuid, rec); ok {
			out[uuid] = p
		}
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// Observation is one card's price for one finish on one day.
//
// Source travels with the price rather than being assumed by the caller: a
// stored observation has to say which vendor quoted it, and having the vendor
// named in two packages is how the two drift apart.
type Observation struct {
	Date   string // 'YYYY-MM-DD'
	Finish string // normal | foil
	Price  float64
	Source string
}

// historyProvider is the only vendor whose back catalogue is read.
//
// Scryfall's USD prices come from TCGplayer alone, so this series is the one
// that is continuous with the prices hoard already holds. Splicing Card Kingdom
// or Manapool onto the front of a Scryfall series would put a vendor's markup at
// the join and read as a real price movement on the day the two meet.
const historyProvider = "tcgplayer"

// PriceHistory returns every dated USD retail observation MTGJSON holds for the
// requested UUIDs — about 90 days' worth, both finishes.
//
// This reads AllPrices rather than AllPricesToday: ~150 MB on the wire against
// 5 MB, and thirty times the rows, which is why nothing calls it on a schedule.
// Observations come back in no particular order; callers that care sort them.
func PriceHistory(ctx context.Context, want map[string]bool) (map[string][]Observation, error) {
	out := make(map[string][]Observation, len(want))
	err := streamPrices(ctx, archiveFile, want, func(uuid string, rec priceRecord) {
		v, ok := rec.Paper[historyProvider]
		if !ok || v.Currency != "USD" {
			return
		}
		var obs []Observation
		for finish, byDate := range map[string]map[string]float64{
			"normal": v.Retail.Normal, "foil": v.Retail.Foil,
		} {
			for date, price := range byDate {
				obs = append(obs, Observation{
					Date: date, Finish: finish, Price: price, Source: historyProvider,
				})
			}
		}
		if len(obs) > 0 {
			out[uuid] = obs
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// seekToData advances the decoder to the inside of the top-level "data" object,
// leaving it positioned at the first UUID key.
func seekToData(dec *json.Decoder) error {
	if _, err := dec.Token(); err != nil { // opening {
		return fmt.Errorf("reading prices: %w", err)
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("reading prices: %w", err)
		}
		if key, _ := keyTok.(string); key == "data" {
			if _, err := dec.Token(); err != nil { // opening { of data
				return fmt.Errorf("reading prices data: %w", err)
			}
			return nil
		}
		if err := skipValue(dec); err != nil { // e.g. "meta"
			return err
		}
	}
	return fmt.Errorf("mtgjson price file has no data object")
}

// skipValue consumes one JSON value, descending through nested objects and
// arrays, without allocating it.
func skipValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if _, ok := tok.(json.Delim); !ok {
		return nil // a scalar; already consumed
	}
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

// bestUSD resolves each finish independently, walking providerOrder until a
// vendor quotes that finish in USD.
//
// Per finish, not per vendor, because the gap this exists to fill is itself
// finish-specific. TCGplayer lists a non-foil price for the Modern Horizons 3
// ripple foils but no foil one, so stopping at the first vendor with *any* price
// takes the non-foil figure and never reaches Card Kingdom and Manapool, which
// do price the foil. That yields a row that looks filled and values the card at
// nothing.
//
// The cost is that a card's two finishes can come from different shops, which is
// acceptable: they are independent numbers, and Source records both.
func bestUSD(uuid string, rec priceRecord) (Price, bool) {
	pick := func(retail func(vendor) map[string]float64) (*float64, string) {
		for _, name := range providerOrder {
			v, ok := rec.Paper[name]
			if !ok || v.Currency != "USD" {
				continue
			}
			if p := latest(retail(v)); p != nil {
				return p, name
			}
		}
		return nil, ""
	}
	normal, normalSrc := pick(func(v vendor) map[string]float64 { return v.Retail.Normal })
	foil, foilSrc := pick(func(v vendor) map[string]float64 { return v.Retail.Foil })
	if normal == nil && foil == nil {
		return Price{}, false
	}
	return Price{
		UUID: uuid, USD: normal, Foil: foil,
		USDSource: normalSrc, FoilSource: foilSrc,
	}, true
}

// latest returns the most recent dated price, or nil when there is none. Keys
// are ISO dates, so lexical ordering is chronological.
func latest(byDate map[string]float64) *float64 {
	var newest string
	for d := range byDate {
		if d > newest {
			newest = d
		}
	}
	if newest == "" {
		return nil
	}
	v := byDate[newest]
	return &v
}
