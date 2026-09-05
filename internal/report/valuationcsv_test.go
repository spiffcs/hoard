package report

import (
	"encoding/csv"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

func usd(v float64) *float64 { return &v }

func valuationRows(t *testing.T, owned []store.OwnedFinish) ([]string, [][]string) {
	t.Helper()
	var b strings.Builder
	if err := ValuationCSV(&b, "2026-09-04T00:00:00Z", owned); err != nil {
		t.Fatalf("ValuationCSV: %v", err)
	}
	recs, err := csv.NewReader(strings.NewReader(b.String())).ReadAll()
	if err != nil {
		t.Fatalf("the CSV does not parse: %v", err)
	}
	if len(recs) < 2 {
		t.Fatalf("got %d records, want a header and at least one row", len(recs))
	}
	return recs[0], recs[1:]
}

func column(t *testing.T, header []string, name string) int {
	t.Helper()
	for i, h := range header {
		if h == name {
			return i
		}
	}
	t.Fatalf("no %q column in %v", name, header)
	return -1
}

func TestValuationCSVLeavesAnUnpricedHoldingBlankNotZero(t *testing.T) {
	owned := []store.OwnedFinish{
		{Name: "Sol Ring", SetCode: "c21", CollectorNumber: "263",
			Finish: finish.Nonfoil, Copies: 2, Value: 3.66, UnitPrice: usd(1.83)},
		{Name: "Acidic Slime", SetCode: "m3c", CollectorNumber: "218",
			Finish: finish.Foil, Copies: 4, Value: 0},
	}
	header, rows := valuationRows(t, owned)
	unit, value := column(t, header, "Unit Price USD"), column(t, header, "Value USD")

	var priced, unpriced []string
	for _, r := range rows {
		switch r[0] {
		case "Sol Ring":
			priced = r
		case "Acidic Slime":
			unpriced = r
		}
	}
	if priced == nil || unpriced == nil {
		t.Fatalf("rows = %v, want both holdings", rows)
	}

	if priced[unit] != "1.83" || priced[value] != "3.66" {
		t.Errorf("priced row each=%q value=%q, want 1.83 and 3.66",
			priced[unit], priced[value])
	}
	if unpriced[unit] != "" {
		t.Errorf("unpriced each = %q, want an empty cell: an absent price is not $0.00",
			unpriced[unit])
	}
	if unpriced[value] != "" {
		t.Errorf("unpriced value = %q, want an empty cell, not a claim the copies are free",
			unpriced[value])
	}
}
