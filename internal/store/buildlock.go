package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var ErrDatabaseReplaced = errors.New("the database file this hoard opened is no longer there")

type buildLock struct{ f *os.File }

func lockFilePath(dbPath string) string {
	dir, base := filepath.Split(dbPath)
	return filepath.Join(dir, "."+base+".hoard-lock")
}

func lockable(path string) bool { return path != "" && path != ":memory:" }

func lockDatabase(path string, exclusive bool) (*buildLock, error) {
	if !lockable(path) {
		return nil, nil
	}
	f, err := os.OpenFile(lockFilePath(path), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening the lock for %q: %w", path, err)
	}
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf(
				"another hoard already has %s open; close it and run this again", path)
		}
		return nil, fmt.Errorf("locking %q: %w", path, err)
	}
	return &buildLock{f: f}, nil
}

func (l *buildLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); err != nil {
		l.f.Close()
		return err
	}
	return l.f.Close()
}

func (s *Store) checkFile() error {
	if !lockable(s.path) || s.file == nil {
		return nil
	}
	now, err := os.Stat(s.path)
	if err != nil || !os.SameFile(s.file, now) {
		return fmt.Errorf("%s: %w", s.path, ErrDatabaseReplaced)
	}
	return nil
}

func (s *Store) name() string {
	if s.path == "" {
		return "the database"
	}
	return s.path
}
