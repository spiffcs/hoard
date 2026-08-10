package catalog

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spiffcs/hoard/internal/buildinfo"
	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/progress"
)

// listingURL is Scryfall's bulk-data index: a few kilobytes describing each
// bundle and when it was last rebuilt. It is a var so tests can point at an
// httptest server.
var listingURL = "https://api.scryfall.com/bulk-data"

// bulkType is the bundle to build from.
//
// default_cards is one row per printing, in English or the printed language
// where there is no English version — which is exactly hoard's unit, since the
// collection is keyed on a printing's Scryfall id. oracle_cards would collapse
// printings together and all_cards would add every language for no gain.
const bulkType = "default_cards"

// checkInterval is how long a freshness check is trusted for.
//
// Scryfall rebuilds these bundles a few times a day, and a shell full of hoard
// invocations should not mean a request each. Six hours keeps the catalog within
// a build or two of current while making the listing call effectively free.
const checkInterval = 6 * time.Hour

// httpClient carries no whole-request timeout: the bundle is 77 MB and any
// fixed deadline is either too tight for a slow link or too slack to be worth
// setting. The transport does bound the header wait, so a server that accepts
// the connection and then goes silent fails in seconds — which matters because
// CheckStatus runs at the start of every update-prices, where a hang would
// block the command before it has done anything at all.
var httpClient = &http.Client{Transport: func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 30 * time.Second
	return t
}()}

// listingTimeout bounds the whole bulk-data index fetch. The listing is a few
// kilobytes; unlike the bundle it can afford a hard deadline, and CheckStatus
// promises "a failed check is silence" — a promise a hang would break.
const listingTimeout = 10 * time.Second

// Status describes the local catalog and, when it was worth asking, how it
// compares to what Scryfall publishes.
type Status struct {
	Cards         int
	Bytes         int64
	Built         time.Time
	SourceUpdated time.Time

	// Remote is the published bundle's timestamp; zero when the listing was not
	// consulted. Checked says which of those it is, so "not asked" is never
	// mistaken for "nothing newer".
	Remote  time.Time
	Checked bool
	Stale   bool
}

// Empty reports whether the catalog has never been built.
func (s Status) Empty() bool { return s.Cards == 0 }

// bundle is one entry in Scryfall's bulk-data index.
type bundle struct {
	Type           string `json:"type"`
	UpdatedAt      string `json:"updated_at"`
	DownloadURI    string `json:"jsonl_download_uri"`
	CompressedSize int64  `json:"compressed_size"`
}

// listing is the shape of Scryfall's bulk-data index, narrowed.
type listing struct {
	Data []bundle `json:"data"`
}

// Status reports the local catalog, checking Scryfall for a newer bundle when
// the last check has aged out.
//
// A failed check is silence, not an error. Being offline is an ordinary state
// for a tool whose point is working without the network, and a status command
// that errors because the network is down would be reporting on the wrong thing.
func (c *Catalog) CheckStatus(ctx context.Context) Status {
	s := Status{
		Cards:         c.CardCount(),
		Bytes:         c.Bytes(),
		Built:         c.built(),
		SourceUpdated: c.sourceUpdated(),
	}
	if time.Since(c.metaTime(keyChecked)) < checkInterval {
		return s
	}

	entry, err := c.listing(ctx)
	if err != nil {
		return s
	}
	remote, err := time.Parse(time.RFC3339, entry.UpdatedAt)
	if err != nil {
		return s
	}
	_ = c.setMeta(keyChecked, time.Now().UTC().Format(time.RFC3339))

	s.Remote, s.Checked = remote, true
	s.Stale = s.SourceUpdated.Before(remote)
	return s
}

// listing returns the bulk-data entry, fetched at most once per process and
// remembered on the Catalog.
func (c *Catalog) listing(ctx context.Context) (bundle, error) {
	if c.entry != nil {
		return *c.entry, nil
	}
	e, err := fetchListing(ctx)
	if err == nil {
		c.entry = &e
	}
	return e, err
}

// fetchListing reads the bulk-data index and returns the entry for bulkType.
func fetchListing(ctx context.Context) (bundle, error) {
	var zero bundle

	ctx, cancel := context.WithTimeout(ctx, listingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listingURL, nil)
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

	var l listing
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

// DownloadSize is what a rebuild would transfer, for asking before spending it.
// Zero when the listing could not be read.
func (c *Catalog) DownloadSize(ctx context.Context) int64 {
	e, err := c.listing(ctx)
	if err != nil {
		return 0
	}
	return e.CompressedSize
}

// bulkCard is a row of the bundle, narrowed to what the catalog stores.
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
	PromoTypes      []string `json:"promo_types"`
	FrameEffects    []string `json:"frame_effects"`
	Frame           string   `json:"frame"`
	BorderColor     string   `json:"border_color"`
	Colors          []string `json:"colors"`
	ColorIdentity   []string `json:"color_identity"`
	Games           []string `json:"games"`
	Prices          struct {
		USD       string `json:"usd"`
		USDFoil   string `json:"usd_foil"`
		USDEtched string `json:"usd_etched"`
	} `json:"prices"`
}

