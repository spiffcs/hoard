package boundedio

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestReaderPassesThroughUnderTheLimit(t *testing.T) {
	got, err := io.ReadAll(Limit(strings.NewReader("hello"), 10, "test"))
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestReaderAllowsExactlyTheLimit(t *testing.T) {
	// An off-by-one here would reject a download that is precisely its
	// declared size, which is the normal case and not an attack.
	got, err := io.ReadAll(Limit(strings.NewReader("hello"), 5, "test"))
	if err != nil {
		t.Fatalf("a stream of exactly N bytes must be allowed: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

// The property the package exists for: over the limit is an ERROR, never a
// short read that reads as success.
func TestReaderFailsRatherThanTruncating(t *testing.T) {
	got, err := io.ReadAll(Limit(strings.NewReader("hello world"), 5, "the card bundle"))
	if err == nil {
		t.Fatalf("over-limit stream returned no error; got %q", got)
	}
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("error does not wrap ErrTooLarge: %v", err)
	}
	// The message has to name the download, or a user seeing it in the wild
	// cannot tell which of four fetches failed.
	if !strings.Contains(err.Error(), "the card bundle") {
		t.Errorf("error does not name the stream: %v", err)
	}
}

// The contrast that motivates the package. io.LimitReader is a correctness
// hazard here, and this test is the evidence rather than the claim.
func TestIoLimitReaderWouldHaveTruncatedSilently(t *testing.T) {
	got, err := io.ReadAll(io.LimitReader(strings.NewReader("hello world"), 5))
	if err != nil {
		t.Fatalf("unexpected error from io.LimitReader: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("io.LimitReader behaviour changed: got %q", got)
	}
	// Reaching here means io.LimitReader reported success on a truncated
	// stream. That is exactly the outcome Reader exists to avoid.
}

func TestLimitExpansionUsesTheDeclaredSize(t *testing.T) {
	b := LimitExpansion(strings.NewReader(""), 100, 999, "test")
	if b.N != 100*MaxExpansion {
		t.Errorf("N = %d, want %d", b.N, 100*MaxExpansion)
	}
}

func TestLimitExpansionFallsBackWhenSizeIsUnknown(t *testing.T) {
	// A missing Content-Length must not mean "unbounded": omitting a header is
	// free for whoever is serving the response.
	for _, size := range []int64{0, -1} {
		b := LimitExpansion(strings.NewReader(""), size, 999, "test")
		if b.N != 999 {
			t.Errorf("compressed=%d: N = %d, want the 999 fallback", size, b.N)
		}
	}
}

// End to end against a real gzip bomb, because the unit tests above only prove
// the arithmetic — this proves the thing actually stops a compressed stream
// from expanding without bound.
func TestStopsAGzipBomb(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(bytes.Repeat([]byte{0}, 8<<20)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	compressed := int64(buf.Len())
	t.Logf("8 MiB of zeroes compressed to %d bytes (%.0f:1)",
		compressed, float64(8<<20)/float64(compressed))

	zr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	// The bound the production call sites use: MaxExpansion × declared size.
	_, err = io.ReadAll(LimitExpansion(zr, compressed, 1<<20, "the bomb"))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a %.0f:1 stream was not stopped: %v",
			float64(8<<20)/float64(compressed), err)
	}
}

// A stream at the real measured ratio must pass, or the limit breaks the
// application it is meant to protect.
func TestAllowsTheRealWorldRatio(t *testing.T) {
	// 8.00x is the ratio measured against Scryfall's default_cards bundle on
	// 2026-08-13. The fixture has to VARY: the first version of this test
	// repeated one identical line and compressed at 402x, which would have
	// "passed" while proving nothing about real card data. Varying the fields
	// puts it in the same order of magnitude as the real bundle.
	var payload []byte
	for i := range 40000 {
		payload = append(payload, fmt.Sprintf(
			`{"id":"%08x-%04x","name":"Card %d","set":"s%03d","cn":"%d","usd":"%d.%02d"}`+"\n",
			i*2654435761, i%65536, i, i%400, i%512, i%40, i%100)...)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	ratio := float64(len(payload)) / float64(buf.Len())
	if ratio > MaxExpansion {
		t.Skipf("fixture compresses at %.1fx, above the %dx bound; "+
			"it is not representative of card JSON", ratio, MaxExpansion)
	}
	compressed := int64(buf.Len())
	zr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(LimitExpansion(zr, compressed, 1<<20, "cards"))
	if err != nil {
		t.Fatalf("card-shaped data at %.1fx was rejected: %v", ratio, err)
	}
	if len(got) != len(payload) {
		t.Errorf("got %d bytes, want %d", len(got), len(payload))
	}
}

// LimitRatio is the bound the call sites use, so it gets the same two tests
// the fixed limit does: it must stop a bomb and must not stop real data.
func TestLimitRatioStopsABomb(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(bytes.Repeat([]byte{0}, 64<<20)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("64 MiB of zeroes compressed to %d bytes", buf.Len())

	src := &Counter{R: &buf}
	zr, err := gzip.NewReader(src)
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, LimitRatio(zr, src, "the bomb"))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("bomb not stopped: read %d bytes, err %v", n, err)
	}
	// The point of the ratio bound: it stops EARLY, not after the full expansion.
	if n > 8<<20 {
		t.Errorf("stopped after %d bytes; expected to stop near the floor", n)
	}
}

func TestLimitRatioAllowsRealData(t *testing.T) {
	var payload []byte
	for i := range 200000 {
		payload = append(payload, fmt.Sprintf(
			`{"id":"%08x-%04x","name":"Card %d","set":"s%03d","cn":"%d"}`+"\n",
			i*2654435761, i%65536, i, i%400, i%512)...)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("card-shaped fixture: %d -> %d (%.1fx)",
		buf.Len(), len(payload), float64(len(payload))/float64(buf.Len()))

	src := &Counter{R: &buf}
	zr, err := gzip.NewReader(src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(LimitRatio(zr, src, "cards"))
	if err != nil {
		t.Fatalf("real-shaped data rejected: %v", err)
	}
	if len(got) != len(payload) {
		t.Errorf("got %d bytes, want %d", len(got), len(payload))
	}
}
