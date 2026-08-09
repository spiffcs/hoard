package artindex

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestVariantURI(t *testing.T) {
	const small = "https://cards.scryfall.io/small/front/b/d/bd8fa327-dd41-4737-8f19-2cf5eb1f7cdd.jpg?1783939332"
	tests := []struct {
		name    string
		uri     string
		variant string
		want    string
	}{
		{
			name:    "small is the identity",
			uri:     small,
			variant: VariantSmall,
			want:    small,
		},
		{
			name:    "empty variant leaves the URL alone",
			uri:     small,
			variant: "",
			want:    small,
		},
		{
			name:    "normal rewrites only the size segment",
			uri:     small,
			variant: VariantNormal,
			want:    "https://cards.scryfall.io/normal/front/b/d/bd8fa327-dd41-4737-8f19-2cf5eb1f7cdd.jpg?1783939332",
		},
		{
			name:    "art_crop rewrites only the size segment",
			uri:     small,
			variant: VariantArtCrop,
			want:    "https://cards.scryfall.io/art_crop/front/b/d/bd8fa327-dd41-4737-8f19-2cf5eb1f7cdd.jpg?1783939332",
		},
		{
			// A URL whose first segment is not a known size is left alone
			// rather than mangled: a wrong URL 404s at ten images a second
			// for three hours.
			name:    "unknown first segment is left alone",
			uri:     "https://example.test/images/front/b/d/x.jpg",
			variant: VariantNormal,
			want:    "https://example.test/images/front/b/d/x.jpg",
		},
		{
			name:    "a URL with no path is left alone",
			uri:     "https://cards.scryfall.io",
			variant: VariantNormal,
			want:    "https://cards.scryfall.io",
		},
		{
			name:    "a non-URL is left alone",
			uri:     "not a url",
			variant: VariantNormal,
			want:    "not a url",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := variantURI(tt.uri, tt.variant); got != tt.want {
				t.Errorf("variantURI(%q, %q) = %q, want %q", tt.uri, tt.variant, got, tt.want)
			}
		})
	}
}

func TestImageCacheRoundTrip(t *testing.T) {
	c := imageCache{dir: t.TempDir()}
	const id = "bd8fa327-dd41-4737-8f19-2cf5eb1f7cdd"
	want := []byte("pretend jpeg")

	if got := c.load(id, VariantNormal); got != nil {
		t.Fatalf("load on an empty cache = %q, want nil", got)
	}
	if err := c.store(id, VariantNormal, want); err != nil {
		t.Fatalf("store: %v", err)
	}
	if got := c.load(id, VariantNormal); !bytes.Equal(got, want) {
		t.Errorf("load = %q, want %q", got, want)
	}
	// Variants are stored apart: switching the source must not silently read
	// pixels of the wrong size and hash them as if they were right.
	if got := c.load(id, VariantSmall); got != nil {
		t.Errorf("load of a different variant = %q, want nil", got)
	}
	// Sharded, not flat — 107k files in one directory is what this avoids.
	if _, err := os.Stat(filepath.Join(c.dir, VariantNormal, "b", "d", id+".jpg")); err != nil {
		t.Errorf("sharded path not written: %v", err)
	}
	// No temp files survive a successful store.
	ents, err := os.ReadDir(filepath.Join(c.dir, VariantNormal, "b", "d"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Errorf("directory holds %d entries, want 1 (a temp file leaked)", len(ents))
	}
}

func TestImageCacheDisabled(t *testing.T) {
	// The zero cache is what an installed CLI uses: no directory, no writes,
	// no error. Nobody who installed hoard asked to store ten gigabytes.
	var c imageCache
	if err := c.store("bd8fa327", VariantNormal, []byte("x")); err != nil {
		t.Errorf("store on a disabled cache = %v, want nil", err)
	}
	if got := c.load("bd8fa327", VariantNormal); got != nil {
		t.Errorf("load on a disabled cache = %q, want nil", got)
	}
}

// jpegBytes is a tiny valid JPEG, so the decode in hashEach has something real
// to chew on.
func jpegBytes(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{R: shade, G: uint8(x * 4), B: uint8(y * 4), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFetchImageCachesAndThenServesLocally(t *testing.T) {
	want := jpegBytes(t, 200)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(want)
	}))
	defer srv.Close()

	cache := imageCache{dir: t.TempDir()}
	src := Source{ScryfallID: "bd8fa327-dd41", ImageURI: srv.URL + "/small/front/b/d/x.jpg"}

	got, fetched, err := fetchImage(context.Background(), srv.Client(), cache, src, VariantSmall)
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if !fetched {
		t.Error("first fetch reported no network use")
	}
	if !bytes.Equal(got, want) {
		t.Error("first fetch returned the wrong bytes")
	}

	// Second call must be local. This is the whole point of the cache: a
	// re-hash costs disk reads, not another pass over Scryfall's CDN.
	got, fetched, err = fetchImage(context.Background(), srv.Client(), cache, src, VariantSmall)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if fetched {
		t.Error("second fetch used the network despite a warm cache")
	}
	if !bytes.Equal(got, want) {
		t.Error("second fetch returned the wrong bytes")
	}
	if hits != 1 {
		t.Errorf("server saw %d requests, want 1", hits)
	}
}