// Update rebuilds the catalog from Scryfall's current bundle, reporting
// byte progress against the listing's compressed size — the one figure known
// before the stream starts, which is what makes the minutes-long download
// determinate.
func (c *Catalog) Update(ctx context.Context, p progress.Fn) error {
	entry, err := c.listing(ctx)
	if err != nil {
		return err
	}
	// A build consumes the listing: whatever happens next — a freshness check,
	// a retry — should look at the world as it is then, not as it was when
	// this command started.
	defer func() { c.entry = nil }()

	// Built beside the real catalog under a temporary name and renamed into
	// place only on success. An interrupted download must not leave a partial
	// catalog that looks complete — every lookup would then quietly miss.
	//
	// The name is unique per build, and nothing here deletes anyone else's:
	// two updates can genuinely overlap (browse's catalog op plus a second
	// terminal's `hoard catalog update`), and a fixed name with a clear-away
	// delete let one unlink the other's 77 MB in-progress build minutes in,
	// surfacing as a corrupt-database error on the innocent side. Last rename
	// wins, which is fine — both were building the same bundle. A crash's
	// orphaned temp is swept by the next build once it is safely stale.
	pruneStaleBuilds(c.dir)
	tmpFile, err := os.CreateTemp(c.dir, fileName+".building-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	// SQLite treats an existing zero-length file as a new database, so the
	// handle only reserved the name.
	tmpFile.Close()
	tmp, err := openAt(c.dir, tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return err
	}

	cards, err := tmp.build(ctx, entry.DownloadURI, entry.CompressedSize, p)
	if err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for k, v := range map[string]string{
		keySourceUpdated: entry.UpdatedAt,
		keyBuilt:         now,
		keyChecked:       now,
		keyCards:         fmt.Sprint(cards),
	} {
		if err := tmp.setMeta(k, v); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Swap. The live handle has to let go of the file first, and is reopened
	// against whatever ends up there — including the old catalog, if the rename
	// fails, so a failed swap leaves a working catalog rather than none.
	if err := c.db.Close(); err != nil {
		return err
	}
	renameErr := os.Rename(tmpPath, c.path)
	reopened, err := openAt(c.dir, c.path)
	if err != nil {
		return fmt.Errorf("catalog: reopening after rebuild: %w", err)
	}
	c.db = reopened.db
	if renameErr != nil {
		return fmt.Errorf("catalog: replacing the catalog: %w", renameErr)
	}
	return nil
}

// staleBuildAge is how long a .building-* temp must sit untouched before it
// reads as abandoned. A live build writes its temp continuously, so an hour
// of silence only ever describes a crash's orphan.
const staleBuildAge = time.Hour

// pruneStaleBuilds sweeps build temps abandoned by a crash — at ~77 MB each
// they are worth collecting — while leaving any concurrent build's fresh temp
// alone (mtime, per staleBuildAge). Best-effort: a failed sweep costs disk,
// not correctness.
func pruneStaleBuilds(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleBuildAge)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), fileName+".building") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		os.Remove(filepath.Join(dir, e.Name()))
	}
}

