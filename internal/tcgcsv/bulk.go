package tcgcsv

// Reading many groups' prices at once, by whichever route moves fewer bytes.
//
// tcgcsv publishes the same figures two ways: one small file per group, and one
// daily archive holding every group of every category. Checking a hoard's
// prices against the asks on their own listings wants most of the Magic groups
// it owns, and which route is cheaper depends entirely on how many that is.
// Measured against the real files: a group averages ~82 KB and the daily
// archive is ~3.8 MB, so the archive wins above roughly fifty groups and loses
// badly below it — a small collection asking for eight groups would download
// six times the bytes and throw most of them away.
//
// So the choice is made per call, on what this call would actually fetch. Not
// on the size of the collection: what matters is the groups still missing from
// today's cache, which after the day's first read is usually none.
//
// This is also the politeness question. tcgcsv publishes no terms — its FAQ
// invites scraping and asks for two things, an identifiable User-Agent and a
// quarter-second between requests, both of which this package does. Nothing
// obliges us to move fewer bytes; measuring anyway is the difference between
// following the rules and being a good guest.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spiffcs/hoard/internal/buildinfo"
)

// archiveFloorBytes is a lower bound on a daily archive, used only to skip a
// pointless size check: below it the archive cannot win however small it turns
// out to be, so a small hoard never pays even a HEAD to learn that. The real
// comparison is measured, not assumed.
const archiveFloorBytes = 3 << 20

// typicalGroupBytes stands in for the per-group size until there is a cache to
// measure one from — the mean over 87 real group files. It only has to be close
// enough to keep a first run on the right side of a threefold difference.
const typicalGroupBytes = 82 << 10

// GroupQuotesBulk returns today's quotes for many groups, keyed group → product
// → subtype, choosing the cheaper of one archive download and one request per
// group.
//
// Groups it could not read are absent rather than an error, exactly as reading
// them one at a time leaves them absent. That is what lets the caller treat a
// missing group as "not checked" instead of "checked and clean", which is the
// difference between leaving a correction standing and retiring it on no
// evidence.
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

	// The archive fills the day cache the per-group route reads, so a run that
	// takes it leaves the same files behind and everything downstream — the
	// treated-foil overlay included — is served from disk either way.
	if archiveIsCheaper(ctx, o, len(missing)) {
		if err := fillDayCacheFromArchive(ctx, o, missing); err != nil {
			// A missing or unreadable archive is not a failure, it is a route
			// that did not work out; the per-group route below still answers.
			o.say("today's price archive was unusable (%v); reading %d groups one at a time",
				err, len(missing))
		}
	}
	for _, gid := range missing {
		q, err := GroupQuotes(ctx, o, gid)
		if err != nil {
			continue // absent, not an error — see the doc comment
		}
		out[gid] = q
	}
	return out, nil
}

// archiveIsCheaper compares what the two routes would move, measuring both
// sides where it can: the per-group size from the files already on disk, and
// the archive's from the server.
func archiveIsCheaper(ctx context.Context, o Options, missing int) bool {
	if o.CacheDir == "" {
		return false // nowhere to put the extraction; the archive would be wasted
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

// meanDayCacheBytes is the average size of today's cached group files, or zero
// when there are none to average.
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

// fillDayCacheFromArchive extracts the wanted groups out of today's archive and
// writes them where the per-group route caches its downloads.
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
			continue // this day's archive has no such group; the per-group route tries
		}
		// Parsed before it is cached: a member that does not decode must not
		// become today's answer for that group, and the per-group route can
		// still fetch a good one.
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

// contentLength asks how big a file is without downloading it. It goes through
// the same pacer as every other request; a HEAD is still a request.
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
