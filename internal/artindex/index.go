package artindex

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
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
	variant string   // the image size the stored hashes were taken from
}

// algorithm names everything about how a stored hash was computed. Any change
// to the grid, the keep block, or the bit layout must change this string.
//
// It exists because a footprint mismatch is the worst failure this package
// has: hashes computed two different ways are still 256 comparable bits, so a
// mismatched index does not error — it silently returns confident nonsense at
// plausible-looking distances. Recording the footprint turns that into an
// empty index and a message saying to rebuild.
const algorithm = "dct32-keep16-nodc-v2"

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
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS meta (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`); err != nil {
		db.Close()
		return nil, err
	}
	ix := &Index{db: db}
	// Rows computed by a different footprint are not stale data to migrate,
	// they are noise: nothing can be recovered from a hash of a different
	// shape. Drop them and say so. This is also the upgrade path from the
	// 64-bit index, whose `hash` column was an INTEGER — there is no meta
	// table there at all, so it reads as a mismatch and gets rebuilt.
	stored, err := ix.meta("algorithm")
	if err != nil {
		db.Close()
		return nil, err
	}
	if stored != algorithm {
		if _, err := db.Exec(`DROP TABLE IF EXISTS hashes`); err != nil {
			db.Close()
			return nil, err
		}
		if err := ix.setMeta("algorithm", algorithm); err != nil {
			db.Close()
			return nil, err
		}
		if err := ix.setMeta("variant", ""); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS hashes (
		scryfall_id TEXT PRIMARY KEY,
		hash        BLOB NOT NULL,
		sole_finish TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		db.Close()
		return nil, err
	}
	if ix.variant, err = ix.meta("variant"); err != nil {
		db.Close()
		return nil, err
	}
	if err := ix.reload(); err != nil {
		db.Close()
		return nil, err
	}
	return ix, nil
}

func (ix *Index) meta(key string) (string, error) {
	var v string
	err := ix.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (ix *Index) setMeta(key, value string) error {
	_, err := ix.db.Exec(
		`INSERT OR REPLACE INTO meta (key, value) VALUES (?,?)`, key, value)
	return err
}

// Variant is the Scryfall image size the stored hashes were taken from, or ""
// for an index built before the variant was recorded. Reported by `artindex
// status` because it is not cosmetic: an index built from art_crop images
// cannot be compared against a hash of a whole-card scanner flatten.
func (ix *Index) Variant() string { return ix.variant }

// encodeHash and decodeHash move a Hash across the SQLite boundary as 32 big-
// endian bytes. Big-endian so that a hex dump of the column sorts and reads in
// the same bit order the hash was built in, which is what makes a row worth
// eyeballing during a footprint investigation.
func encodeHash(h Hash) []byte {
	b := make([]byte, hashWords*8)
	for i, w := range h {
		binary.BigEndian.PutUint64(b[i*8:], w)
	}
	return b
}

func decodeHash(b []byte) (Hash, bool) {
	var h Hash
	if len(b) != hashWords*8 {
		return h, false
	}
	for i := range h {
		h[i] = binary.BigEndian.Uint64(b[i*8:])
	}
	return h, true
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
		var raw []byte
		if err := rows.Scan(&id, &raw, &fin); err != nil {
			return err
		}
		h, ok := decodeHash(raw)
		if !ok {
			// A row of the wrong width is a row from another footprint that
			// slipped past the algorithm check. Skipping it beats loading it:
			// a wrong-length hash zero-pads into a plausible distance, and a
			// plausible distance is exactly what this channel must never
			// invent.
			continue
		}
		ix.ids = append(ix.ids, id)
		ix.hashes = append(ix.hashes, h)
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
	// Seeded above every reachable distance, so an empty or one-entry index
	// yields a runner-up no caller's margin test can clear.
	best, second = Match{Distance: maxDistance + 1}, Match{Distance: maxDistance + 1}
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

// BuildOptions configures a build. The zero value is the shipped behaviour:
// stream each small image from the CDN, hash it, discard the pixels.
type BuildOptions struct {
	// CacheDir keeps every downloaded image under it, so a later pass can
	// re-hash without the network. Empty disables the cache entirely —
	// which is what an installed CLI wants, since the corpus runs to
	// gigabytes and nobody opted into storing it.
	CacheDir string
	// Variant is the Scryfall image size to fetch: VariantSmall when empty.
	// Read the constants' comment before changing it — the choice is
	// coupled to the hash footprint, not just to picture quality.
	Variant string
}

func (o BuildOptions) variant() string {
	if o.Variant == "" {
		return VariantSmall
	}
	return o.Variant
}

// adoptVariant records the image size a build is about to use, clearing the
// table first if it differs from what the stored rows were taken from.
//
// Switching the source image is a footprint change like any other, and Build
// skips ids it already holds — so without this, an index half-built from
// `small` and half from `normal` would look complete while comparing rows that
// were never comparable. That is the same silent-nonsense failure the
// algorithm marker guards against, one level down.
//
// Called before the to-do list is computed, not inside the hashing loop: a
// wipe after the skip-list was built would strand every row it just deleted.
func (ix *Index) adoptVariant(variant string) error {
	if ix.variant != "" && ix.variant != variant {
		if _, err := ix.db.Exec(`DELETE FROM hashes`); err != nil {
			return err
		}
		if err := ix.reload(); err != nil {
			return err
		}
	}
	if err := ix.setMeta("variant", variant); err != nil {
		return err
	}
	ix.variant = variant
	return nil
}

// Build downloads and hashes every source not already in the index.
// Resumable by construction: present rows are skipped, so an interrupted
// build continues where it stopped. The images come from Scryfall's CDN —
// polite pacing rather than the API's per-endpoint budget, but still paced:
// docs/data-licensing.md's terms cover bulk image use.
//
// With a cache configured, images already on disk are neither re-fetched nor
// paced: the ticker exists to be kind to somebody else's servers, and a local
// read touches none of them. A fully cached build therefore runs at CPU
// speed rather than the three hours a cold one takes.
func (ix *Index) Build(ctx context.Context, srcs []Source, opts BuildOptions, progress func(done, total int)) error {
	if err := ix.adoptVariant(opts.variant()); err != nil {
		return err
	}
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
	return ix.hashEach(ctx, todo, opts, false, progress)
}

// Rehash re-derives every hash from the cached images, touching no network.
//
// This is the loop the cache is for. Changing the footprint — the keep block,
// FromCard's crop, the plane the hash reads — invalidates all ~107k rows at
// once, and Build cannot help because it skips exactly the rows that need
// redoing. Rehash rebuilds them in place from disk.
//
// Sources with no cached image are skipped rather than fetched: a rehash that
// silently turned into a download would take three hours and look like a bug.
// The count of skips is what the caller reports.
func (ix *Index) Rehash(ctx context.Context, srcs []Source, opts BuildOptions, progress func(done, total int)) (skipped int, err error) {
	if opts.CacheDir == "" {
		return 0, fmt.Errorf("rehash needs a cached image corpus; none is configured")
	}
	if err := ix.adoptVariant(opts.variant()); err != nil {
		return 0, err
	}
	cache := imageCache{dir: opts.CacheDir}
	var todo []Source
	for _, s := range srcs {
		if s.ImageURI == "" {
			continue
		}
		if cache.load(s.ScryfallID, opts.variant()) == nil {
			skipped++
			continue
		}
		todo = append(todo, s)
	}
	return skipped, ix.hashEach(ctx, todo, opts, true, progress)
}

// hashEach is the shared body of Build and Rehash: fetch (or read), hash,
// write the row. cacheOnly refuses the network, which is what makes a rehash
// provably local.
func (ix *Index) hashEach(ctx context.Context, todo []Source, opts BuildOptions, cacheOnly bool, progress func(done, total int)) error {
	cache := imageCache{dir: opts.CacheDir}
	variant := opts.variant()
	client := &http.Client{Timeout: 30 * time.Second}
	ins, err := ix.db.Prepare(`INSERT OR REPLACE INTO hashes (scryfall_id, hash, sole_finish) VALUES (?,?,?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	pace := time.NewTicker(100 * time.Millisecond) // ≤10 images/s
	defer pace.Stop()
	for i, s := range todo {
		if err := ctx.Err(); err != nil {
			return err
		}
		var b []byte
		if cacheOnly {
			b = cache.load(s.ScryfallID, variant)
			if b == nil {
				continue
			}
		} else {
			var fetched bool
			b, fetched, err = fetchImage(ctx, client, cache, s, variant)
			if err != nil {
				// One bad image is not worth abandoning a multi-hour build;
				// the row stays absent and the next build retries it.
				if fetched {
					// Only a real request owes the CDN a pause.
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-pace.C:
					}
				}
				continue
			}
			if fetched {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-pace.C:
				}
			}
		}
		img, _, err := image.Decode(bytes.NewReader(b))
		if err != nil {
			continue
		}
		if _, err := ins.Exec(s.ScryfallID, encodeHash(FromImage(img)), s.SoleFinish); err != nil {
			return err
		}
		if progress != nil {
			progress(i+1, len(todo))
		}
	}
	return ix.reload()
}
