package pricing

// The treated-foil overlay. TCGplayer sells ripple/surge/textured foils
// and etched cards as separate products, and the MTGJSON feed never
// carries those products' prices — its tcgplayer bucket covers only the
// base product, which is why every treated foil showed a dash for TCG
// SOLD (observed live: Akroma's Will ripple, product 553005, listed and
// selling while the feed said nothing). The ids are in the set files
// (resolve stores them); the prices are on TCGplayer's public catalog,
// read here via tcgcsv.com and merged into the MTGJSON records as an
// ExtraSeries, so bestUSD, the quote list, and the history fallback all
// treat them as native feed data.
//
// Everything here fails soft: the overlay missing means dashes come back,
// not a broken price update.

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/tcgcsv"
)

// historyDays matches the MTGJSON archive's reach, so the treated foils'
// backfilled series is as deep as everyone else's.
const historyDays = 90

// todayDate is a var so tests can pin the overlay's date keys.
var todayDate = func() string { return time.Now().Format("2006-01-02") }

// tcgcsvOptions derives the overlay client's cache home: a sibling of the
// MTGJSON cache, empty when caching is off.
func (f *Fetcher) tcgcsvOptions() tcgcsv.Options {
	o := tcgcsv.Options{BaseURL: f.tcgcsvBase}
	if f.cacheDir != "" {
		o.CacheDir = filepath.Join(filepath.Dir(f.cacheDir), "tcgcsv")
	}
	return o
}

// treatedExtra builds the overlay series for every ref whose printing has
// a split TCGplayer product: today's market price, and with days > 1 the
// archive series behind it. Returns nil — no overlay — when nothing is
// treated or the source is unreachable.
func (f *Fetcher) treatedExtra(ctx context.Context, refs []Ref, days int) mtgjson.ExtraSeries {
	uuids, err := f.resolve(ctx, refs)
	if err != nil {
		return nil
	}
	ids, _, err := f.st.TCGAltProducts()
	if err != nil || len(ids) == 0 {
		return nil
	}
	type need struct{ uuid, product string }
	bySet := map[string][]need{}
	for _, r := range refs {
		pid := ids[r.ScryfallID]
		if pid == "" {
			continue
		}
		uuid := r.MTGJSONUUID
		if uuid == "" {
			uuid = uuids[r.ScryfallID]
		}
		if uuid == "" {
			continue
		}
		code := strings.ToUpper(r.SetCode)
		bySet[code] = append(bySet[code], need{uuid, pid})
	}
	if len(bySet) == 0 {
		return nil
	}
	opts := f.tcgcsvOptions()
	groups, err := tcgcsv.Groups(ctx, opts)
	if err != nil {
		f.say("skipping treated-foil prices: %v", err)
		return nil
	}

	extra := mtgjson.ExtraSeries{}
	add := func(uuid, date string, price float64) {
		if price <= 0 {
			return
		}
		if extra[uuid] == nil {
			extra[uuid] = map[string]float64{}
		}
		extra[uuid][date] = price
	}
	var gids []int
	byGroup := map[int][]need{}
	for code, needs := range bySet {
		gid, ok := groups[code]
		if !ok {
			continue // a set TCGplayer groups differently; those cards keep their dashes
		}
		if byGroup[gid] == nil {
			gids = append(gids, gid)
		}
		byGroup[gid] = append(byGroup[gid], needs...)
	}
	for gid, needs := range byGroup {
		prices, err := tcgcsv.GroupPrices(ctx, opts, gid)
		if err != nil {
			f.say("skipping treated-foil prices for group %d: %v", gid, err)
			continue
		}
		for _, n := range needs {
			add(n.uuid, todayDate(), prices[n.product])
		}
	}
	if days <= 1 || len(gids) == 0 {
		return extra
	}

	// The archive series: one ~4 MB download per day the extraction cache
	// does not already answer, decoded in pure Go (see tcgcsv's lenient
	// PPMd registration). The days run concurrently — each archive is an
	// independent download plus a CPU-heavy PPMd decode of a solid
	// stream, and running them one at a time made the first backfill a
	// multi-minute wait (observed live). Bounded workers keep memory sane
	// (each decode holds the PPMd model plus buffers); cached days cost a
	// file read and finish instantly, so steady state is one worker doing
	// one day.
	now := time.Now()
	var (
		mu   sync.Mutex
		done atomic.Int32
		wg   sync.WaitGroup
	)
	sem := make(chan struct{}, archiveWorkers)
	for i := 1; i < days; i++ {
		if ctx.Err() != nil {
			break
		}
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			day, err := tcgcsv.ArchivePrices(ctx, opts, date, gids)
			f.say("reading treated-foil price archives · day %d/%d", done.Add(1), days-1)
			if err != nil {
				return // one day's archive missing is a hole, not a failure
			}
			mu.Lock()
			defer mu.Unlock()
			for gid, needs := range byGroup {
				prices := day[gid]
				if prices == nil {
					continue
				}
				for _, n := range needs {
					add(n.uuid, date, prices[n.product])
				}
			}
		}()
	}
	wg.Wait()
	return extra
}

// archiveWorkers bounds the concurrent archive reads: enough to overlap
// downloads with PPMd decodes, few enough that memory stays boring.
const archiveWorkers = 4
