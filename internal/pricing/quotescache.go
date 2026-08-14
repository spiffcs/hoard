package pricing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/spiffcs/hoard/internal/mtgjson"
)

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
			return nil, false
		}
		if qs, ok := doc.Quotes[r.ScryfallID]; ok {
			out[r.ScryfallID] = qs
		}
	}
	return out, true
}

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
