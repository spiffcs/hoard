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
