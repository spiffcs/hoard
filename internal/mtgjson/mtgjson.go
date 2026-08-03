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
	"bytes"
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
	"sync/atomic"
	"time"

	"github.com/spiffcs/hoard/internal/buildinfo"
)

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

// httpClient is shared across requests. It carries no whole-request timeout:
// the files here range from a sub-megabyte set file to the ~150 MB price
// archive, and any single deadline is either too tight for the archive on a
// slow link or too slack to be worth setting for the rest. The transport does
// bound the header wait, so a server that accepts the connection and then goes
// silent fails in seconds instead of hanging a command that has printed nothing.
var httpClient = &http.Client{Transport: headerBoundedTransport()}

// headerBoundedTransport is the default transport with a response-header
// deadline — the one timeout that is safe for arbitrarily large bodies.
func headerBoundedTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 30 * time.Second
	return t
}

// today is a var so tests can pin the cache key.
var today = func() string { return time.Now().Format("2006-01-02") }

// Options configures a fetch: where downloads cache, and who watches the
// bytes arrive.
type Options struct {
	// CacheDir, when non-empty, keeps downloaded files so repeated runs on
	// the same day don't re-fetch them.
	CacheDir string
	// Progress receives cumulative downloaded bytes against the response's
	// total (0 when the server did not say). Only a genuine download
	// reports; a cache hit is instant and silent. Nil is silent.
	Progress func(done, total int64)
}

// progressWriter tees byte counts to Options.Progress as a download lands.
type progressWriter struct {
	w           io.Writer
	done, total int64
	fn          func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	p.fn(p.done, p.total)
	return n, err
}

// idleAfter is how long a download may deliver zero bytes before it is
// declared stalled. The transport already bounds the header wait; this
// bounds the body — the ~150 MB archive on a connection that silently died
// used to hang forever, printing nothing. Sixty seconds of literally no
// bytes is not a slow link, it is a dead one. A var so tests can shrink it.
var idleAfter = 60 * time.Second

// idleReader fails a read that has produced no bytes for idleAfter, by
// closing the underlying body from a watchdog timer — the only way to
// unblock a Read parked inside the transport.
type idleReader struct {
	r       io.ReadCloser
	timer   *time.Timer
	wait    time.Duration
	stalled atomic.Bool
}

func newIdleReader(r io.ReadCloser, wait time.Duration) *idleReader {
	ir := &idleReader{r: r, wait: wait}
	ir.timer = time.AfterFunc(wait, func() {
		ir.stalled.Store(true)
		r.Close()
	})
	return ir
}

func (ir *idleReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if err != nil && ir.stalled.Load() {
		return n, fmt.Errorf("mtgjson download stalled: no data for %s; check the connection and try again", ir.wait)
	}
	if n > 0 {
		ir.timer.Reset(ir.wait)
	}
	return n, err
}

func (ir *idleReader) Close() error {
	ir.timer.Stop()
	return ir.r.Close()
}

