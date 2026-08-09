package command

// The card-image fetch behind the detail overlay's picture. Bytes cache
// in the per-user cache directory — the same doctrine as the catalog and
// the MTGJSON bundles: re-downloadable data stays out of hoard.db, where
// the migration backup would copy it on every schema change.

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // Scryfall's normal scans are JPEG
	_ "image/png"  // and its png size is PNG
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spiffcs/hoard/internal/buildinfo"
)

// imageHTTP is the image fetch's own client: one decision about timeouts,
// separate from the API client because a stuck image download must never
// hold a price refresh hostage.
var imageHTTP = &http.Client{Timeout: 20 * time.Second}

// fetchCardImage returns one card's normal-size image, from the on-disk
// cache when it has it, Scryfall when it doesn't. Cache entries are
// immutable — a printing's scan does not change — so there is no
// freshness logic, only presence.
func fetchCardImage(ctx context.Context, scryfallID, url string) (image.Image, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "hoard", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, scryfallID+".img")

	if b, err := os.ReadFile(path); err == nil {
		if img, _, err := image.Decode(bytes.NewReader(b)); err == nil {
			return img, nil
		}
		// A corrupt cache entry falls through to a refetch that overwrites it.
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent)
	req.Header.Set("Accept", "image/jpeg, image/png, image/*")
	resp, err := imageHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image fetch: %s", resp.Status)
	}
	// A card scan is ~100 KB; buffering it whole keeps one copy of the
	// bytes serving both the decode and the cache write. maxImageBytes
	// bounds a misbehaving server.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes))
	if err != nil {
		return nil, err
	}

	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// Cache with the temp-write-then-rename the other caches use; a failed
	// write costs a refetch next time, never a torn file served as a scan.
	if tmp, err := os.CreateTemp(dir, "img-*"); err == nil {
		if _, err := tmp.Write(body); err == nil && tmp.Close() == nil {
			_ = os.Rename(tmp.Name(), path)
		} else {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}
	return img, nil
}

// maxImageBytes bounds one download; normal scans run ~100 KB, so 5 MB is
// generous headroom rather than a limit anyone should meet.
const maxImageBytes = 5 << 20
