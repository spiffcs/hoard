package command

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture store has no price history, so the report's as-of date falls
// back to the cards' fetch stamp — real but not fixed, which is why these
// tests check structure rather than exact bytes.

func TestCmdReportText(t *testing.T) {
	st := exportStore(t)
	out := filepath.Join(t.TempDir(), "report.txt")
	if _, err := execCmd(context.Background(), st, []string{"report", "-o", out}, false); err != nil {
		t.Fatalf("hoard report: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, want := range []string{
		"VALUATION · prices as of ",
		"BINDERS", "Trade",
		"TOP 3 HOLDINGS", "Sol Ring",
		"PRICE SOURCES", "scryfall", "unpriced",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("report is missing %q:\n%s", want, got)
		}
	}
}

func TestCmdReportJSON(t *testing.T) {
	st := exportStore(t)
	out := filepath.Join(t.TempDir(), "report.json")
	if _, err := execCmd(context.Background(), st, []string{"report", "-o", out}, true); err != nil {
		t.Fatalf("hoard report: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var doc struct {
		SchemaVersion string `json:"schemaVersion"`
		Kind          string `json:"kind"`
		Report        struct {
			AsOf  string `json:"asOf"`
			Total struct {
				TotalCopies int     `json:"totalCopies"`
				ValueUsd    float64 `json:"valueUsd"`
			} `json:"total"`
			Binders     []struct{ Name string }
			TopHoldings []struct {
				Card struct {
					Name   string `json:"name"`
					Finish string `json:"finish"`
				} `json:"card"`
				PriceUsd *float64 `json:"priceUsd"`
			} `json:"topHoldings"`
			Sources []struct {
				Source    string `json:"source"`
				Printings int    `json:"printings"`
				Copies    int    `json:"copies"`
			} `json:"sources"`
			Unpriced struct {
				Printings int `json:"printings"`
				Copies    int `json:"copies"`
			} `json:"unpriced"`
		} `json:"report"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	r := doc.Report
	if doc.Kind != "report" || doc.SchemaVersion == "" {
		t.Errorf("envelope = %s/%s", doc.SchemaVersion, doc.Kind)
	}
	if r.AsOf == "" {
		t.Error("asOf is empty; the fetch-stamp fallback did not fire")
	}
	if r.Total.TotalCopies != 4 || r.Total.ValueUsd != 16.5 {
		t.Errorf("total = %+v, want 4 copies / 16.5", r.Total)
	}
	if len(r.Binders) != 2 {
		t.Errorf("binders = %+v, want the default and Trade", r.Binders)
	}
	if len(r.TopHoldings) != 3 || r.TopHoldings[0].Card.Name != "Sol Ring" ||
		r.TopHoldings[0].Card.Finish != "foil" {
		t.Errorf("topHoldings = %+v, want the foil Sol Ring first", r.TopHoldings)
	}
	if last := r.TopHoldings[2]; last.PriceUsd != nil {
		t.Errorf("the unpriced holding carries priceUsd %v", *last.PriceUsd)
	}
	if len(r.Sources) != 1 || r.Sources[0].Source != "scryfall" ||
		r.Sources[0].Printings != 2 || r.Sources[0].Copies != 3 {
		t.Errorf("sources = %+v, want scryfall covering 2 printings / 3 copies", r.Sources)
	}
	if r.Unpriced.Printings != 1 || r.Unpriced.Copies != 1 {
		t.Errorf("unpriced = %+v, want 1 printing / 1 copy", r.Unpriced)
	}
}

func TestCmdReportCSV(t *testing.T) {
	st := exportStore(t)
	out := filepath.Join(t.TempDir(), "report.csv")
	if _, err := execCmd(context.Background(), st, []string{"report", "--csv", "-o", out}, false); err != nil {
		t.Fatalf("hoard report: %v", err)
	}
	got, _ := os.ReadFile(out)
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 4 {
		t.Fatalf("valuation CSV has %d lines, want header + 3 rows:\n%s", len(lines), got)
	}
	if lines[0] != "Name,Set,Collector Number,Finish,Copies,Unit Price USD,Value USD,As Of" {
		t.Errorf("header = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "Sol Ring,c21,125,foil,1,12.50,12.50,") {
		t.Errorf("first row = %q, want the foil Sol Ring", lines[1])
	}
}

func TestCmdReportRejectsCSVWithJSON(t *testing.T) {
	st := exportStore(t)
	if _, err := execCmd(context.Background(), st, []string{"report", "--csv"}, true); err == nil {
		t.Error("hoard report --csv --json succeeded, want an error")
	}
}
