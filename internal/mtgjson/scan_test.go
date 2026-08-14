package mtgjson

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

type oneByteReader struct{ r io.Reader }

func (o oneByteReader) Read(p []byte) (int, error) { return o.r.Read(p[:1]) }

func scanDoc() string {
	big := strings.Repeat(`"x":1,`, 5000)
	return `{"meta":{"date":"2026-07-31"},"data":{` +
		`"aaa-1":{"paper":{"tcgplayer":{"retail":{"normal":{"2026-07-01":1.5}}}}},` +
		`"bbb-2":{` + big[:len(big)-1] + `},` +
		`"ccc-3":{"nested":{"deep":{"s":"a \"quoted\" bra}ce"}}},` +
		`"ddd-4":{"last":true}}}`
}

func TestScanKeyedObjectsFindsAcrossChunkBoundaries(t *testing.T) {
	for _, r := range []io.Reader{
		strings.NewReader(scanDoc()),
		oneByteReader{strings.NewReader(scanDoc())},
	} {
		got := map[string]string{}
		err := scanKeyedObjects(r, map[string]bool{
			"aaa-1": true, "ccc-3": true, "ddd-4": true,
		}, func(k string, raw []byte) error {
			got[k] = string(raw)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Fatalf("found %d records, want 3: %v", len(got), got)
		}
		if !strings.Contains(got["aaa-1"], "tcgplayer") {
			t.Errorf("aaa-1 = %q", got["aaa-1"])
		}

		if !strings.HasSuffix(got["ccc-3"], `}}}`) {
			t.Errorf("ccc-3 cut short: %q", got["ccc-3"])
		}
		if got["ddd-4"] != `{"last":true}` {
			t.Errorf("ddd-4 = %q", got["ddd-4"])
		}
	}
}

func TestScanKeyedObjectsLargeRecordSpansReads(t *testing.T) {
	var b bytes.Buffer
	b.WriteString(`{"data":{`)
	b.WriteString(`"big":{`)
	for i := range 100000 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"k%d":%d`, i, i)
	}
	b.WriteString(`},"tail":{"t":1}}}`)

	found := 0
	err := scanKeyedObjects(bytes.NewReader(b.Bytes()),
		map[string]bool{"big": true, "tail": true},
		func(k string, raw []byte) error {
			found++
			if k == "big" && len(raw) < 100000 {
				t.Errorf("big record truncated: %d bytes", len(raw))
			}
			return nil
		})
	if err != nil || found != 2 {
		t.Fatalf("err=%v found=%d", err, found)
	}
}

func TestScanKeyedObjectsStopsEarly(t *testing.T) {

	payload := `{"data":{"first":{"a":1},` + strings.Repeat(`"pad":{"p":0},`, 10) + `"end":{"z":9}}}`
	total := payload + strings.Repeat(" ", 16<<20)
	r := &countingReader{r: strings.NewReader(total)}
	err := scanKeyedObjects(r, map[string]bool{"first": true},
		func(string, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if r.n > (5 << 20) {
		t.Errorf("no early exit: read %d of %d bytes", r.n, len(total))
	}
}

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestScanKeyedObjectsAbsentKey(t *testing.T) {
	calls := 0
	err := scanKeyedObjects(strings.NewReader(scanDoc()),
		map[string]bool{"nope": true}, func(string, []byte) error { calls++; return nil })
	if err != nil || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestScanKeyedObjectsSetPath(t *testing.T) {
	defer func(v int) { setScanMin = v }(setScanMin)
	setScanMin = 0

	for _, r := range []io.Reader{
		strings.NewReader(scanDoc()),
		oneByteReader{strings.NewReader(scanDoc())},
	} {
		got := map[string]string{}
		err := scanKeyedObjects(r, map[string]bool{
			"aaa-1": true, "ccc-3": true, "ddd-4": true,
		}, func(k string, raw []byte) error {
			got[k] = string(raw)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 {
			t.Fatalf("set path found %d records, want 3: %v", len(got), got)
		}
		if !strings.HasSuffix(got["ccc-3"], `}}}`) {
			t.Errorf("ccc-3 cut short: %q", got["ccc-3"])
		}
		if got["ddd-4"] != `{"last":true}` {
			t.Errorf("ddd-4 = %q", got["ddd-4"])
		}
	}
}

func TestScanKeyedObjectsMixedLengthsFallBack(t *testing.T) {
	defer func(v int) { setScanMin = v }(setScanMin)
	setScanMin = 0

	got := 0
	err := scanKeyedObjects(strings.NewReader(scanDoc()),
		map[string]bool{"aaa-1": true, "big-key-longer": true},
		func(string, []byte) error { got++; return nil })
	if err != nil || got != 1 {
		t.Fatalf("err=%v got=%d, want the uniform fallback to still find aaa-1", err, got)
	}
}

func TestScanKeyedObjectsSetPathStopsEarly(t *testing.T) {
	defer func(v int) { setScanMin = v }(setScanMin)
	setScanMin = 0

	payload := `{"data":{"first":{"a":1}}}`
	total := payload + strings.Repeat(" ", 16<<20)
	r := &countingReader{r: strings.NewReader(total)}
	err := scanKeyedObjects(r, map[string]bool{"first": true},
		func(string, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if r.n > (5 << 20) {
		t.Errorf("no early exit: read %d of %d bytes", r.n, len(total))
	}
}
