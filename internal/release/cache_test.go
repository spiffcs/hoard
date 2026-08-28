package release

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var checkedAt = time.Date(2026, 8, 27, 22, 14, 22, 0, time.UTC)

func TestCacheRoundTripsThroughDisk(t *testing.T) {
	dir := t.TempDir()
	want := Cache{LastChecked: checkedAt, LatestSeen: "v0.4.1"}

	if err := SaveCache(dir, want); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	got, err := LoadCache(dir)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if got.LatestSeen != want.LatestSeen {
		t.Errorf("LatestSeen = %q, want %q", got.LatestSeen, want.LatestSeen)
	}
	if !got.LastChecked.Equal(want.LastChecked) {
		t.Errorf("LastChecked = %v, want %v", got.LastChecked, want.LastChecked)
	}
}

func TestAFreshEntryIsGoodForADayAndNoLonger(t *testing.T) {
	c := Cache{LastChecked: checkedAt, LatestSeen: "v0.4.1"}

	cases := []struct {
		when time.Time
		want bool
		why  string
	}{
		{checkedAt, true, "just checked"},
		{checkedAt.Add(23 * time.Hour), true, "within the day"},
		{checkedAt.Add(25 * time.Hour), false, "past the day"},
		{checkedAt.Add(-time.Hour), true, "a clock that went backwards must not cause a storm of checks"},
	}
	for _, c2 := range cases {
		if got := c.Fresh(c2.when); got != c2.want {
			t.Errorf("Fresh(%v) = %v, want %v: %s", c2.when, got, c2.want, c2.why)
		}
	}
}

func TestAnEmptyCacheIsNeverFresh(t *testing.T) {
	if (Cache{}).Fresh(checkedAt) {
		t.Error("a zero cache reported fresh; the first run must check")
	}
}

func TestACorruptCacheIsNotFatal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "update.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, _ := LoadCache(dir)
	if got.Fresh(checkedAt) {
		t.Error("a corrupt cache reported fresh; it must fall back to checking")
	}
}

func TestAMissingCacheIsNotFatal(t *testing.T) {
	got, _ := LoadCache(filepath.Join(t.TempDir(), "no", "such", "dir"))
	if got.Fresh(checkedAt) {
		t.Error("a missing cache reported fresh; the first run must check")
	}
}

func TestSavingToAnUnwritableDirectoryIsReportedNotFatal(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("cannot make a read-only dir here: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if err := SaveCache(dir, Cache{LastChecked: checkedAt, LatestSeen: "v0.4.1"}); err == nil {
		t.Error("SaveCache into a read-only directory returned nil; " +
			"callers need the error so they can ignore it deliberately")
	}
}

func TestDefaultCacheDirSitsBesideTheOtherHoardCaches(t *testing.T) {
	got := DefaultCacheDir()
	if got == "" {
		t.Fatal("DefaultCacheDir is empty")
	}
	if filepath.Base(got) != "hoard" {
		t.Errorf("DefaultCacheDir = %q, want it to end in a hoard directory", got)
	}
}
