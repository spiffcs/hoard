package pricing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/store"
)

type Ref struct {
	ScryfallID string
	SetCode    string

	MTGJSONUUID string

	Finish finish.Finish
}

func DefaultCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "hoard", "mtgjson")
}

type Fetcher struct {
	st       *store.Store
	cacheDir string

	progress func(string)

	warning func(msg, group string)

	bytes func(done, total int64)

	baseURL string

	tcgcsvBase string
}

func New(st *store.Store, cacheDir string) *Fetcher {
	return &Fetcher{st: st, cacheDir: cacheDir}
}

func (f *Fetcher) WithProgress(fn func(string)) *Fetcher {
	f.progress = fn
	return f
}

func (f *Fetcher) WithWarning(fn func(msg, group string)) *Fetcher {
	f.warning = fn
	return f
}

func (f *Fetcher) WithBytes(fn func(done, total int64)) *Fetcher {
	f.bytes = fn
	return f
}

func (f *Fetcher) options() mtgjson.Options {
	return mtgjson.Options{CacheDir: f.cacheDir, Progress: f.bytes, BaseURL: f.baseURL}
}

func (f *Fetcher) WithBaseURL(u string) *Fetcher {
	f.baseURL = u
	return f
}

func (f *Fetcher) WithTCGCSVBaseURL(u string) *Fetcher {
	f.tcgcsvBase = u
	return f
}

func (f *Fetcher) say(format string, args ...any) {
	if f.progress != nil {
		f.progress(fmt.Sprintf(format, args...))
	}
}

func (f *Fetcher) warn(group, format string, args ...any) {
	if f.warning != nil {
		f.warning(fmt.Sprintf(format, args...), group)
	}
}

func remap[T any](f *Fetcher, ctx context.Context, refs []Ref, uuids map[string]string, what string,
	read func(context.Context, mtgjson.Options, map[string]bool) (map[string]T, error)) (map[string]T, int, error) {
	byUUID, toScryfall := f.want(refs, uuids)
	if len(byUUID) == 0 {
		return nil, 0, nil
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

func (f *Fetcher) Prices(ctx context.Context, refs []Ref) (map[string]mtgjson.Price, error) {
	uuids, err := f.resolve(ctx, refs)
	if err != nil {
		return nil, err
	}
	extra := f.treatedExtra(ctx, refs, 1, uuids)
	out, _, err := remap(f, ctx, refs, uuids, "prices", mtgjson.TodayPricesWith(extra))
	return out, err
}

func (f *Fetcher) CachedQuotes(refs []Ref) (map[string][]mtgjson.Quote, bool) {
	return f.cachedQuotes(refs)
}

func (f *Fetcher) Quotes(ctx context.Context, refs []Ref) (map[string][]mtgjson.Quote, error) {
	if out, ok := f.cachedQuotes(refs); ok {
		return out, nil
	}
	return f.RefreshQuotes(ctx, refs)
}

func (f *Fetcher) RefreshQuotes(ctx context.Context, refs []Ref) (map[string][]mtgjson.Quote, error) {
	uuids, err := f.resolve(ctx, refs)
	if err != nil {
		return nil, err
	}

	extra := f.treatedExtra(ctx, refs, 1, uuids)
	out, _, err := remap(f, ctx, refs, uuids, "quotes", mtgjson.TodayQuotesWith(extra))
	if err != nil {
		return out, err
	}
	for sid, asks := range f.asks(ctx, refs) {
		out[sid] = append(out[sid], asks...)
	}
	f.saveQuotes(refs, out)
	return out, nil
}

func (f *Fetcher) History(ctx context.Context, refs []Ref, days int) (map[string]mtgjson.CardHistory, int, error) {
	if days <= 0 || days > historyDays {
		days = historyDays
	}
	uuids, err := f.resolve(ctx, refs)
	if err != nil {
		return nil, 0, err
	}

	warm := make(chan struct{})
	go func() {
		defer close(warm)
		if err := mtgjson.PrefetchPriceHistory(ctx, f.options()); err != nil {
			f.say("fetching the price archive: %v", err)
		}
	}()
	extra := f.treatedExtra(ctx, refs, days, uuids)
	<-warm

	return remap(f, ctx, refs, uuids, "price history", mtgjson.PriceHistoryWith(extra))
}

func (f *Fetcher) want(refs []Ref, uuids map[string]string) (map[string]bool, map[string]string) {
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
	return byUUID, toScryfall
}

func (f *Fetcher) resolve(ctx context.Context, refs []Ref) (map[string]string, error) {
	known, err := f.st.KnownMTGJSONUUIDs()
	if err != nil {
		return nil, err
	}
	linked, err := f.st.KnownCardKingdomLinks()
	if err != nil {
		return nil, err
	}
	_, _, altStamped, err := f.st.TCGAltProducts()
	if err != nil {
		return nil, err
	}
	vendorStamped, err := f.st.KnownVendorProductIDs()
	if err != nil {
		return nil, err
	}
	bySet := map[string][]string{}
	for _, r := range refs {

		needUUID := r.MTGJSONUUID == "" && known[r.ScryfallID] == ""
		if needUUID || !linked[r.ScryfallID] || !altStamped[r.ScryfallID] || !vendorStamped[r.ScryfallID] {
			bySet[r.SetCode] = append(bySet[r.SetCode], r.ScryfallID)
		}
	}
	if len(bySet) == 0 {
		return known, nil
	}

	quiet := f.options()
	quiet.Progress = nil
	learned := make(map[string]string)
	links := make(map[string]store.CKLinks)
	altIDs := make(map[string]string)
	etchedIDs := make(map[string]string)
	vendorIDs := make(map[string]store.VendorProductIDs)
	n := 0
	for setCode, sids := range bySet {
		n++
		f.say("resolving card ids · set %d/%d (%s)", n, len(bySet), setCode)
		ids, err := mtgjson.SetIdentifiers(ctx, quiet, setCode)
		if errors.Is(err, mtgjson.ErrNoSuchSet) {

			f.warn("sets are not in MTGJSON, so their printings are unpriced",
				"skipping set %s: mtgjson has no such set", setCode)
			continue
		}
		if err != nil {

			return nil, fmt.Errorf("resolving mtgjson ids for set %s: %w", setCode, err)
		}
		for _, sid := range sids {
			if sc, ok := ids[sid]; ok {
				known[sid] = sc.UUID
				learned[sid] = sc.UUID

				links[sid] = store.CKLinks{URL: sc.CKURL, FoilURL: sc.CKFoilURL}
				altIDs[sid] = sc.AltProductID
				etchedIDs[sid] = sc.EtchedProductID
				vendorIDs[sid] = store.VendorProductIDs{
					TCGProduct: sc.TCGProductID,
					CKFoil:     sc.CKFoilID,
					CKEtched:   sc.CKEtchedID,
				}
			}
		}
	}
	if err := f.st.SaveMTGJSONUUIDs(learned); err != nil {
		return nil, err
	}
	if err := f.st.SaveCardKingdomLinks(links); err != nil {
		return nil, err
	}
	if err := f.st.SaveTCGAltProducts(altIDs, etchedIDs); err != nil {
		return nil, err
	}
	if err := f.st.SaveVendorProductIDs(vendorIDs); err != nil {
		return nil, err
	}
	return known, nil
}
