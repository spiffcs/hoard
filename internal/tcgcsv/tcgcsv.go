package tcgcsv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bodgit/sevenzip"

	"github.com/spiffcs/hoard/internal/boundedio"
	"github.com/spiffcs/hoard/internal/buildinfo"
)

const magicCategory = 1

var apiBase = "https://tcgcsv.com"

var today = func() string { return time.Now().Format("2006-01-02") }

type Options struct {
	BaseURL string

	CacheDir string

	Note func(string)
}

func (o Options) say(format string, args ...any) {
	if o.Note != nil {
		o.Note(fmt.Sprintf(format, args...))
	}
}

func (o Options) base() string {
	if o.BaseURL != "" {
		return o.BaseURL
	}
	return apiBase
}

var httpClient = &http.Client{Timeout: 2 * time.Minute}

var (
	requestGap = 250 * time.Millisecond
	paceMu     sync.Mutex
	lastStart  time.Time
	paceSleep  = time.Sleep
)

func pace() {
	paceMu.Lock()
	wait := requestGap - time.Since(lastStart)
	if wait > 0 {
		paceSleep(wait)
	}
	lastStart = time.Now()
	paceMu.Unlock()
}

func get(ctx context.Context, o Options, url, cacheName string) ([]byte, error) {
	var cachePath string
	if o.CacheDir != "" && cacheName != "" {
		cachePath = filepath.Join(o.CacheDir, today()+"-"+cacheName)
		if b, err := os.ReadFile(cachePath); err == nil {
			return b, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent)
	pace()
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if cachePath != "" {
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
			os.WriteFile(cachePath, b, 0o644)
			cleanDayCache(o.CacheDir)
		}
	}
	return b, nil
}

func cleanDayCache(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := today() + "-"
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		os.Remove(filepath.Join(dir, e.Name()))
	}
}

