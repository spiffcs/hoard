package mtgjson

// Stage-by-stage wall-clock for the price archive path, against a real
// cached file. Gated behind an env var so the suite never depends on a
// 142 MB fixture; run it when the backfill feels slow:
//
//	HOARD_PROFILE_ARCHIVE=~/Library/Caches/hoard/mtgjson/<date>-AllPrices.json.gz \
//	  go test ./internal/mtgjson/ -run TestProfileArchiveStages -v
//
// History: the original tokenizer scan measured 30.2s of a 31s backfill;
// the byte scanner (scanKeyedObjects) measures ~3s full-stream, ~2.4s with
// the early exit.

import (
	"bufio"
	"compress/gzip"
	"context"
	"io"
	"os"
	"testing"
	"time"
)

func TestProfileArchiveStages(t *testing.T) {
	path := os.Getenv("HOARD_PROFILE_ARCHIVE")
	if path == "" {
		t.Skip("set HOARD_PROFILE_ARCHIVE to the cached AllPrices.json.gz")
	}

	// Stage 1: raw read of the compressed bytes (disk throughput).
	start := time.Now()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, bufio.NewReaderSize(f, 1<<20))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("read compressed: %8.2fs  (%d MB)", time.Since(start).Seconds(), n>>20)

	// Stage 2: gunzip only (decompression throughput).
	start = time.Now()
	f, _ = os.Open(path)
	zr, err := gzip.NewReader(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	dn, err := io.Copy(io.Discard, zr)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("gunzip:          %8.2fs  (%d MB decoded)", time.Since(start).Seconds(), dn>>20)

	// Stage 3: gunzip + byte scan wanting a key that never matches — the
	// scanner's worst case, with no early exit.
	start = time.Now()
	f, _ = os.Open(path)
	zr, _ = gzip.NewReader(bufio.NewReaderSize(f, 1<<20))
	err = scanKeyedObjects(zr, map[string]bool{"00000000-0000-0000-0000-000000000000": true},
		func(string, []byte) error { return nil })
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("byte scan:       %8.2fs  (full stream, no early exit)", time.Since(start).Seconds())

	// Stage 3b: the large-collection path — one pass with a set lookup at
	// every key boundary, again with no early exit.
	start = time.Now()
	f, _ = os.Open(path)
	zr, _ = gzip.NewReader(bufio.NewReaderSize(f, 1<<20))
	err = scanKeyedObjectsSet(zr, map[string]bool{"00000000-0000-0000-0000-000000000000": true}, 36,
		func(string, []byte) error { return nil })
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("set scan:        %8.2fs  (full stream, no early exit)", time.Since(start).Seconds())

	// Stage 4: the real entry point with an empty want set short-circuits,
	// so time it with one impossible UUID to keep the full path honest.
	start = time.Now()
	_, err = PriceHistory(context.Background(),
		Options{CacheDir: dirOf(path)}, map[string]bool{"no-such-uuid": true})
	if err != nil {
		t.Logf("PriceHistory err: %v", err)
	}
	t.Logf("full pipeline:   %8.2fs", time.Since(start).Seconds())
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}