// fetch returns the bytes of one MTGJSON file, from the cache when possible.
// The caller closes the returned reader.
//
// cacheDir, when non-empty, keeps downloaded files so repeated runs on the
// same day don't re-fetch them. This matters because a card no source can
// price stays a gap forever, and without a cache every single update-prices
// would re-download the whole bundle chasing it. MTGJSON rebuilds nightly, so
// entries are keyed by date and yesterday's are simply never read again.
func fetch(ctx context.Context, o Options, name string) (io.ReadCloser, error) {
	var cachePath string
	if o.CacheDir != "" {
		cachePath = filepath.Join(o.CacheDir, today()+"-"+name)
		if f, err := os.Open(cachePath); err == nil {
			if gzipMagic(f) {
				return f, nil
			}
			// A cached entry that is not gzip is a poisoned file — a captive
			// portal or maintenance page that arrived with a 200 on some
			// earlier run. Served as-is it would wedge every price command
			// until midnight with "gzip: invalid header"; deleted, this run
			// simply downloads again.
			f.Close()
			os.Remove(cachePath)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent)
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
	body := newIdleReader(resp.Body, idleAfter)
	if cachePath == "" {
		return body, nil
	}

	// Write through to the cache, then serve from it. A partial download must
	// not be left behind looking complete, so write to a temp file and rename.
	// Caching is best-effort: if the cache directory cannot be used, the live
	// body is returned instead — which is why the close cannot be deferred
	// here; it would fire before the caller ever read from that body.
	if err := os.MkdirAll(o.CacheDir, 0o755); err != nil {
		return body, nil //nolint:nilerr // caching is best-effort
	}
	pruneCache(o.CacheDir)
	tmp, err := os.CreateTemp(o.CacheDir, "dl-*")
	if err != nil {
		return body, nil //nolint:nilerr // caching is best-effort
	}
	defer body.Close()
	dst := io.Writer(tmp)
	if o.Progress != nil {
		// ContentLength is -1 when unknown; 0 means indeterminate here.
		dst = &progressWriter{w: tmp, total: max(resp.ContentLength, 0), fn: o.Progress}
	}
	if _, err := io.Copy(dst, body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	tmp.Close()
	// Never cache what isn't gzip: a 200 carrying an interstitial page would
	// otherwise become today's bundle and every later command would fail on it.
	if bad, err := os.Open(tmp.Name()); err == nil {
		ok := gzipMagic(bad)
		bad.Close()
		if !ok {
			os.Remove(tmp.Name())
			return nil, fmt.Errorf("mtgjson %s: server sent a non-gzip response (an outage page?); try again shortly", name)
		}
	}
	if err := os.Rename(tmp.Name(), cachePath); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	return os.Open(cachePath)
}

// gzipMagic reports whether f begins with the gzip magic bytes, leaving the
// offset back at the start either way. Every file this package fetches is
// gzip, so anything else is a wrong answer no matter what the status code said.
func gzipMagic(f *os.File) bool {
	var hdr [2]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return false
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false
	}
	return hdr[0] == 0x1f && hdr[1] == 0x8b
}

// pruneCache deletes entries from previous days. Without it the cache grows by
// the size of a full bundle every day it is used, and yesterday's files are
// never read again anyway. Best-effort: failures are not worth reporting.
func pruneCache(cacheDir string) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	prefix := today() + "-"
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			os.Remove(filepath.Join(cacheDir, e.Name()))
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
func SetIdentifiers(ctx context.Context, o Options, setCode string) (map[string]string, error) {
	body, err := fetch(ctx, o, strings.ToUpper(setCode)+".json.gz")
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
func TodayQuotes(ctx context.Context, o Options, want map[string]bool) (map[string][]Quote, error) {
	out := map[string][]Quote{}
	err := streamPrices(ctx, o, todayFile, want, func(uuid string, rec priceRecord) {
		var qs []Quote
		for _, name := range providerOrder {
			v, ok := rec.Paper[name]
			if !ok || v.Currency != "USD" {
				continue
			}
			// Fixed slices, not map literals: ranging a map would shuffle the
			// quote order per run, and arbitrage breaks price ties by position —
			// two vendors at the same price would give different advice run to run.
			for _, k := range []struct {
				kind string
				side byFinish
			}{{Retail, v.Retail}, {Buylist, v.Buylist}} {
				for _, f := range []struct {
					finish string
					byDate map[string]float64
				}{{"normal", k.side.Normal}, {"foil", k.side.Foil}} {
					if p := latest(f.byDate); p != nil {
						qs = append(qs, Quote{
							Provider: name, Kind: k.kind, Finish: f.finish, Price: *p,
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
// The archive decodes to over a gigabyte covering every card in Magic, and
// walking it with encoding/json's tokenizer cost ~30 seconds of a 31-second
// backfill (profiled: disk 0.02s, gunzip 2s, token scan 30s). The wanted
// keys are searched for directly in the decompressed bytes instead —
// bytes.Index runs at memory speed — and only their records are decoded.
// The scan also stops as soon as the last wanted key is found, so on
// average half the archive is never even decompressed.
func streamPrices(ctx context.Context, o Options, file string, want map[string]bool, visit func(string, priceRecord)) error {
	if len(want) == 0 {
		return nil
	}
	body, err := fetch(ctx, o, file)
	if err != nil {
		return fmt.Errorf("fetching prices: %w", err)
	}
	defer body.Close()

	zr, err := gzip.NewReader(body)
	if err != nil {
		return fmt.Errorf("decompressing prices: %w", err)
	}
	defer zr.Close()

	return scanKeyedObjects(zr, want, func(uuid string, raw []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var rec priceRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return fmt.Errorf("decoding prices for %s: %w", uuid, err)
		}
		visit(uuid, rec)
		return nil
	})
}

// scanKeyedObjects streams r looking for `"<key>":{...}` for each wanted
// key, handing the raw object bytes to visit. Keys are matched as
// quote-key-quote-colon, which only a JSON key position can produce; the
// wanted keys are UUIDs, which never occur as keys inside a price record
// (those are vendors, finishes and dates), so a match is the record wanted.
// Returns once every key is found or the stream ends — absent keys are
// simply never visited, matching the tokenizer's behavior.
func scanKeyedObjects(r io.Reader, want map[string]bool, visit func(string, []byte) error) error {
	// Per-needle searching costs one pass over the stream per wanted key.
	// A handful of keys beats everything; a whole collection would re-scan
	// the gigabyte once per card. MTGJSON keys are fixed-length UUIDs, so
	// past a small count the colon-anchored single pass wins — flat cost
	// however large the hoard grows.
	if len(want) > setScanMin {
		if kl := uniformKeyLen(want); kl > 0 {
			return scanKeyedObjectsSet(r, want, kl, visit)
		}
	}
	type needle struct {
		key string
		pat []byte
	}
	needles := make([]needle, 0, len(want))
	maxPat := 0
	for k := range want {
		n := needle{key: k, pat: []byte(`"` + k + `":`)}
		needles = append(needles, n)
		maxPat = max(maxPat, len(n.pat))
	}

	const chunk = 4 << 20
	buf := make([]byte, 0, chunk*2)
	tmp := make([]byte, chunk)
	scanned := 0  // buf[:scanned] has been searched for needle starts
	pending := -1 // start of a matched needle whose record is still arriving
	for len(needles) > 0 {
		n, rerr := r.Read(tmp)
		buf = append(buf, tmp[:n]...)

		// Search the fresh region, backed up so a needle split across the
		// read boundary is still seen whole; a pending match re-scans from
		// its own start so its record is retried as bytes arrive.
		from := max(scanned-maxPat+1, 0)
		if pending >= 0 && pending < from {
			from = pending
		}
		pending = -1
		for i := 0; i < len(needles); {
			nd := needles[i]
			at := bytes.Index(buf[from:], nd.pat)
			if at < 0 {
				i++
				continue
			}
			start := from + at
			objStart := start + len(nd.pat)
			for objStart < len(buf) && (buf[objStart] == ' ' || buf[objStart] == '\n' || buf[objStart] == '\t') {
				objStart++
			}
			end := scanJSONValue(buf, objStart)
			if end < 0 {
				// Still arriving: remember the earliest such start so the
				// trim below keeps it, and let other needles keep looking.
				if pending < 0 || start < pending {
					pending = start
				}
				i++
				continue
			}
			if err := visit(nd.key, buf[objStart:end]); err != nil {
				return err
			}
			needles[i] = needles[len(needles)-1]
			needles = needles[:len(needles)-1]
		}
		scanned = len(buf)

		// Trim consumed bytes: keep a needle-sized tail for boundary
		// splits, and everything from a pending match onward.
		keep := len(buf) - (maxPat - 1)
		if pending >= 0 && pending < keep {
			keep = pending
		}
		if keep > 0 {
			buf = append(buf[:0], buf[keep:]...)
			scanned -= keep
			if pending >= 0 {
				pending -= keep
			}
		}

		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			return fmt.Errorf("reading prices: %w", rerr)
		}
	}
	return nil
}

// setScanMin is the wanted-key count above which the single-pass set scan
// replaces per-needle searching; a variable so tests can force either path.
var setScanMin = 24

// uniformKeyLen returns the shared length of every wanted key, or 0 when
// they differ — the set scan anchors on fixed-width keys.
func uniformKeyLen(want map[string]bool) int {
	kl := 0
	for k := range want {
		if kl == 0 {
			kl = len(k)
		} else if len(k) != kl {
			return 0
		}
	}
	return kl
}

// scanKeyedObjectsSet is the one-pass form: every `":` in the stream is a
// key boundary candidate; the fixed-width key just before it is looked up
// in the set. One pass whatever the collection size, still with the
// early exit once everything wanted is found.
func scanKeyedObjectsSet(r io.Reader, want map[string]bool, keyLen int, visit func(string, []byte) error) error {
	found := make(map[string]bool, len(want))
	remaining := len(want)
	// `"key":` spans keyLen+3 bytes; the tail kept across reads must cover a
	// pattern split anywhere.
	pat := keyLen + 3

	const chunk = 4 << 20
	buf := make([]byte, 0, chunk*2)
	tmp := make([]byte, chunk)
	scanned := 0
	pending := -1
	for remaining > 0 {
		n, rerr := r.Read(tmp)
		buf = append(buf, tmp[:n]...)

		from := max(scanned-pat+1, 0)
		if pending >= 0 && pending < from {
			from = pending
		}
		pending = -1
		for i := from; i < len(buf); {
			rel := bytes.IndexByte(buf[i:], ':')
			if rel < 0 {
				break
			}
			j := i + rel
			i = j + 1
			// A wanted key here reads `"<key>":` ending at j.
			ks := j - keyLen - 1
			if ks < 1 || buf[j-1] != '"' || buf[ks-1] != '"' {
				continue
			}
			key := buf[ks : j-1]
			if !want[string(key)] || found[string(key)] {
				continue
			}
			objStart := j + 1
			for objStart < len(buf) && (buf[objStart] == ' ' || buf[objStart] == '\n' || buf[objStart] == '\t') {
				objStart++
			}
			end := scanJSONValue(buf, objStart)
			if end < 0 {
				if pending < 0 {
					pending = ks - 1
				}
				break
			}
			if err := visit(string(key), buf[objStart:end]); err != nil {
				return err
			}
			found[string(key)] = true
			remaining--
			if remaining == 0 {
				return nil
			}
		}
		scanned = len(buf)

		keep := len(buf) - (pat - 1)
		if pending >= 0 && pending < keep {
			keep = pending
		}
		if keep > 0 {
			buf = append(buf[:0], buf[keep:]...)
			scanned -= keep
			if pending >= 0 {
				pending -= keep
			}
		}

		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			return fmt.Errorf("reading prices: %w", rerr)
		}
	}
	return nil
}

// scanJSONValue returns the index just past the JSON value starting at
// buf[i], or -1 when the value runs past the buffer. String-aware, so a
// brace inside a quoted value cannot end the object early.
func scanJSONValue(buf []byte, i int) int {
	if i >= len(buf) {
		return -1
	}
	if buf[i] != '{' && buf[i] != '[' {
		// A scalar record would be malformed for this file; refuse rather
		// than guess where it ends.
		return -1
	}
	depth, inStr, esc := 0, false, false
	for j := i; j < len(buf); j++ {
		c := buf[j]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return j + 1
			}
		}
	}
	return -1
}

// TodayPrices returns USD paper retail prices for the requested UUIDs.
//
// AllPricesToday is ~5 MB gzipped but around 50 MB decoded and covers every card
// in Magic, so it is streamed and filtered against `want` rather than decoded
// whole. Only UUIDs asked for are retained.
func TodayPrices(ctx context.Context, o Options, want map[string]bool) (map[string]Price, error) {
	out := make(map[string]Price, len(want))
	err := streamPrices(ctx, o, todayFile, want, func(uuid string, rec priceRecord) {
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

// historyProvider is the only vendor whose retail back catalogue is read.
//
// Scryfall's USD prices come from TCGplayer alone, so this series is the one
// that is continuous with the prices hoard already holds. Splicing Card Kingdom
// or Manapool onto the front of a Scryfall series would put a vendor's markup at
// the join and read as a real price movement on the day the two meet.
const historyProvider = "tcgplayer"

// bidProvider is the only vendor whose buylist back catalogue is read —
// Card Kingdom runs the only buylist in the feed, so its series is the bid
// history, not a choice among several.
const bidProvider = "cardkingdom"

// CardHistory is one card's dated archive series, both sides of the
// counter: what TCGplayer's market said it traded at, and what Card
// Kingdom offered for it.
type CardHistory struct {
	Retail []Observation
	Bids   []Observation
}

// PriceHistory returns every dated USD observation MTGJSON holds for the
// requested UUIDs — about 90 days' worth, both finishes, retail and bid.
//
// This reads AllPrices rather than AllPricesToday: ~150 MB on the wire against
// 5 MB, and thirty times the rows, which is why nothing calls it on a schedule
// — and why both sides come back from the one pass rather than two.
// Observations come back in no particular order; callers that care sort them.
func PriceHistory(ctx context.Context, o Options, want map[string]bool) (map[string]CardHistory, error) {
	out := make(map[string]CardHistory, len(want))
	side := func(v vendor, prices byFinish, source string) []Observation {
		if v.Currency != "USD" {
			return nil
		}
		var obs []Observation
		for finish, byDate := range map[string]map[string]float64{
			"normal": prices.Normal, "foil": prices.Foil,
		} {
			for date, price := range byDate {
				obs = append(obs, Observation{
					Date: date, Finish: finish, Price: price, Source: source,
				})
			}
		}
		return obs
	}
	err := streamPrices(ctx, o, archiveFile, want, func(uuid string, rec priceRecord) {
		var h CardHistory
		if v, ok := rec.Paper[historyProvider]; ok {
			h.Retail = side(v, v.Retail, historyProvider)
		}
		if v, ok := rec.Paper[bidProvider]; ok {
			h.Bids = side(v, v.Buylist, bidProvider)
		}
		if len(h.Retail) > 0 || len(h.Bids) > 0 {
			out[uuid] = h
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// bestUSD resolves each finish independently, walking providerOrder until a
// vendor quotes that finish in USD.
//
// Per finish, not per vendor: TCGplayer lists a non-foil price for the MH3 ripple
// foils but no foil one, so stopping at the first vendor with *any* price yields a
// row that looks filled and values the card at nothing.
//
// The cost is that a card's two finishes can come from different shops. They are
// independent numbers and Source records both.
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
