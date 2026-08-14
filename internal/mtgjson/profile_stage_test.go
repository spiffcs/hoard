package mtgjson

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
