package command

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// outputFile is where `-o FILE` writes land. Two protections over a bare
// os.Create, both earned the hard way in review:
//
//   - It refuses to overwrite a SQLite database. `hoard report -o hoard.db`,
//     typed in a hurry, used to truncate the collection itself before a
//     single byte of report existed.
//   - It writes through a temp file in the target's directory and renames
//     into place on Commit, so a failed write (disk full, quota) leaves any
//     previous file intact instead of truncated — and the failure is
//     reported rather than swallowed by a deferred Close.
//
// Deliberate overwrites of previous exports keep working: re-running a
// report onto the same path is the normal workflow, not an accident.
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

// Commit closes the temp file and renames it over the target, reporting any
// failure — a truncated file with exit code 0 is the one outcome this type
// exists to prevent.
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

// Abort discards the temp file, leaving whatever was at the target untouched.
func (o *outputFile) Abort() {
	o.f.Close()
	os.Remove(o.f.Name())
}

// looksLikeSQLite sniffs the file's magic rather than trusting its name:
// the database a user points at by accident is rarely called anything.db.
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
