package pricing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/spiffcs/hoard/internal/mtgjson"
)

// The quotes day-cache. Vendor quotes come from a bundle that is ~50 MB
// decoded and covers every card in Magic, so even with the download cached a
// second read of the same day pays seconds of decompress-and-scan for the
// same answer. The filtered result — just this hoard's cards — is a few tens
// of kilobytes, so it is kept beside the bundles and served whole.
//
// The file is dated like the bundle cache ("2006-01-02-..."), which is what
// lets mtgjson's nightly prune collect it: tomorrow's quotes are tomorrow's
// download, and yesterday's file must not answer for them.

// quotesCacheDoc is the cached day: which Scryfall ids the parse covered, and
// the quotes it found. Asked is kept separately from the quotes map because
// absence from Quotes is an answer ("no vendor quoted it today") only for a
// card that was actually asked about — a card added since the parse must miss
// the cache, not read as quoteless.
type quotesCacheDoc struct {
	Asked  []string                   `json:"asked"`
	Quotes map[string][]mtgjson.Quote `json:"quotes"`
}

func (f *Fetcher) quotesCachePath() string {
	if f.cacheDir == "" {
		return ""
	}
	return filepath.Join(f.cacheDir, time.Now().Format("2006-01-02")+"-owned-quotes.json")
}

// cachedQuotes serves today's quotes from the day-cache, if every requested
// printing was covered when the file was written.
func (f *Fetcher) cachedQuotes(refs []Ref) (map[string][]mtgjson.Quote, bool) {
	path := f.quotesCachePath()
	if path == "" {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var doc quotesCacheDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false
	}
	asked := make(map[string]bool, len(doc.Asked))
	for _, id := range doc.Asked {
		asked[id] = true
	}
	out := make(map[string][]mtgjson.Quote, len(refs))
	for _, r := range refs {
		if !asked[r.ScryfallID] {
			return nil, false // a printing the parse never saw: go parse
		}
		if qs, ok := doc.Quotes[r.ScryfallID]; ok {
			out[r.ScryfallID] = qs
		}
	}
	return out, true
}

// saveQuotes writes the day-cache. Best-effort, like the bundle cache: a
// failure here costs a re-parse tomorrow's user never hears about.
func (f *Fetcher) saveQuotes(refs []Ref, quotes map[string][]mtgjson.Quote) {
	path := f.quotesCachePath()
	if path == "" {
		return
	}
	doc := quotesCacheDoc{Asked: make([]string, 0, len(refs)), Quotes: quotes}
	for _, r := range refs {
		doc.Asked = append(doc.Asked, r.ScryfallID)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return
	}
	if err := os.MkdirAll(f.cacheDir, 0o755); err != nil {
		return
	}
	// Write-then-rename, as the bundle cache does: a crash mid-write must not
	// leave a torn file that answers tomorrow's first read with garbage.
	tmp, err := os.CreateTemp(f.cacheDir, "quotes-*")
	if err != nil {
		return
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
	}
}