func Groups(ctx context.Context, o Options) (map[string]int, error) {
	b, err := get(ctx, o, fmt.Sprintf("%s/tcgplayer/%d/groups", o.base(), magicCategory), "groups.json")
	if err != nil {
		return nil, err
	}
	var d struct {
		Results []struct {
			GroupID      int    `json:"groupId"`
			Abbreviation string `json:"abbreviation"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("decoding tcgcsv groups: %w", err)
	}
	out := make(map[string]int, len(d.Results))
	for _, g := range d.Results {
		if g.Abbreviation != "" {
			out[strings.ToUpper(g.Abbreviation)] = g.GroupID
		}
	}
	return out, nil
}

type priceRows struct {
	Results []struct {
		ProductID   int     `json:"productId"`
		MarketPrice float64 `json:"marketPrice"`
		LowPrice    float64 `json:"lowPrice"`
		MidPrice    float64 `json:"midPrice"`
		HighPrice   float64 `json:"highPrice"`
		DirectLow   float64 `json:"directLowPrice"`
		SubTypeName string  `json:"subTypeName"`
	} `json:"results"`
}

type Quote struct {
	Market float64

	Low, Mid, High float64

	Direct float64
}

const (
	Normal = "Normal"
	Foil   = "Foil"
)

func allQuotes(b []byte) (map[string]map[string]Quote, error) {
	var d priceRows
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("decoding tcgcsv prices: %w", err)
	}
	out := map[string]map[string]Quote{}
	for _, r := range d.Results {
		if r.MarketPrice <= 0 {
			continue
		}
		id := strconv.Itoa(r.ProductID)
		if out[id] == nil {
			out[id] = map[string]Quote{}
		}
		out[id][r.SubTypeName] = Quote{
			Market: r.MarketPrice,
			Low:    r.LowPrice,
			Mid:    r.MidPrice,
			High:   r.HighPrice,
			Direct: r.DirectLow,
		}
	}
	return out, nil
}

func foilQuotes(b []byte) (map[string]Quote, error) {
	all, err := allQuotes(b)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Quote, len(all))
	for id, bySub := range all {
		if q, ok := bySub[Foil]; ok {
			out[id] = q
			continue
		}
		for _, q := range bySub {
			out[id] = q
		}
	}
	return out, nil
}

func GroupPrices(ctx context.Context, o Options, groupID int) (map[string]Quote, error) {
	b, err := groupBody(ctx, o, groupID)
	if err != nil {
		return nil, err
	}
	return foilQuotes(b)
}

func GroupQuotes(ctx context.Context, o Options, groupID int) (map[string]map[string]Quote, error) {
	b, err := groupBody(ctx, o, groupID)
	if err != nil {
		return nil, err
	}
	return allQuotes(b)
}

func groupBody(ctx context.Context, o Options, groupID int) ([]byte, error) {
	return get(ctx, o,
		fmt.Sprintf("%s/tcgplayer/%d/%d/prices", o.base(), magicCategory, groupID),
		fmt.Sprintf("prices-%d.json", groupID))
}

const archiveKeepDays = 100

var readArchiveMembers = func(path string, want map[string]bool) (map[string][]byte, error) {
	r, err := sevenzip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", filepath.Base(path), err)
	}
	defer r.Close()

	var archiveBytes int64 = 16 << 20
	if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
		archiveBytes = fi.Size()
	}
	out := map[string][]byte{}
	for _, f := range r.File {
		if !want[f.Name] {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("extracting %s: %w", f.Name, err)
		}

		b, err := io.ReadAll(boundedio.Limit(rc, archiveBytes*boundedio.MaxExpansion,
			"the price archive member "+f.Name))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("extracting %s: %w", f.Name, err)
		}
		out[f.Name] = b
	}
	return out, nil
}

func ArchivePrices(ctx context.Context, o Options, date string, groupIDs []int) (map[int]map[string]Quote, error) {
	if o.CacheDir == "" {
		return nil, fmt.Errorf("archive reads need a cache directory")
	}
	dir := filepath.Join(o.CacheDir, "archive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	out := map[int]map[string]Quote{}
	var missing []int
	for _, gid := range groupIDs {
		path := filepath.Join(dir, fmt.Sprintf("%s-%d.json", date, gid))
		b, err := os.ReadFile(path)
		if err != nil {
			missing = append(missing, gid)
			continue
		}
		prices, err := foilQuotes(b)
		if err != nil {

			os.Remove(path)
			missing = append(missing, gid)
			continue
		}
		out[gid] = prices
	}
	if len(missing) == 0 {
		return out, nil
	}

	archive := filepath.Join(dir, date+".7z")
	if _, err := os.Stat(archive); err != nil {
		b, err := get(ctx, o, fmt.Sprintf("%s/archive/tcgplayer/prices-%s.ppmd.7z", o.base(), date), "")
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(archive, b, 0o644); err != nil {
			return nil, err
		}
	}
	defer os.Remove(archive)
	want := map[string]bool{}
	byMember := map[string]int{}
	for _, gid := range missing {
		member := fmt.Sprintf("%s/%d/%d/prices", date, magicCategory, gid)
		want[member], byMember[member] = true, gid
	}
	members, err := readArchiveMembers(archive, want)
	if err != nil {
		return nil, err
	}
	for member, gid := range byMember {
		b := members[member]
		if len(b) == 0 {

			writeFileAtomic(filepath.Join(dir, fmt.Sprintf("%s-%d.json", date, gid)), []byte("{}"))
			continue
		}
		prices, err := foilQuotes(b)
		if err != nil {

			continue
		}
		writeFileAtomic(filepath.Join(dir, fmt.Sprintf("%s-%d.json", date, gid)), b)
		out[gid] = prices
	}
	pruneArchive(dir)
	return out, nil
}

func writeFileAtomic(path string, b []byte) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
	}
}

func pruneArchive(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -archiveKeepDays).Format("2006-01-02")
	for _, e := range entries {
		if name := e.Name(); len(name) >= 10 && name[:10] < cutoff {
			os.Remove(filepath.Join(dir, name))
		}
	}
}
