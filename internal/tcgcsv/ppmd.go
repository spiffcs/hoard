package tcgcsv

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/bodgit/sevenzip"
	"github.com/stangelandcl/ppmd"
)

var ppmdMethodID = []byte{0x03, 0x04, 0x01}

func init() {
	sevenzip.RegisterDecompressor(ppmdMethodID, lenientPPMd)
}

const (
	ppmdMinOrder = 2
	ppmdMaxOrder = 64

	ppmdMaxMemory = 128 << 20

	ppmdMaxOutput = 1 << 30
)

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
