package command

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type outputFile struct {
	f    *os.File
	path string
}

func createOutput(path string) (*outputFile, error) {
	if looksLikeSQLite(path) {
		return nil, fmt.Errorf("%s is a SQLite database — refusing to overwrite it with a report", path)
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return nil, err
	}
	return &outputFile{f: f, path: path}, nil
}

func (o *outputFile) Write(p []byte) (int, error) { return o.f.Write(p) }

func (o *outputFile) Commit() error {
	if err := o.f.Close(); err != nil {
		os.Remove(o.f.Name())
		return fmt.Errorf("writing %s: %w", o.path, err)
	}
	if err := os.Rename(o.f.Name(), o.path); err != nil {
		os.Remove(o.f.Name())
		return err
	}
	return nil
}

func (o *outputFile) Abort() {
	o.f.Close()
	os.Remove(o.f.Name())
}

func looksLikeSQLite(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [16]byte
	n, _ := io.ReadFull(f, magic[:])
	return n == len(magic) && string(magic[:]) == "SQLite format 3\x00"
}
