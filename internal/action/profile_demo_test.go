package action_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/demo"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

type stepTimer struct {
	t     *testing.T
	total map[string]time.Duration
	order []string
	last  string
	at    time.Time
	start time.Time
}

func newStepTimer(t *testing.T) *stepTimer {
	now := time.Now()
	return &stepTimer{t: t, total: map[string]time.Duration{}, at: now, start: now}
}

func bucket(ev progress.Event) string {
	if ev.Note == "" {
		return ev.Step
	}
	prefix := ev.Note
	if i := strings.Index(prefix, "·"); i >= 0 {
		prefix = strings.TrimSpace(prefix[:i])
	}
	if len(prefix) > 60 {
		prefix = prefix[:60]
	}
	return ev.Step + " | " + prefix
}

func (s *stepTimer) fn() progress.Fn {
	return func(ev progress.Event) {
		b := bucket(ev)
		if b == s.last {
			return
		}
		s.charge()
		s.last = b
		if _, seen := s.total[b]; !seen {
			s.order = append(s.order, b)
		}
	}
}

func (s *stepTimer) charge() {
	now := time.Now()
	if s.last != "" {
		s.total[s.last] += now.Sub(s.at)
	} else {
		s.total["(before first event)"] += now.Sub(s.at)
		if len(s.order) == 0 {
			s.order = append(s.order, "(before first event)")
		}
	}
	s.at = now
}

func (s *stepTimer) report(label string) {
	s.charge()
	s.t.Logf("=== %s: %.2fs total", label, time.Since(s.start).Seconds())
	rows := append([]string(nil), s.order...)
	sort.SliceStable(rows, func(i, j int) bool { return s.total[rows[i]] > s.total[rows[j]] })
	for _, step := range rows {
		s.t.Logf("    %8.2fs  %s", s.total[step].Seconds(), step)
	}
}

func TestProfileDemoPopulate(t *testing.T) {
	if os.Getenv("HOARD_PROFILE_DEMO") == "" {
		t.Skip("set HOARD_PROFILE_DEMO=1 to profile the demo's Movers population")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "demo.db")

	if src := os.Getenv("HOARD_PROFILE_DEMO_DB"); src != "" {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("profiling a copy of %s (%d MB)", src, len(b)>>20)
	}

	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if os.Getenv("HOARD_PROFILE_DEMO_DB") == "" {
		start := time.Now()
		seed, err := action.SeedHoard(st, demo.Collection, "the sample collection")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("seed: %.2fs (%d printings, %d copies)",
			time.Since(start).Seconds(), seed.Printings, seed.Copies)
	}
	owned, err := st.OwnedByFinish()
	if err != nil {
		t.Fatal(err)
	}
	sets := map[string]bool{}
	for _, o := range owned {
		sets[o.SetCode] = true
	}
	t.Logf("hoard: %d held card-and-finish rows across %d sets", len(owned), len(sets))

	cacheHome, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Open(filepath.Join(cacheHome, "hoard", "catalog"))
	if err != nil {
		t.Logf("catalog unavailable: %v", err)
	} else {
		defer cat.Close()
		t.Logf("catalog: %d cards", cat.CardCount())
	}

	cacheDir := pricing.DefaultCacheDir()
	if os.Getenv("HOARD_PROFILE_DEMO_COLD") != "" {
		cacheDir = filepath.Join(dir, "cache", "mtgjson")
		t.Logf("cold caches: %s", cacheDir)
	}

	deps := action.Deps{
		Store: st, Catalog: cat, CacheDir: cacheDir,
		Confirm: func(q string) bool { t.Logf("confirm (answering yes): %s", q); return true },
	}
	ctx := context.Background()

	upStart := time.Now()
	stage := func(name string, fn func() error) {
		t.Helper()
		start := time.Now()
		if err := fn(); err != nil {
			t.Fatal(err)
		}
		t.Logf("    %8.2fs  %s", time.Since(start).Seconds(), name)
	}
	t.Log("=== UpdatePrices, by stage")

	ids, err := st.ActivePrintingIDs()
	if err != nil {
		t.Fatal(err)
	}
	var found []scryfall.Card
	var fromCatalog int
	stage("checking catalog", func() error {
		action.EnsureCatalog(ctx, deps, nil)
		return nil
	})
	stage("refreshing cards", func() (err error) {
		found, _, fromCatalog, err = action.RefreshCards(ctx, deps, nil, ids)
		return err
	})
	stage("storing printings", func() error { return st.UpsertPrintings(found) })
	var gaps action.GapReport
	stage("filling gaps (mtgjson)", func() (err error) {
		gaps, err = action.FillGaps(ctx, deps, nil)
		return err
	})
	stage("recording history", func() error {
		_, err := st.RecordPrices()
		return err
	})

	blocking := time.Since(upStart)
	var refused, repaired int
	stage("checking prices against asks (tcgcsv) [DEFERRED]", func() (err error) {
		refused, err = action.RefuseContradictedPrices(ctx, deps, nil)
		return err
	})
	stage("repairing the recording [DEFERRED]", func() error {
		_, n, err := st.RepairRecordedPrices()
		repaired = n
		return err
	})
	upWall := time.Since(upStart)
	t.Logf("    blocking: %.2fs of %.2fs · %d observation(s) repaired",
		blocking.Seconds(), upWall.Seconds(), repaired)
	t.Logf("    result: total=%d fromCatalog=%d refused=%d gaps=%+v",
		len(ids), fromCatalog, refused, gaps)

	bf := newStepTimer(t)
	bfStart := time.Now()
	bres, err := action.BackfillPrices(ctx, deps, bf.fn(), 90)
	bfWall := time.Since(bfStart)
	if err != nil {
		t.Fatal(err)
	}
	bf.report("BackfillPrices(90)")
	t.Logf("    result: printings=%d inserted=%d cards=%d bids=%d unmapped=%d unquoted=%d",
		bres.Printings, bres.Inserted, bres.Cards, bres.BidInserted, bres.Unmapped, bres.Unquoted)

	fmt.Printf("\nPOPULATE TOTAL: %.2fs  (update %.2fs + backfill %.2fs)\n",
		(upWall + bfWall).Seconds(), upWall.Seconds(), bfWall.Seconds())
}
