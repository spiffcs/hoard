// Package tcgcsv reads TCGplayer prices for the products the MTGJSON feed
// never carries: the treated foils (ripple, surge, textured, …) and
// etched cards TCGplayer sells as separate products.
//
// tcgcsv.com republishes TCGplayer's public catalog daily — no key, plain
// JSON. Three reads matter here: the group list (one group per set), a
// group's current prices, and the daily archives of the same. Everything
// is keyed by TCGplayer product id; mapping a printing to its treated
// product id is the caller's business (MTGJSON identifiers carry it).
//
// Failures here must stay soft at the call site: this is an overlay over
// the primary feed, and a missing overlay means a dash, not a broken
// price update.
package tcgcsv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bodgit/sevenzip"

	"github.com/spiffcs/hoard/internal/buildinfo"
)

// magicCategory is TCGplayer's category id for Magic: the Gathering.
const magicCategory = 1

// apiBase is the tcgcsv file root, a var for tests.
var apiBase = "https://tcgcsv.com"

// today is a var so tests can pin the cache key.
var today = func() string { return time.Now().Format("2006-01-02") }

// Options configures a fetch.
type Options struct {
	// BaseURL overrides the tcgcsv root — tests. Empty means the real one.
	BaseURL string
	// CacheDir, when non-empty, keeps downloads: current files day-stamped
	// (pruned like the MTGJSON cache), archive extractions date-keyed and
	// immutable (pruned past archiveKeepDays).
	CacheDir string
}

func (o Options) base() string {
	if o.BaseURL != "" {
		return o.BaseURL
	}
	return apiBase
}

var httpClient = &http.Client{Timeout: 2 * time.Minute}

// The request pacer: tcgcsv is a volunteer mirror, and the archive sweep
// with concurrent workers is exactly the burst shape volunteer
// infrastructure hates. No two request *starts* come closer than
// requestGap; cache hits never touch the pacer, so a fully cached day
// costs zero waits along with its zero requests. Vars so tests pin them.
var (
	requestGap = 250 * time.Millisecond
	paceMu     sync.Mutex
	lastStart  time.Time
	paceSleep  = time.Sleep
)

// pace blocks until this request start is requestGap after the previous one.
func pace() {
	paceMu.Lock()
	wait := requestGap - time.Since(lastStart)
	if wait > 0 {
		paceSleep(wait)
	}
	lastStart = time.Now()
	paceMu.Unlock()
}

// get fetches one URL's body, through the day cache when cacheName is
// non-empty. Day-cached entries from earlier dates are removed as a side
// effect, mirroring the MTGJSON cache's convention.
func get(ctx context.Context, o Options, url, cacheName string) ([]byte, error) {
	var cachePath string
	if o.CacheDir != "" && cacheName != "" {
		cachePath = filepath.Join(o.CacheDir, today()+"-"+cacheName)
		if b, err := os.ReadFile(cachePath); err == nil {
			return b, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent)
	pace()
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if cachePath != "" {
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
			os.WriteFile(cachePath, b, 0o644)
			cleanDayCache(o.CacheDir)
		}
	}
	return b, nil
}

// cleanDayCache removes day-stamped entries from earlier dates. Archive
// extractions live under archive/ and are immutable, so they are pruned
// by age instead (pruneArchive).
func cleanDayCache(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := today() + "-"
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		os.Remove(filepath.Join(dir, e.Name()))
	}
}

