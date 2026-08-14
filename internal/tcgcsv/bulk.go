package tcgcsv

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spiffcs/hoard/internal/buildinfo"
)

const archiveFloorBytes = 3 << 20

const typicalGroupBytes = 82 << 10

func GroupQuotesBulk(ctx context.Context, o Options, groupIDs []int) (map[int]map[string]map[string]Quote, error) {
	out := make(map[int]map[string]map[string]Quote, len(groupIDs))
	var missing []int
	for _, gid := range groupIDs {
		b, err := readDayCache(o, gid)
		if err != nil {
			missing = append(missing, gid)
			continue
		}
		q, err := allQuotes(b)
		if err != nil {
			missing = append(missing, gid)
			continue
		}
		out[gid] = q
	}
	if len(missing) == 0 {
		return out, nil
	}

	if archiveIsCheaper(ctx, o, len(missing)) {
		if err := fillDayCacheFromArchive(ctx, o, missing); err != nil {

			o.say("today's price archive was unusable (%v); reading %d groups one at a time",
				err, len(missing))
		}
	}
	for _, gid := range missing {
		q, err := GroupQuotes(ctx, o, gid)
		if err != nil {
			continue
		}
		out[gid] = q
	}
	return out, nil
}

func archiveIsCheaper(ctx context.Context, o Options, missing int) bool {
	if o.CacheDir == "" {
		return false
	}
	perGroup := meanDayCacheBytes(o)
	if perGroup <= 0 {
		perGroup = typicalGroupBytes
	}
	want := int64(missing) * perGroup
	if want < archiveFloorBytes {
		return false
	}
	size, err := contentLength(ctx, o, archiveURL(o, today()))
	if err != nil || size <= 0 {
		return false
	}
	return size < want
}

func meanDayCacheBytes(o Options) int64 {
	entries, err := os.ReadDir(o.CacheDir)
	if err != nil {
		return 0
	}
	prefix := today() + "-prices-"
	var total, n int64
	for _, e := range entries {
		if e.IsDir() || len(e.Name()) <= len(prefix) || e.Name()[:len(prefix)] != prefix {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		total += info.Size()
		n++
	}
	if n == 0 {
		return 0
	}
	return total / n
}

func fillDayCacheFromArchive(ctx context.Context, o Options, groupIDs []int) error {
	date := today()
	dir := filepath.Join(o.CacheDir, "archive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := get(ctx, o, archiveURL(o, date), "")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, date+".todays.7z")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return err
	}
	defer os.Remove(path)

	want := map[string]bool{}
	byMember := map[string]int{}
	for _, gid := range groupIDs {
		member := fmt.Sprintf("%s/%d/%d/prices", date, magicCategory, gid)
		want[member], byMember[member] = true, gid
	}
	members, err := readArchiveMembers(path, want)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.CacheDir, 0o755); err != nil {
		return err
	}
	for member, gid := range byMember {
		body := members[member]
		if len(body) == 0 {
			continue
		}

		if _, err := allQuotes(body); err != nil {
			continue
		}
		writeFileAtomic(dayCachePath(o, gid), body)
	}
	return nil
}

func archiveURL(o Options, date string) string {
	return fmt.Sprintf("%s/archive/tcgplayer/prices-%s.ppmd.7z", o.base(), date)
}

func dayCachePath(o Options, groupID int) string {
	return filepath.Join(o.CacheDir, fmt.Sprintf("%s-prices-%d.json", today(), groupID))
}

func readDayCache(o Options, groupID int) ([]byte, error) {
	if o.CacheDir == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(dayCachePath(o, groupID))
}

func contentLength(ctx context.Context, o Options, url string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent)
	pace()
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HEAD %s: %s", url, resp.Status)
	}
	return resp.ContentLength, nil
}
