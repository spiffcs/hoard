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
	"errors"
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
	// bytes watches downloads land, for callers rendering a determinate bar
	// over the multi-megabyte bundles. Nil is silent.
	bytes func(done, total int64)
	// baseURL overrides the MTGJSON file root; empty means the real one.
	baseURL string
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

// WithBytes attaches a download byte-count reporter.
func (f *Fetcher) WithBytes(fn func(done, total int64)) *Fetcher {
	f.bytes = fn
	return f
}

// options is what every mtgjson call reads through.
func (f *Fetcher) options() mtgjson.Options {
	return mtgjson.Options{CacheDir: f.cacheDir, Progress: f.bytes, BaseURL: f.baseURL}
}

// WithBaseURL points the fetcher at another MTGJSON file root — tests.
func (f *Fetcher) WithBaseURL(u string) *Fetcher {
	f.baseURL = u
	return f
}

func (f *Fetcher) say(format string, args ...any) {
	if f.progress != nil {
		f.progress(fmt.Sprintf(format, args...))
	}
}

// remap runs one MTGJSON reader over the resolvable refs and re-keys its result
// by Scryfall id. The public readers differ only in which reader they hand
// over; the resolution and the key translation are one decision, made here.
// The int is how many refs resolved, so a caller can report the printings it
// could not ask about without resolving them all a second time.
func remap[T any](f *Fetcher, ctx context.Context, refs []Ref, what string,
	read func(context.Context, mtgjson.Options, map[string]bool) (map[string]T, error)) (map[string]T, int, error) {
	byUUID, toScryfall, err := f.want(ctx, refs)
	if err != nil || len(byUUID) == 0 {
		return nil, 0, err
	}
	got, err := read(ctx, f.options(), byUUID)
	if err != nil {
		return nil, 0, fmt.Errorf("mtgjson %s: %w", what, err)
	}
	out := make(map[string]T, len(got))
	for uuid, v := range got {
		out[toScryfall[uuid]] = v
	}
	return out, len(byUUID), nil
}

// Prices returns the best USD price for each printing that has one, keyed by
// Scryfall id.
func (f *Fetcher) Prices(ctx context.Context, refs []Ref) (map[string]mtgjson.Price, error) {
	out, _, err := remap(f, ctx, refs, "prices", mtgjson.TodayPrices)
	return out, err
}

// Quotes returns every vendor quote for each printing, keyed by Scryfall id.
//
// Answers come from the day-cache when today's parse already covered these
// printings — the full bundle is a ~50 MB scan for a few tens of kilobytes of
// quotes about cards actually owned, and vendor prices only change with the
// bundle, once a day.
// CachedQuotes serves today's vendor quotes from the day-cache without any
// network or parse work — nil, false when today's cache is missing or does
// not cover every requested printing. It is how a fresh session can show
// the quotes an earlier one already fetched.
func (f *Fetcher) CachedQuotes(refs []Ref) (map[string][]mtgjson.Quote, bool) {
	return f.cachedQuotes(refs)
}

func (f *Fetcher) Quotes(ctx context.Context, refs []Ref) (map[string][]mtgjson.Quote, error) {
	if out, ok := f.cachedQuotes(refs); ok {
		return out, nil
	}
	out, _, err := remap(f, ctx, refs, "quotes", mtgjson.TodayQuotes)
	if err == nil {
		f.saveQuotes(refs, out)
	}
	return out, err
}

// History returns up to ninety days of observations for each printing, keyed by
// Scryfall id, plus how many refs had an id to ask with. Reads a ~150 MB
// archive, so it is only for a deliberate backfill.
func (f *Fetcher) History(ctx context.Context, refs []Ref) (map[string]mtgjson.CardHistory, int, error) {
	return remap(f, ctx, refs, "price history", mtgjson.PriceHistory)
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
// must and remembering everything it learns — including Card Kingdom's
// product links, which ride in the same files.
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
	linked, err := f.st.KnownCardKingdomLinks()
	if err != nil {
		return nil, err
	}
	bySet := map[string][]string{}
	for _, r := range refs {
		// A set is fetched when anything it can teach is still unknown:
		// the uuid, or the vendor links a pre-v15 card never learned. Both
		// are stamped after one read, so this stays a once-per-set cost.
		needUUID := r.MTGJSONUUID == "" && known[r.ScryfallID] == ""
		if needUUID || !linked[r.ScryfallID] {
			bySet[r.SetCode] = append(bySet[r.SetCode], r.ScryfallID)
		}
	}
	if len(bySet) == 0 {
		return known, nil
	}

	// Set files are around a megabyte each; a byte bar per file renders as
	// dozens of bars filling and vanishing, which reads as the same
	// download failing over and over (observed live). The count note below
	// is the honest progress; byte bars stay for the big archives, where
	// one download is the whole wait.
	quiet := f.options()
	quiet.Progress = nil
	learned := make(map[string]string)
	links := make(map[string]store.CKLinks)
	n := 0
	for setCode, sids := range bySet {
		n++
		f.say("resolving card ids · set %d/%d (%s) · once only", n, len(bySet), setCode)
		ids, err := mtgjson.SetIdentifiers(ctx, quiet, setCode)
		if errors.Is(err, mtgjson.ErrNoSuchSet) {
			// Scryfall and MTGJSON disagree on some promo sets. Skip the set
			// rather than abandon every other card.
			f.say("skipping set %s: mtgjson has no such set", setCode)
			continue
		}
		if err != nil {
			// Anything else — DNS failure, a 503, a cancelled context — is not
			// an answer about this set. Pressing on would let an outage read as
			// "these cards resolved to nothing", and FillGaps would then stamp
			// every gap as checked and stay quiet for a week about prices that
			// do exist. An honest failure is re-runnable; a wrong answer isn't.
			return nil, fmt.Errorf("resolving mtgjson ids for set %s: %w", setCode, err)
		}
		for _, sid := range sids {
			if sc, ok := ids[sid]; ok {
				known[sid] = sc.UUID
				learned[sid] = sc.UUID
				// Empty links save too: the file was read, and NULL staying
				// NULL would fetch it again tomorrow.
				links[sid] = store.CKLinks{URL: sc.CKURL, FoilURL: sc.CKFoilURL}
			}
		}
	}
	if err := f.st.SaveMTGJSONUUIDs(learned); err != nil {
		return nil, err
	}
	if err := f.st.SaveCardKingdomLinks(links); err != nil {
		return nil, err
	}
	return known, nil
}