func TestFetchImageWithoutCacheAlwaysFetches(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(jpegBytes(t, 100))
	}))
	defer srv.Close()

	src := Source{ScryfallID: "bd8fa327-dd41", ImageURI: srv.URL + "/small/front/b/d/x.jpg"}
	for i := range 2 {
		if _, _, err := fetchImage(context.Background(), srv.Client(), imageCache{}, src, VariantSmall); err != nil {
			t.Fatalf("fetch %d: %v", i, err)
		}
	}
	if hits != 2 {
		t.Errorf("server saw %d requests, want 2 — the disabled cache stored something", hits)
	}
}

func TestFetchImageReportsNetworkUseOnError(t *testing.T) {
	// The bool is what tells Build whether it owes the CDN a pause. A 404 was
	// still a real request, so it must report true even though it errored —
	// otherwise a run of bad URLs hammers the server unpaced.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	src := Source{ScryfallID: "bd8fa327-dd41", ImageURI: srv.URL + "/small/front/b/d/x.jpg"}
	_, fetched, err := fetchImage(context.Background(), srv.Client(), imageCache{}, src, VariantSmall)
	if err == nil {
		t.Fatal("a 404 returned no error")
	}
	if !fetched {
		t.Error("a failed request reported no network use; the pacer would be skipped")
	}
}

func TestBuildPopulatesCacheAndRehashRunsOffline(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(jpegBytes(t, uint8(100+hits*20)))
	}))
	defer srv.Close()

	ix, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	opts := BuildOptions{CacheDir: t.TempDir(), Variant: VariantSmall}
	srcs := []Source{
		{ScryfallID: "aa11-1", ImageURI: srv.URL + "/small/front/a/a/1.jpg"},
		{ScryfallID: "bb22-2", ImageURI: srv.URL + "/small/front/b/b/2.jpg"},
	}
	if err := ix.Build(context.Background(), srcs, opts, nil); err != nil {
		t.Fatalf("build: %v", err)
	}
	if ix.Count() != 2 {
		t.Fatalf("index holds %d rows, want 2", ix.Count())
	}
	if hits != 2 {
		t.Fatalf("server saw %d requests, want 2", hits)
	}

	// Rehash must not touch the network at all — that is the property that
	// makes iterating on the footprint cheap, and a regression here would
	// show up as a three-hour "rehash".
	before := hits
	skipped, err := ix.Rehash(context.Background(), srcs, opts, nil)
	if err != nil {
		t.Fatalf("rehash: %v", err)
	}
	if skipped != 0 {
		t.Errorf("rehash skipped %d cached sources, want 0", skipped)
	}
	if hits != before {
		t.Errorf("rehash made %d requests, want 0", hits-before)
	}
	if ix.Count() != 2 {
		t.Errorf("index holds %d rows after rehash, want 2", ix.Count())
	}
}

func TestRehashCountsUncachedSourcesInsteadOfFetching(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("rehash reached the network")
		w.Write(jpegBytes(t, 120))
	}))
	defer srv.Close()

	ix, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	opts := BuildOptions{CacheDir: t.TempDir(), Variant: VariantSmall}
	srcs := []Source{{ScryfallID: "aa11-1", ImageURI: srv.URL + "/small/front/a/a/1.jpg"}}
	skipped, err := ix.Rehash(context.Background(), srcs, opts, nil)
	if err != nil {
		t.Fatalf("rehash: %v", err)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
}

func TestRehashWithoutCacheIsAnError(t *testing.T) {
	// Silently downgrading to a network build would take three hours and look
	// like a hang. Refuse instead.
	ix, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if _, err := ix.Rehash(context.Background(), nil, BuildOptions{}, nil); err == nil {
		t.Error("rehash with no cache configured returned no error")
	}
}
