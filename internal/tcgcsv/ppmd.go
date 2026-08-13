package tcgcsv

// The tcgcsv daily archives are PPMd-compressed 7z files. bodgit/sevenzip
// ships a PPMd decoder, but its reader requires exactly five coder
// property bytes (order + memory size) while these archives carry seven —
// the canonical five plus two trailing zeros (observed live) — and it
// rejects them with "ppmd: not enough properties". Only the first five
// bytes mean anything, so this package registers a lenient replacement
// for the PPMd method id that reads those and ignores the tail. Pure Go:
// no system 7z tool involved.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/bodgit/sevenzip"
	"github.com/stangelandcl/ppmd"
)

// ppmdMethodID is the 7z coder id for PPMd, as upstream registers it.
var ppmdMethodID = []byte{0x03, 0x04, 0x01}

func init() {
	sevenzip.RegisterDecompressor(ppmdMethodID, lenientPPMd)
}

// The three figures below come straight from the archive's own headers — an
// attacker-shaped input from a volunteer mirror, decoded by v0 single-
// maintainer dependencies — and the ppmd package allocates whatever they ask
// for. These ceilings are what a genuine archive can plausibly need, so a
// hostile or corrupt header fails as a bad archive instead of as an
// arbitrary allocation (times however many concurrent backfill workers
// pricing.archiveWorkers allows).
const (
	// PPMd var.H model orders run 2..64 by the format's own definition;
	// 7-Zip produces 6 for these archives.
	ppmdMinOrder = 2
	ppmdMaxOrder = 64
	// The model allocation. 7-Zip's UI tops out at 1 GB, but these archives
	// use 16 MB; an eighth of a gigabyte is already indulgent.
	//
	// Halved when pricing.archiveWorkers went from three to six, so the
	// aggregate a hostile header can demand across a concurrent sweep is
	// unchanged. The two numbers are a pair; move one and move the other.
	ppmdMaxMemory = 128 << 20
	// The decoded member size. A whole day's extraction is ~4 MB compressed;
	// a member claiming to inflate past a gigabyte is not price data.
	ppmdMaxOutput = 1 << 30
)

// lenientPPMd mirrors upstream's PPMd reader with the property check
// loosened from "exactly five bytes" to "at least five".
func lenientPPMd(p []byte, uncompressedSize uint64, readers []io.ReadCloser) (io.ReadCloser, error) {
	if len(readers) != 1 {
		return nil, errors.New("ppmd: need exactly one reader")
	}
	if len(p) < 5 {
		return nil, errors.New("ppmd: not enough properties")
	}
	order := p[0]
	memory := binary.LittleEndian.Uint32(p[1:5])
	if order < ppmdMinOrder || order > ppmdMaxOrder {
		return nil, fmt.Errorf("ppmd: implausible model order %d", order)
	}
	if memory > ppmdMaxMemory {
		return nil, fmt.Errorf("ppmd: implausible memory size %d", memory)
	}
	if uncompressedSize > ppmdMaxOutput {
		return nil, fmt.Errorf("ppmd: implausible uncompressed size %d", uncompressedSize)
	}
	pr, err := ppmd.NewH7zReader(readers[0], int(order), int(memory), int(uncompressedSize)) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("ppmd: error creating reader: %w", err)
	}
	return &ppmdReadCloser{c: readers[0], r: &pr}, nil
}

type ppmdReadCloser struct {
	c io.Closer
	r io.Reader
}

func (rc *ppmdReadCloser) Read(p []byte) (int, error) {
	if rc.r == nil {
		return 0, errors.New("ppmd: already closed")
	}
	return rc.r.Read(p)
}

func (rc *ppmdReadCloser) Close() error {
	if rc.c == nil {
		return nil
	}
	err := rc.c.Close()
	rc.c, rc.r = nil, nil
	return err
}
