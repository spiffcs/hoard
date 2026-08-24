package demo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/store"
)

const HistoryKind = "demo-price-history"

type HistoryPoint struct {
	Date  string  `json:"date"`
	Price float64 `json:"price"`
}

type HistorySeries struct {
	ScryfallID string         `json:"scryfallId"`
	Finish     finish.Finish  `json:"finish"`
	Source     string         `json:"source"`
	Points     []HistoryPoint `json:"points"`
}

type HistoryDocument struct {
	Kind string `json:"kind"`

	GeneratedAt string          `json:"generatedAt"`
	Retail      []HistorySeries `json:"retail"`
	Bids        []HistorySeries `json:"bids,omitempty"`
}

type SeedHistoryResult struct {
	Inserted, Cards       int
	BidInserted, BidCards int

	GeneratedAt string
}

func ReadHistory(r io.Reader) (HistoryDocument, error) {
	var doc HistoryDocument
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return doc, fmt.Errorf("reading price history: %w", err)
	}
	if doc.Kind != HistoryKind {
		return doc, fmt.Errorf("not a %s document: kind is %q", HistoryKind, doc.Kind)
	}
	return doc, nil
}

func SeedHistory(st *store.Store, doc HistoryDocument) (SeedHistoryResult, error) {
	out := SeedHistoryResult{GeneratedAt: doc.GeneratedAt}
	var err error
	if out.Inserted, out.Cards, err = st.BackfillPrices(observations(doc.Retail)); err != nil {
		return out, fmt.Errorf("recording sample price history: %w", err)
	}
	if out.BidInserted, out.BidCards, err = st.BackfillBids(observations(doc.Bids)); err != nil {
		return out, fmt.Errorf("recording sample buylist history: %w", err)
	}
	return out, nil
}

func SeedEmbeddedHistory(st *store.Store) (SeedHistoryResult, error) {
	doc, err := ReadHistory(bytes.NewReader(History))
	if err != nil {
		return SeedHistoryResult{}, fmt.Errorf("the built-in sample price history: %w", err)
	}
	return SeedHistory(st, doc)
}

func TopUpHistory(st *store.Store) (res SeedHistoryResult, seeded bool, err error) {
	obs, _, err := st.PriceHistoryDepth()
	if err != nil || obs > 0 {
		return SeedHistoryResult{}, false, err
	}
	res, err = SeedEmbeddedHistory(st)
	if err != nil {
		return SeedHistoryResult{}, false, err
	}
	return res, res.Inserted+res.BidInserted > 0, nil
}

func observations(series []HistorySeries) map[string][]mtgjson.Observation {
	if len(series) == 0 {
		return nil
	}
	byCard := map[string][]mtgjson.Observation{}
	for _, s := range series {
		for _, p := range s.Points {
			byCard[s.ScryfallID] = append(byCard[s.ScryfallID], mtgjson.Observation{
				Date: p.Date, Finish: s.Finish, Price: p.Price, Source: s.Source,
			})
		}
	}
	return byCard
}
