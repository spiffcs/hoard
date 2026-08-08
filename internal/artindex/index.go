package artindex

import (
	"context"
	"database/sql"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

// Index is the persisted printing→hash table, queried by Hamming distance.
//
// Lookup is a linear scan over an in-memory slice, on purpose. 100k entries ×
// one XOR+popcount each is well under a millisecond — a BK-tree would save
// nothing measurable and cost a structure to maintain. Revisit only if the
// entry count grows by an order of magnitude.
type Index struct {
	db      *sql.DB
	ids     []string
	hashes  []Hash
	finishA []string // the printing's sole finish, "" when it has several
}

// Match is one candidate printing for a scanned image.
type Match struct {
	ScryfallID string
	Distance   int
}

// Open loads (creating if absent) the index beside the given directory,
// typically the catalog cache dir's sibling "artindex".
func Open(dir string) (*Index, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	// The busy timeout is what lets `artindex status` glance at the table
	// while a multi-hour build holds the write lock — without it the *build*
	// is the one that dies, mid-download, with SQLITE_BUSY (observed on the
	// first live build, 15,974 rows in).
	db, err := sql.Open("sqlite",
		filepath.Join(dir, "artindex.db")+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS hashes (
		scryfall_id TEXT PRIMARY KEY,
		hash        INTEGER NOT NULL,
		sole_finish TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		db.Close()
		return nil, err
	}
	ix := &Index{db: db}
	if err := ix.reload(); err != nil {
		db.Close()
		return nil, err
	}
	return ix, nil
}

func (ix *Index) reload() error {
	rows, err := ix.db.Query(`SELECT scryfall_id, hash, sole_finish FROM hashes`)
	if err != nil {
		return err
	}
	defer rows.Close()
	ix.ids, ix.hashes, ix.finishA = nil, nil, nil
	for rows.Next() {
		var id, fin string
		var h int64
		if err := rows.Scan(&id, &h, &fin); err != nil {
			return err
		}
		ix.ids = append(ix.ids, id)
		ix.hashes = append(ix.hashes, Hash(h))
		ix.finishA = append(ix.finishA, fin)
	}
	return rows.Err()
}

func (ix *Index) Close() error { return ix.db.Close() }

// Count is how many printings are hashed.
func (ix *Index) Count() int { return len(ix.ids) }

// Best returns the two closest printings — the winner and the runner-up the
// caller needs to judge ambiguity. A match is only evidence when the winner
// is close in absolute terms AND clearly ahead of the runner-up; that
// decision belongs to the caller's rank logic, so both distances cross.
func (ix *Index) Best(h Hash) (best, second Match) {
	best, second = Match{Distance: 65}, Match{Distance: 65}
	for i, ih := range ix.hashes {
		d := h.Distance(ih)
		switch {
		case d < best.Distance:
			second = best
			best = Match{ScryfallID: ix.ids[i], Distance: d}
		case d < second.Distance:
			second = Match{ScryfallID: ix.ids[i], Distance: d}
		}
	}
	return best, second
}

// SoleFinish is the matched printing's only finish, or "" when it has
// several — carried in the index so a match can settle foil without a
// catalog lookup.
func (ix *Index) SoleFinish(id string) string {
	for i, iid := range ix.ids {
		if iid == id {
			return ix.finishA[i]
		}
	}
	return ""
}

// Source is one printing to hash: what Build needs from the catalog.
type Source struct {
	ScryfallID string
	ImageURI   string
	SoleFinish string // "" when the printing has several finishes
}

// Build downloads and hashes every source not already in the index.
// Resumable by construction: present rows are skipped, so an interrupted
// build continues where it stopped. The images come from Scryfall's CDN —
// polite pacing rather than the API's per-endpoint budget, but still paced:
// docs/data-licensing.md's terms cover bulk image use.
func (ix *Index) Build(ctx context.Context, srcs []Source, progress func(done, total int)) error {
	have := make(map[string]bool, len(ix.ids))
	for _, id := range ix.ids {
		have[id] = true
	}
	var todo []Source
	for _, s := range srcs {
		if s.ImageURI != "" && !have[s.ScryfallID] {
			todo = append(todo, s)
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	ins, err := ix.db.Prepare(`INSERT OR REPLACE INTO hashes (scryfall_id, hash, sole_finish) VALUES (?,?,?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	pace := time.NewTicker(100 * time.Millisecond) // ≤10 images/s
	defer pace.Stop()
	for i, s := range todo {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-pace.C:
		}
		h, err := fetchHash(ctx, client, s.ImageURI)
		if err != nil {
			// One bad image is not worth abandoning a multi-hour build; the
			// row stays absent and the next build retries it.
			continue
		}
		if _, err := ins.Exec(s.ScryfallID, int64(h), s.SoleFinish); err != nil {
			return err
		}
		if progress != nil {
			progress(i+1, len(todo))
		}
	}
	return ix.reload()
}

func fetchHash(ctx context.Context, c *http.Client, url string) (Hash, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "hoard/artindex")
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("image fetch: %s", resp.Status)
	}
	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return 0, err
	}
	return FromImage(img), nil
}
