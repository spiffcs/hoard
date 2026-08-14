package pricing

import (
	"context"
	"github.com/spiffcs/hoard/internal/finish"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/tcgcsv"
)

const historyDays = 90

var todayDate = func() string { return time.Now().Format("2006-01-02") }

func (f *Fetcher) tcgcsvOptions() tcgcsv.Options {
	o := tcgcsv.Options{BaseURL: f.tcgcsvBase, Note: func(msg string) { f.say("%s", msg) }}
	if f.cacheDir != "" {
		o.CacheDir = filepath.Join(filepath.Dir(f.cacheDir), "tcgcsv")
	}
	return o
}

func (f *Fetcher) treatedExtra(ctx context.Context, refs []Ref, days int, uuids map[string]string) mtgjson.ExtraSeries {
	foilIDs, etchedIDs, _, err := f.st.TCGAltProducts()
	if err != nil || (len(foilIDs) == 0 && len(etchedIDs) == 0) {
		return nil
	}

	type need struct {
		uuid, product string
		finish        finish.Finish
	}
	bySet := map[string][]need{}
	for _, r := range refs {
		uuid := r.MTGJSONUUID
		if uuid == "" {
			uuid = uuids[r.ScryfallID]
		}
		if uuid == "" {
			continue
		}
		code := strings.ToUpper(r.SetCode)
		for fin, pid := range map[finish.Finish]string{
			finish.Foil:   foilIDs[r.ScryfallID],
			finish.Etched: etchedIDs[r.ScryfallID],
		} {
			if pid == "" || (r.Finish != (finish.Finish{}) && r.Finish != fin) {
				continue
			}
			bySet[code] = append(bySet[code], need{uuid, pid, fin})
		}
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
	add := func(n need, date string, price float64) {
		if price <= 0 {
			return
		}
		e := extra[n.uuid]
		dst := &e.Foil
		if n.finish == finish.Etched {
			dst = &e.Etched
		}
		if *dst == nil {
			*dst = map[string]float64{}
		}
		(*dst)[date] = price
		extra[n.uuid] = e
	}
	var gids []int
	byGroup := map[int][]need{}
	for code, needs := range bySet {
		gid, ok := groups[code]
		if !ok {
			continue
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
			price, _ := Resolve(prices[n.product])
			add(n, todayDate(), price)
		}
	}
	if days <= 1 || len(gids) == 0 {
		return extra
	}

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
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for gid, needs := range byGroup {
				prices := day[gid]
				if prices == nil {
					continue
				}
				for _, n := range needs {
					price, _ := Resolve(prices[n.product])
					add(n, date, price)
				}
			}
		}()
	}
	wg.Wait()
	return extra
}

const archiveWorkers = 6
