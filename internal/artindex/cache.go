package artindex

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// imageCache keeps the source images Build hashes, so re-hashing the catalog
// costs disk reads instead of a second pass over Scryfall's CDN.
//
// Why this exists: the hash footprint is not settled. FromCard's crop, the
// keep-block size, and a possible chroma-plane second hash are all open
// questions, and each answer means re-hashing all ~107k printings. Without a
// cache that is a fresh multi-hour download every time — the build is paced at
// ten images a second by policy, so the wall clock is fixed no matter how fast
// the link is. With one, the second and every later pass are local.
//
// The cache is opt-in and empty by default. `hoard artindex build` on a user's
// machine streams and discards exactly as before; only a developer who sets
// the cache directory pays the disk. That asymmetry is deliberate: nobody
// installing the CLI asked to store ten gigabytes of card scans.
type imageCache struct {
	dir string
}

// path is where one printing's image lives: two levels of hex sharding taken
// from the id, mirroring Scryfall's own CDN layout. Flat would put 107k files
// in one directory, which APFS tolerates and `ls` does not.
func (c imageCache) path(id, variant string) string {
	if c.dir == "" || len(id) < 2 {
		return ""
	}
	return filepath.Join(c.dir, variant, id[0:1], id[1:2], id+".jpg")
}

// load returns the cached bytes for a printing, or nil when it is not cached.
// A read error is a miss, not a failure: a corrupt or half-written file should
// cost one re-download, not the build.
func (c imageCache) load(id, variant string) []byte {
	p := c.path(id, variant)
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return b
}

// store writes the bytes for a printing, atomically.
//
// Atomic because a build is interrupted routinely — it runs for three hours
// and Ctrl-C is the normal way to stop it. A truncated file left at the final
// path would be read as authoritative by the next run and hash to garbage,
// and the garbage would be indistinguishable from a real hash. Write to a
// temp name in the same directory, then rename: on every filesystem hoard
// targets, a rename within a directory is atomic, so a reader sees the whole
// file or no file.
//
// A store failure is reported but never fatal to the caller — the image is
// already in hand and hashing it is what matters.
func (c imageCache) store(id, variant string, b []byte) error {
	p := c.path(id, variant)
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-"+id+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeds
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Variants are the Scryfall image sizes Build can be pointed at. The catalog
// stores only the small URL (internal/catalog/build.go's smallImage), so the
// others are derived by rewriting one path segment — see variantURI.
//
// Which one to cache is a real decision, not a preference:
//
//   - VariantSmall (~13KB, 1.4GB for the catalog) is what shipped. The card is
//     whole, so FromCard's fixed-fraction crop applies, but its art region is
//     only about 123x98 pixels.
//   - VariantNormal (~91KB, 9.7GB) is the same whole card at 488x680. The same
//     crop applies unchanged, with room under it for a chroma-plane hash or a
//     different footprint later.
//   - VariantArtCrop (~66KB, 7.0GB) is Scryfall's own crop to the art box.
//     Sharper art per byte, and it does NOT compose with FromCard: the crop
//     geometry is Scryfall's, varies by frame, and cannot be reproduced from a
//     scanner flatten — so the index side and the phone side would stop
//     agreeing on what they hash. Use it only with a footprint that hashes the
//     art box on both ends.
//
// All four cost the same wall clock. Build is paced at ten images a second, so
// even the largest is a few megabits — the ticker sets the runtime, not the
// bytes.
const (
	VariantSmall   = "small"
	VariantNormal  = "normal"
	VariantArtCrop = "art_crop"
	VariantLarge   = "large"
)

// variantURI rewrites a Scryfall image URL to another size.
//
// The URLs are structural: https://cards.scryfall.io/<size>/front/a/b/<id>.jpg
// — the size is the first path segment and nothing else in the URL depends on
// it. Rewriting beats storing four columns in the catalog, which would mean a
// schema change and a full catalog rebuild to get pixels we can already
// address.
//
// An unrecognised shape is returned unchanged rather than guessed at: a
// mangled URL 404s at ten images a second for three hours, and a URL that
// still points at the small image merely hashes the wrong size, which the
// footprint check catches.
func variantURI(uri, variant string) string {
	if variant == "" || variant == VariantSmall {
		return uri
	}
	i := strings.Index(uri, "://")
	if i < 0 {
		return uri
	}
	host := i + 3
	j := strings.Index(uri[host:], "/")
	if j < 0 {
		return uri
	}
	rest := uri[host+j:] // "/small/front/a/b/id.jpg?123"
	seg := path.Clean(rest)
	parts := strings.SplitN(strings.TrimPrefix(seg, "/"), "/", 2)
	if len(parts) != 2 {
		return uri
	}
	switch parts[0] {
	case VariantSmall, VariantNormal, VariantArtCrop, VariantLarge, "png", "border_crop":
	default:
		return uri // not a size segment; leave it alone
	}
	return uri[:host+j] + "/" + variant + "/" + parts[1]
}

// fetchImage returns one printing's image bytes, from the cache when it is
// there and from the CDN when it is not. The bool reports whether the network
// was used, which is what lets Build pace only real requests: a cached build
// has no reason to sit behind a politeness ticker aimed at somebody else's
// servers.
func fetchImage(ctx context.Context, c *http.Client, cache imageCache, s Source, variant string) ([]byte, bool, error) {
	if b := cache.load(s.ScryfallID, variant); b != nil {
		return b, false, nil
	}
	uri := variantURI(s.ImageURI, variant)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "hoard/artindex")
	resp, err := c.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, true, fmt.Errorf("image fetch: %s", resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	return b, true, cache.store(s.ScryfallID, variant, b)
}
