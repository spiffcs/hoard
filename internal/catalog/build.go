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

	"github.com/spiffcs/hoard/internal/boundedio"
	"github.com/spiffcs/hoard/internal/buildinfo"
	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/scryfall"
)

// ListingURL is where the catalog asks Scryfall which bulk file to fetch.
// It is a variable so a test can point the catalog at a local server.
var ListingURL = "https://api.scryfall.com/bulk-data"

const bulkType = "default_cards"

const checkInterval = 6 * time.Hour

var httpClient = &http.Client{Transport: func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 30 * time.Second
	return t
}()}

const listingTimeout = 10 * time.Second

type Status struct {
	Cards         int
	Bytes         int64
	Built         time.Time
	SourceUpdated time.Time

	Remote  time.Time
	Checked bool
	Stale   bool
}

func (s Status) Empty() bool { return s.Cards == 0 }

type bundle struct {
	Type           string `json:"type"`
	UpdatedAt      string `json:"updated_at"`
	DownloadURI    string `json:"jsonl_download_uri"`
	CompressedSize int64  `json:"compressed_size"`
}

type listing struct {
	Data []bundle `json:"data"`
}

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

func fetchListing(ctx context.Context) (bundle, error) {
	var zero bundle

	ctx, cancel := context.WithTimeout(ctx, listingTimeout)
	defer cancel()
	if err := scryfall.Pace(ctx, ListingURL); err != nil {
		return zero, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ListingURL, nil)
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

func (c *Catalog) DownloadSize(ctx context.Context) int64 {
	e, err := c.listing(ctx)
	if err != nil {
		return 0
	}
	return e.CompressedSize
}

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
	ImageURIs bulkImages `json:"image_uris"`
	CardFaces []struct {
		ImageURIs bulkImages `json:"image_uris"`
	} `json:"card_faces"`
}

type bulkImages struct {
	Normal string
}

func (c bulkCard) frontImage() string {
	if c.ImageURIs.Normal != "" {
		return c.ImageURIs.Normal
	}
	if len(c.CardFaces) > 0 {
		return c.CardFaces[0].ImageURIs.Normal
	}
	return ""
}

func (c *Catalog) Update(ctx context.Context, p progress.Fn) error {
	entry, err := c.listing(ctx)
	if err != nil {
		return err
	}

	defer func() { c.entry = nil }()

	pruneStaleBuilds(c.dir)
	tmpFile, err := os.CreateTemp(c.dir, fileName+".building-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

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

	c.mu.Lock()
	closeErr := c.db.Close()
	if closeErr != nil {
		c.mu.Unlock()
		return closeErr
	}
	renameErr := os.Rename(tmpPath, c.path)
	reopened, err := openAt(c.dir, c.path)
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("catalog: reopening after rebuild: %w", err)
	}
	c.db = reopened.db
	c.mu.Unlock()
	if renameErr != nil {
		return fmt.Errorf("catalog: replacing the catalog: %w", renameErr)
	}
	return nil
}

const staleBuildAge = time.Hour

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

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

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

	cr := &countingReader{r: resp.Body}

	bc := &boundedio.Counter{R: cr}
	zr, err := gzip.NewReader(bc)
	if err != nil {
		return 0, fmt.Errorf("decompressing the card bundle: %w", err)
	}
	defer zr.Close()

	bundle := boundedio.LimitRatio(zr, bc, "the card bundle")

	c.mu.RLock()
	tx, err := c.db.Begin()
	c.mu.RUnlock()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	insertCard, err := tx.Prepare(`
INSERT OR REPLACE INTO cards (scryfall_id, name, name_norm, set_code, collector_number,
    set_name, released_at, rarity, lang, finishes, promo_types, frame_effects, frame,
    border_color,
    colors, color_identity,
    price_usd, price_usd_foil, price_usd_etched, scryfall_url, image_uri)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer insertCard.Close()

	names := map[string]string{}

	sc := bufio.NewScanner(bundle)

	sc.Buffer(make([]byte, 0, 256*1024), 4*1024*1024)

	var n int
	for sc.Scan() {

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
			parsePrice(bc.Prices.USDEtched), bc.ScryfallURI,
			nullable(bc.frontImage())); err != nil {
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

func hasGame(games []string, want string) bool {
	for _, g := range games {
		if g == want {
			return true
		}
	}
	return false
}

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

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

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