// Groups maps upper-cased set abbreviations to TCGplayer group ids for
// the Magic category. Some Scryfall set codes have no group; callers skip
// those.
func Groups(ctx context.Context, o Options) (map[string]int, error) {
	b, err := get(ctx, o, fmt.Sprintf("%s/tcgplayer/%d/groups", o.base(), magicCategory), "groups.json")
	if err != nil {
		return nil, err
	}
	var d struct {
		Results []struct {
			GroupID      int    `json:"groupId"`
			Abbreviation string `json:"abbreviation"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("decoding tcgcsv groups: %w", err)
	}
	out := make(map[string]int, len(d.Results))
	for _, g := range d.Results {
		if g.Abbreviation != "" {
			out[strings.ToUpper(g.Abbreviation)] = g.GroupID
		}
	}
	return out, nil
}

// priceRows is the JSON shape of one group's price list — identical on
// the current endpoint and inside the archives.
type priceRows struct {
	Results []struct {
		ProductID   int     `json:"productId"`
		MarketPrice float64 `json:"marketPrice"`
		LowPrice    float64 `json:"lowPrice"`
		MidPrice    float64 `json:"midPrice"`
		HighPrice   float64 `json:"highPrice"`
		SubTypeName string  `json:"subTypeName"`
	} `json:"results"`
}

// Quote is one product's figures for a day, as TCGplayer publishes them.
//
// Market and the ask figures answer different questions and can disagree
// without either being wrong. Market is an average over *completed sales*;
// Low, Mid and High describe the *listings* standing right now. An illiquid
// card whose last sale undercuts today's cheapest listing is ordinary — 39
// of 2,604 owned products quote Market under Low, and 38 of those are
// healthy. Only the magnitude separates that from a market price taken over
// no sales at all, so the asks are carried here rather than dropped: a
// reader that sees only Market cannot tell the two apart.
type Quote struct {
	// Market is the volume-weighted average of recent sales, and is the
	// figure Scryfall republishes as its USD price.
	Market float64
	// Low, Mid and High describe the live listings: the cheapest ask, the
	// midpoint, and the dearest. Low is what a buyer would actually pay.
	Low, Mid, High float64
}

// The two subtypes TCGplayer files a Magic listing under. One product id
// carries both, so a printing's foil and non-foil prices arrive as sibling
// rows rather than as separate products.
const (
	Normal = "Normal"
	Foil   = "Foil"
)

// allQuotes decodes a price list to productID → subtype → quote, keeping
// both subtypes apart.
//
// A row with no market price contributes nothing, as it always has: it is
// not a price.
func allQuotes(b []byte) (map[string]map[string]Quote, error) {
	var d priceRows
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("decoding tcgcsv prices: %w", err)
	}
	out := map[string]map[string]Quote{}
	for _, r := range d.Results {
		if r.MarketPrice <= 0 {
			continue
		}
		id := strconv.Itoa(r.ProductID)
		if out[id] == nil {
			out[id] = map[string]Quote{}
		}
		out[id][r.SubTypeName] = Quote{
			Market: r.MarketPrice,
			Low:    r.LowPrice,
			Mid:    r.MidPrice,
			High:   r.HighPrice,
		}
	}
	return out, nil
}

// foilQuotes reduces a price list to productID → quote, preferring each
// product's Foil row. A treated product usually lists only as Foil; its
// Normal row, when that is all there is, still describes the treated
// product, so it stands in.
//
// This is the overlay's view, where the product id already names the treated
// foil and the subtype is noise. A caller that needs to tell a printing's foil
// price from its non-foil one wants GroupQuotes instead — collapsing the two
// here would hand it the foil figure for both.
func foilQuotes(b []byte) (map[string]Quote, error) {
	all, err := allQuotes(b)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Quote, len(all))
	for id, bySub := range all {
		if q, ok := bySub[Foil]; ok {
			out[id] = q
			continue
		}
		for _, q := range bySub {
			out[id] = q
		}
	}
	return out, nil
}

// GroupPrices returns today's quote per product id for one group, Foil
// rows preferred.
func GroupPrices(ctx context.Context, o Options, groupID int) (map[string]Quote, error) {
	b, err := groupBody(ctx, o, groupID)
	if err != nil {
		return nil, err
	}
	return foilQuotes(b)
}

// GroupQuotes returns today's quotes for one group with the Normal and Foil
// subtypes kept apart, keyed product id → subtype. Same download and same day
// cache as GroupPrices, so asking for both costs one fetch.
func GroupQuotes(ctx context.Context, o Options, groupID int) (map[string]map[string]Quote, error) {
	b, err := groupBody(ctx, o, groupID)
	if err != nil {
		return nil, err
	}
	return allQuotes(b)
}

func groupBody(ctx context.Context, o Options, groupID int) ([]byte, error) {
	return get(ctx, o,
		fmt.Sprintf("%s/tcgplayer/%d/%d/prices", o.base(), magicCategory, groupID),
		fmt.Sprintf("prices-%d.json", groupID))
}

// archiveKeepDays bounds the immutable archive-extraction cache: the
// MTGJSON archive reaches back ninety days, so anything older can never
// be asked for again.
const archiveKeepDays = 100

// readArchiveMembers opens a downloaded archive and returns the bodies of
// the wanted members, keyed by member name; wanted members the archive
// lacks are simply absent. One pass in stored order, so a solid PPMd
// stream decodes once, not once per member. A var so tests can stub it —
// no Go PPMd writer exists to build a fixture archive with (the reading
// itself is proven against the live archives).
var readArchiveMembers = func(path string, want map[string]bool) (map[string][]byte, error) {
	r, err := sevenzip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", filepath.Base(path), err)
	}
	defer r.Close()
	out := map[string][]byte{}
	for _, f := range r.File {
		if !want[f.Name] {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("extracting %s: %w", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("extracting %s: %w", f.Name, err)
		}
		out[f.Name] = b
	}
	return out, nil
}

// ArchivePrices returns one past day's quote per product id for each
// requested group, from that day's archive. The archive downloads once
// (~4 MB) and only the requested groups' members are extracted and
// kept; the 7z itself is deleted after extraction. Groups absent from
// that day's archive come back missing, not as an error.
func ArchivePrices(ctx context.Context, o Options, date string, groupIDs []int) (map[int]map[string]Quote, error) {
	if o.CacheDir == "" {
		return nil, fmt.Errorf("archive reads need a cache directory")
	}
	dir := filepath.Join(o.CacheDir, "archive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	out := map[int]map[string]Quote{}
	var missing []int
	for _, gid := range groupIDs {
		path := filepath.Join(dir, fmt.Sprintf("%s-%d.json", date, gid))
		b, err := os.ReadFile(path)
		if err != nil {
			missing = append(missing, gid)
			continue
		}
		prices, err := foilQuotes(b)
		if err != nil {
			// A cache file that does not parse is not an answer, it is damage
			// (a torn write from before these were atomic, a bad disk). Kept,
			// it would stand in for this group-day forever — a permanent hole
			// in the treated-foil history — so it is deleted and re-fetched.
			os.Remove(path)
			missing = append(missing, gid)
			continue
		}
		out[gid] = prices
	}
	if len(missing) == 0 {
		return out, nil
	}

	archive := filepath.Join(dir, date+".7z")
	if _, err := os.Stat(archive); err != nil {
		b, err := get(ctx, o, fmt.Sprintf("%s/archive/tcgplayer/prices-%s.ppmd.7z", o.base(), date), "")
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(archive, b, 0o644); err != nil {
			return nil, err
		}
	}
	defer os.Remove(archive)
	want := map[string]bool{}
	byMember := map[string]int{}
	for _, gid := range missing {
		member := fmt.Sprintf("%s/%d/%d/prices", date, magicCategory, gid)
		want[member], byMember[member] = true, gid
	}
	members, err := readArchiveMembers(archive, want)
	if err != nil {
		return nil, err
	}
	for member, gid := range byMember {
		b := members[member]
		if len(b) == 0 {
			// This day's archive has no such group — remember the absence so
			// the next backfill does not re-download the archive to relearn it.
			writeFileAtomic(filepath.Join(dir, fmt.Sprintf("%s-%d.json", date, gid)), []byte("{}"))
			continue
		}
		prices, err := foilQuotes(b)
		if err != nil {
			// A member that does not parse is a bad archive day. Left uncached
			// so a later, repaired archive can still answer; caching it would
			// only feed the delete-and-refetch path above every run.
			continue
		}
		writeFileAtomic(filepath.Join(dir, fmt.Sprintf("%s-%d.json", date, gid)), b)
		out[gid] = prices
	}
	pruneArchive(dir)
	return out, nil
}

// writeFileAtomic lands b at path via a same-directory temp and rename — the
// mtgjson cache's pattern. The extraction files written with it are treated as
// authoritative by presence, so a ctrl-C or full disk mid-write must leave
// nothing rather than truncated JSON claiming to be that group-day's answer.
// Best-effort like the writes it replaces: the caller already holds the bytes,
// so a failed cache write costs a re-download, not the result.
func writeFileAtomic(path string, b []byte) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
	}
}

// pruneArchive removes extractions older than archiveKeepDays; their
// dates lead the filename, so age is a string comparison.
func pruneArchive(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -archiveKeepDays).Format("2006-01-02")
	for _, e := range entries {
		if name := e.Name(); len(name) >= 10 && name[:10] < cutoff {
			os.Remove(filepath.Join(dir, name))
		}
	}
}
