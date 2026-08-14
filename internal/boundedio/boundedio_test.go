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

	got, err := io.ReadAll(Limit(strings.NewReader("hello"), 5, "test"))
	if err != nil {
		t.Fatalf("a stream of exactly N bytes must be allowed: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestReaderFailsRatherThanTruncating(t *testing.T) {
	got, err := io.ReadAll(Limit(strings.NewReader("hello world"), 5, "the card bundle"))
	if err == nil {
		t.Fatalf("over-limit stream returned no error; got %q", got)
	}
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("error does not wrap ErrTooLarge: %v", err)
	}

	if !strings.Contains(err.Error(), "the card bundle") {
		t.Errorf("error does not name the stream: %v", err)
	}
}

func TestIoLimitReaderWouldHaveTruncatedSilently(t *testing.T) {
	got, err := io.ReadAll(io.LimitReader(strings.NewReader("hello world"), 5))
	if err != nil {
		t.Fatalf("unexpected error from io.LimitReader: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("io.LimitReader behaviour changed: got %q", got)
	}

}

func TestLimitExpansionUsesTheDeclaredSize(t *testing.T) {
	b := LimitExpansion(strings.NewReader(""), 100, 999, "test")
	if b.N != 100*MaxExpansion {
		t.Errorf("N = %d, want %d", b.N, 100*MaxExpansion)
	}
}

func TestLimitExpansionFallsBackWhenSizeIsUnknown(t *testing.T) {

	for _, size := range []int64{0, -1} {
		b := LimitExpansion(strings.NewReader(""), size, 999, "test")
		if b.N != 999 {
			t.Errorf("compressed=%d: N = %d, want the 999 fallback", size, b.N)
		}
	}
}

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

	_, err = io.ReadAll(LimitExpansion(zr, compressed, 1<<20, "the bomb"))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a %.0f:1 stream was not stopped: %v",
			float64(8<<20)/float64(compressed), err)
	}
}

func TestAllowsTheRealWorldRatio(t *testing.T) {

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
