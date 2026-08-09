package artindex

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestHashEncodingRoundTrips(t *testing.T) {
	h := FromImage(synthImage(3, 146, 204))
	b := encodeHash(h)
	if len(b) != 32 {
		t.Fatalf("encoded to %d bytes, want 32", len(b))
	}
	got, ok := decodeHash(b)
	if !ok {
		t.Fatal("decode rejected its own encoding")
	}
	if got != h {
		t.Errorf("round trip changed the hash: %v -> %v", h, got)
	}
}

func TestDecodeHashRejectsWrongWidths(t *testing.T) {
	// The width check is what stands between a foreign row and a confident
	// wrong answer: a short blob zero-pads into a plausible distance, and a
	// plausible distance is exactly what this channel must never invent. The
	// 8-byte case is the old 64-bit index specifically.
	for _, n := range []int{0, 8, 16, 31, 33, 64} {
		if _, ok := decodeHash(make([]byte, n)); ok {
			t.Errorf("decodeHash accepted %d bytes", n)
		}
	}
}

func TestOpenRecordsTheAlgorithm(t *testing.T) {
	dir := t.TempDir()
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	got, err := ix.meta("algorithm")
	if err != nil {
		t.Fatal(err)
	}
	if got != algorithm {
		t.Errorf("stored algorithm = %q, want %q", got, algorithm)
	}
}

func TestOpenDropsRowsFromAnotherFootprint(t *testing.T) {
	dir := t.TempDir()
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ix.db.Exec(`INSERT INTO hashes VALUES ('a', ?, '')`,
		encodeHash(FromImage(synthImage(1, 146, 204)))); err != nil {
		t.Fatal(err)
	}
	// Pretend a future build changed the keep block.
	if err := ix.setMeta("algorithm", "dct32-keep24-nodc-v3"); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	// Reopening under the real algorithm must discard the foreign rows rather
	// than compare against them. Hashes of two footprints are both 256
	// comparable bits, so nothing here errors on its own — the index would
	// simply return confident nonsense.
	ix2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix2.Close()
	if ix2.Count() != 0 {
		t.Errorf("index kept %d rows from another footprint, want 0", ix2.Count())
	}
	got, _ := ix2.meta("algorithm")
	if got != algorithm {
		t.Errorf("algorithm not re-stamped: %q", got)
	}
}

func TestOpenMigratesTheSixtyFourBitIndex(t *testing.T) {
	// The real upgrade path: a v1 index has an INTEGER hash column and no meta
	// table at all. It must come back empty and rebuildable, not half-read.
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "artindex.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE hashes (
		scryfall_id TEXT PRIMARY KEY,
		hash        INTEGER NOT NULL,
		sole_finish TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO hashes VALUES ('a', 123456789, 'foil')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	ix, err := Open(dir)
	if err != nil {
		t.Fatalf("opening a 64-bit index: %v", err)
	}
	defer ix.Close()
	if ix.Count() != 0 {
		t.Errorf("kept %d rows from the 64-bit index, want 0", ix.Count())
	}
	// And it must be usable afterwards, not just empty.
	if _, err := ix.db.Exec(`INSERT INTO hashes VALUES ('b', ?, '')`,
		encodeHash(FromImage(synthImage(2, 146, 204)))); err != nil {
		t.Fatalf("the rebuilt table does not accept a 256-bit hash: %v", err)
	}
}

func TestBuildRecordsVariantAndAVariantSwitchClearsTheIndex(t *testing.T) {
	served := jpegBytes(t, 180)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(served)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cacheDir := t.TempDir()
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	srcs := []Source{{ScryfallID: "aa11-1", ImageURI: srv.URL + "/small/front/a/a/1.jpg"}}
	if err := ix.Build(context.Background(), srcs,
		BuildOptions{CacheDir: cacheDir, Variant: VariantSmall}, nil); err != nil {
		t.Fatal(err)
	}
	if ix.Count() != 1 {
		t.Fatalf("count = %d, want 1", ix.Count())
	}
	if ix.Variant() != VariantSmall {
		t.Errorf("variant = %q, want %q", ix.Variant(), VariantSmall)
	}

	// Switching the source image invalidates every stored row. Build skips
	// ids it already holds, so without the wipe this index would report one
	// entry and never re-fetch it — half small, half normal, and no way to
	// tell from the outside.
	if err := ix.Build(context.Background(), srcs,
		BuildOptions{CacheDir: cacheDir, Variant: VariantNormal}, nil); err != nil {
		t.Fatal(err)
	}
	if ix.Variant() != VariantNormal {
		t.Errorf("variant = %q, want %q", ix.Variant(), VariantNormal)
	}
	// The row was cleared and then rebuilt from the new variant, so the count
	// is back to one — but it was genuinely re-fetched rather than kept.
	if ix.Count() != 1 {
		t.Errorf("count = %d after the variant switch, want 1", ix.Count())
	}
}

func TestVariantSwitchSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.adoptVariant(VariantNormal); err != nil {
		t.Fatal(err)
	}
	ix.Close()

	ix2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix2.Close()
	if ix2.Variant() != VariantNormal {
		t.Errorf("variant after reopen = %q, want %q", ix2.Variant(), VariantNormal)
	}
}

func TestReloadSkipsRowsOfTheWrongWidth(t *testing.T) {
	dir := t.TempDir()
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	good := encodeHash(FromImage(synthImage(1, 146, 204)))
	if _, err := ix.db.Exec(
		`INSERT INTO hashes VALUES ('good', ?, ''), ('short', ?, '')`,
		good, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := ix.reload(); err != nil {
		t.Fatal(err)
	}
	if ix.Count() != 1 {
		t.Fatalf("loaded %d rows, want 1 (the short one must be skipped)", ix.Count())
	}
	if ix.ids[0] != "good" {
		t.Errorf("loaded %q, want the well-formed row", ix.ids[0])
	}
	if !bytes.Equal(encodeHash(ix.hashes[0]), good) {
		t.Error("the surviving row's hash did not round-trip")
	}
}

func TestBestSentinelIsAboveEveryReachableDistance(t *testing.T) {
	// An empty index must not hand back a match that any margin test can
	// clear. maxDistance is every bit differing, so the seed sits one above.
	dir := t.TempDir()
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	best, second := ix.Best(FromImage(synthImage(1, 146, 204)))
	if best.Distance <= maxDistance || second.Distance <= maxDistance {
		t.Errorf("empty index returned best=%d second=%d, want both > %d",
			best.Distance, second.Distance, maxDistance)
	}
	if best.ScryfallID != "" {
		t.Errorf("empty index named a printing: %q", best.ScryfallID)
	}
}