// countingReader counts compressed bytes as they arrive, which is the only
// place the download's true position is knowable — after gzip the byte count
// no longer matches anything the listing promised.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// build streams the bundle into this (temporary) catalog and returns how many
// cards it stored.
func (c *Catalog) build(ctx context.Context, url string, size int64, p progress.Fn) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("downloading the card bundle: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("card bundle returned %d", resp.StatusCode)
	}

	// Counted before decompression, so Done measures the same thing the
	// listing's compressed size promises and the bar cannot overshoot 100%.
	cr := &countingReader{r: resp.Body}
	zr, err := gzip.NewReader(cr)
	if err != nil {
		return 0, fmt.Errorf("decompressing the card bundle: %w", err)
	}
	defer zr.Close()

	tx, err := c.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	insertCard, err := tx.Prepare(`
INSERT OR REPLACE INTO cards (scryfall_id, name, name_norm, set_code, collector_number,
    set_name, released_at, rarity, lang, finishes, promo_types, frame_effects, frame,
    border_color,
    colors, color_identity,
    price_usd, price_usd_foil, price_usd_etched, scryfall_url)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer insertCard.Close()

	// Names are collected in memory and written after the cards. There are far
	// fewer of them than printings, and deduplicating in Go avoids a SELECT per
	// row against a table that is still being written.
	names := map[string]string{}

	sc := bufio.NewScanner(zr)
	// Some cards — long oracle text, many faces, many rulings links — exceed the
	// scanner's 64 KB default, and a line that does not fit is a silent short
	// read rather than an error.
	sc.Buffer(make([]byte, 0, 256*1024), 4*1024*1024)

	var n int
	for sc.Scan() {
		// A cancelled build should stop promptly rather than at the end of a
		// 77 MB stream.
		if n%2000 == 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			default:
			}
			if n > 0 {
				p.Emit(progress.Event{Step: "downloading catalog",
					Done: cr.n, Total: size, Unit: progress.UnitBytes})
			}
		}

		line := trimJSONLine(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var bc bulkCard
		if err := json.Unmarshal(line, &bc); err != nil {
			// One malformed row is not worth abandoning the build for; the
			// catalog is advisory and a missing card falls through to the API.
			continue
		}
		if bc.ID == "" || !hasGame(bc.Games, "paper") {
			continue
		}

		norm := cardname.Normalize(bc.Name)
		if _, err := insertCard.Exec(bc.ID, bc.Name, norm, bc.Set, bc.CollectorNumber,
			nullable(bc.SetName), nullable(bc.ReleasedAt), nullable(bc.Rarity),
			nullable(bc.Lang), jsonArray(bc.Finishes), jsonArray(bc.PromoTypes), jsonArray(bc.FrameEffects),
			nullable(bc.Frame),
			nullable(bc.BorderColor),
			jsonArrayKeepEmpty(bc.Colors), jsonArrayKeepEmpty(bc.ColorIdentity),
			parsePrice(bc.Prices.USD), parsePrice(bc.Prices.USDFoil),
			parsePrice(bc.Prices.USDEtched), bc.ScryfallURI); err != nil {
			return 0, fmt.Errorf("storing %s: %w", bc.Name, err)
		}
		if norm != "" {
			names[norm] = bc.Name
		}
		n++
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("reading the card bundle: %w", err)
	}
	if n == 0 {
		return 0, fmt.Errorf("card bundle contained no paper cards")
	}

	if err := writeNames(tx, names); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

// writeNames stores the distinct names and the trigram index that generates
// fuzzy candidates.
//
// Written from a map accumulated during the card pass rather than by querying
// the cards table afterwards: many printings share a name, so the map is a
// fraction of the rows and the deduplication has already happened.
func writeNames(tx *sql.Tx, names map[string]string) error {
	insertName, err := tx.Prepare(`INSERT OR REPLACE INTO names (name_norm, name) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer insertName.Close()

	insertTri, err := tx.Prepare(`INSERT INTO name_trigrams (tri, name_norm) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer insertTri.Close()

	for norm, display := range names {
		if _, err := insertName.Exec(norm, display); err != nil {
			return fmt.Errorf("storing name %q: %w", display, err)
		}
		for _, tri := range cardname.Trigrams(norm) {
			if _, err := insertTri.Exec(tri, norm); err != nil {
				return fmt.Errorf("indexing name %q: %w", display, err)
			}
		}
	}
	return nil
}

// hasGame reports whether a card is available in the given game.
func hasGame(games []string, want string) bool {
	for _, g := range games {
		if g == want {
			return true
		}
	}
	return false
}

// trimJSONLine strips the array punctuation Scryfall's JSON (as opposed to
// JSONL) bundles carry, so either form parses.
func trimJSONLine(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '[') {
		b = b[1:]
	}
	for len(b) > 0 {
		c := b[len(b)-1]
		if c == ' ' || c == '\t' || c == '\r' || c == ',' || c == ']' {
			b = b[:len(b)-1]
			continue
		}
		break
	}
	if len(b) == 0 || b[0] != '{' {
		return nil
	}
	return b
}

// jsonArray stores a string slice the way Scryfall sends it, so callers read it
// back with json_each rather than by splitting a delimiter that could appear in
// a value.
func jsonArray(v []string) any {
	if len(v) == 0 {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

// jsonArrayKeepEmpty is jsonArray with nil and empty kept distinct: an
// absent field stores NULL (unknown), a present-but-empty one stores "[]".
// The color columns need the difference — a colorless card is not a card
// whose colors are unknown.
func jsonArrayKeepEmpty(v []string) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

// nullable stores an empty string as NULL, so "unknown" and "empty" stay
// distinguishable in the catalog the way they are in hoard.db.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// parsePrice converts Scryfall's decimal string into a nullable float.
func parsePrice(s string) any {
	if s == "" {
		return nil
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%g", &f); err != nil {
		return nil
	}
	return f
}
