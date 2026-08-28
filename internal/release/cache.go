package release

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const (
	cacheFile  = "update.json"
	checkEvery = 24 * time.Hour
)

// Cache records the last answer so hoard asks GitHub at most once a day. It is
// a cache in the strict sense: losing it costs one extra request, so every
// error here is safe for a caller to ignore.
type Cache struct {
	LastChecked time.Time `json:"lastChecked"`
	LatestSeen  string    `json:"latestSeen"`
}

func DefaultCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "hoard")
}

func LoadCache(dir string) (Cache, error) {
	if dir == "" {
		return Cache{}, errors.New("no cache directory")
	}
	raw, err := os.ReadFile(filepath.Join(dir, cacheFile))
	if err != nil {
		return Cache{}, err
	}
	var c Cache
	if err := json.Unmarshal(raw, &c); err != nil {
		return Cache{}, err
	}
	return c, nil
}

func SaveCache(dir string, c Cache) error {
	if dir == "" {
		return errors.New("no cache directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, cacheFile), raw, 0o600)
}

// Fresh treats a clock that has gone backwards as fresh rather than stale, so
// a time adjustment cannot turn every launch into a request.
func (c Cache) Fresh(now time.Time) bool {
	if c.LastChecked.IsZero() || c.LatestSeen == "" {
		return false
	}
	return now.Sub(c.LastChecked) < checkEvery
}
