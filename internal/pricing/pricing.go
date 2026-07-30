// Package pricing reads prices MTGJSON has and Scryfall does not.
//
// Scryfall's USD figures come from TCGplayer alone, so a printing TCGplayer has
// no record of is simply unpriced there — not rare, and whole decks can hang on
// it. MTGJSON aggregates other vendors, but keys everything by its own UUID.
//
// Callers pass Scryfall ids and get Scryfall ids back. The UUID, the set files
// downloaded to learn it, and the cache they live in stay inside here.
package pricing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/store"
)

// Ref is a printing to price.
type Ref struct {
	ScryfallID string
	SetCode    string
	// MTGJSONUUID is the id already stored for this printing, empty if unknown.
	// Supplying it avoids a set-file download.
	MTGJSONUUID string
}

// DefaultCacheDir is where downloaded MTGJSON bundles belong: the OS cache
// directory, because they are re-downloadable and losing them costs a fetch.
// Empty disables caching, which only makes the downloads repeat.
func DefaultCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "hoard", "mtgjson")
}

// Fetcher reads MTGJSON prices for hoard's printings.
type Fetcher struct {
	st       *store.Store
	cacheDir string
	// progress reports work worth waiting through. Nil is silent, which is what
	// tests want and what keeps output decisions in the command layer.
	progress func(string)
}

// New returns a Fetcher reading through cacheDir.
func New(st *store.Store, cacheDir string) *Fetcher {
	return &Fetcher{st: st, cacheDir: cacheDir}
}

// WithProgress attaches a progress reporter.
func (f *Fetcher) WithProgress(fn func(string)) *Fetcher {
	f.progress = fn
	return f
}

func (f *Fetcher) say(format string, args ...any) {
	if f.progress != nil {
		f.progress(fmt.Sprintf(format, args...))
	}
}

// Prices returns the best USD price for each printing that has one, keyed by
// Scryfall id.
func (f *Fetcher) Prices(ctx context.Context, refs []Ref) (map[string]mtgjson.Price, error) {
	byUUID, toScryfall, err := f.want(ctx, refs)
	if err != nil || len(byUUID) == 0 {
		return nil, err
	}
	prices, err := mtgjson.TodayPrices(ctx, f.cacheDir, byUUID)
	if err != nil {
		return nil, fmt.Errorf("mtgjson prices: %w", err)
	}
	out := make(map[string]mtgjson.Price, len(prices))
	for uuid, p := range prices {
		out[toScryfall[uuid]] = p
	}
	return out, nil
}

// Quotes returns every vendor quote for each printing, keyed by Scryfall id.
func (f *Fetcher) Quotes(ctx context.Context, refs []Ref) (map[string][]mtgjson.Quote, error) {
	byUUID, toScryfall, err := f.want(ctx, refs)
	if err != nil || len(byUUID) == 0 {
		return nil, err
	}
	quotes, err := mtgjson.TodayQuotes(ctx, f.cacheDir, byUUID)
	if err != nil {
		return nil, fmt.Errorf("mtgjson quotes: %w", err)
	}
	out := make(map[string][]mtgjson.Quote, len(quotes))
	for uuid, q := range quotes {
		out[toScryfall[uuid]] = q
	}
	return out, nil
}

// History returns up to ninety days of observations for each printing, keyed by
// Scryfall id. Reads a ~150 MB archive, so it is only for a deliberate backfill.
func (f *Fetcher) History(ctx context.Context, refs []Ref) (map[string][]mtgjson.Observation, error) {
	byUUID, toScryfall, err := f.want(ctx, refs)
	if err != nil || len(byUUID) == 0 {
		return nil, err
	}
	hist, err := mtgjson.PriceHistory(ctx, f.cacheDir, byUUID)
	if err != nil {
		return nil, fmt.Errorf("mtgjson price history: %w", err)
	}
	out := make(map[string][]mtgjson.Observation, len(hist))
	for uuid, obs := range hist {
		out[toScryfall[uuid]] = obs
	}
	return out, nil
}

// Resolvable reports how many refs have an MTGJSON id, so a caller can say how
// many printings it could not ask about.
func (f *Fetcher) Resolvable(ctx context.Context, refs []Ref) (int, error) {
	byUUID, _, err := f.want(ctx, refs)
	return len(byUUID), err
}

// want resolves refs to the UUID set MTGJSON is keyed by, plus the way back.
func (f *Fetcher) want(ctx context.Context, refs []Ref) (map[string]bool, map[string]string, error) {
	uuids, err := f.resolve(ctx, refs)
	if err != nil {
		return nil, nil, err
	}
	byUUID := make(map[string]bool, len(refs))
	toScryfall := make(map[string]string, len(refs))
	for _, r := range refs {
		uuid := r.MTGJSONUUID
		if uuid == "" {
			uuid = uuids[r.ScryfallID]
		}
		if uuid != "" {
			byUUID[uuid] = true
			toScryfall[uuid] = r.ScryfallID
		}
	}
	return byUUID, toScryfall, nil
}

// resolve maps Scryfall ids to MTGJSON UUIDs, downloading only the set files it
// must and remembering everything it learns.
//
// One id costs a whole set-file download and the answer never changes, so
// results are written back to the catalog. Without that, a collection-wide price
// read would re-fetch most of the catalog's set files every day, because the
// download cache is pruned nightly.
func (f *Fetcher) resolve(ctx context.Context, refs []Ref) (map[string]string, error) {
	known, err := f.st.KnownMTGJSONUUIDs()
	if err != nil {
		return nil, err
	}
	bySet := map[string][]string{}
	for _, r := range refs {
		if r.MTGJSONUUID != "" {
			continue
		}
		if _, ok := known[r.ScryfallID]; !ok {
			bySet[r.SetCode] = append(bySet[r.SetCode], r.ScryfallID)
		}
	}
	if len(bySet) == 0 {
		return known, nil
	}

	if len(bySet) > 3 {
		f.say("resolving card ids from %d sets (once only)...", len(bySet))
	}
	learned := make(map[string]string)
	for setCode, sids := range bySet {
		ids, err := mtgjson.SetIdentifiers(ctx, f.cacheDir, setCode)
		if err != nil {
			// Scryfall and MTGJSON disagree on some promo sets. Skip the set
			// rather than abandon every other card.
			f.say("skipping set %s: %v", setCode, err)
			continue
		}
		for _, sid := range sids {
			if uuid, ok := ids[sid]; ok {
				known[sid] = uuid
				learned[sid] = uuid
			}
		}
	}
	if err := f.st.SaveMTGJSONUUIDs(learned); err != nil {
		return nil, err
	}
	return known, nil
}
