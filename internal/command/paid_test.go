package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func paidStore(t *testing.T) *store.Store {
	t.Helper()
	st := roundTripStore(t)
	stubFetch(t, roundTripCards()...)
	if err := importCmd(st, "--preserve-binders", manaboxRoundTripFixture); err != nil {
		t.Fatalf("hoard import: %v", err)
	}
	return st
}

const fixtureSpend = 4.25*2 + 9.99 + 1.50 + 0.75 + 0.40 + 12.00 + 3.00

type paidHoldingsDoc struct {
	Holdings struct {
		Rows []struct {
			Card struct {
				Name   string `json:"name"`
				Finish string `json:"finish"`
			} `json:"card"`
			Count int      `json:"count"`
			Paid  *float64 `json:"paid"`
		} `json:"rows"`
	} `json:"holdings"`
}

func readPaidHoldings(t *testing.T, st *store.Store, args ...string) paidHoldingsDoc {
	t.Helper()
	out, err := execCmd(context.Background(), st, args, true)
	if err != nil {
		t.Fatalf("hoard %v --json: %v", args, err)
	}
	var doc paidHoldingsDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON (%v): %q", err, out)
	}
	return doc
}

func TestExportJSONCarriesWhatYouPaid(t *testing.T) {
	doc := readPaidHoldings(t, paidStore(t), "export", "--all")

	var withPaid int
	for _, r := range doc.Holdings.Rows {
		if r.Paid != nil {
			withPaid++
		}
	}
	if withPaid == 0 {
		t.Fatalf("no holding carried a paid field; the scripting surface cannot "+
			"see cost basis at all:\n%+v", doc.Holdings.Rows)
	}
	if withPaid != len(doc.Holdings.Rows) {
		t.Errorf("%d of %d rows carried paid; every row in this fixture has one",
			withPaid, len(doc.Holdings.Rows))
	}
}

func TestExportJSONOmitsPaidWhenNobodyRecordedIt(t *testing.T) {
	doc := readPaidHoldings(t, exportStore(t), "export", "--all")
	if len(doc.Holdings.Rows) == 0 {
		t.Fatal("fixture exported nothing")
	}
	for _, r := range doc.Holdings.Rows {
		if r.Paid != nil {
			t.Errorf("%s carries paid=%v but nobody recorded a cost basis",
				r.Card.Name, *r.Paid)
		}
	}
}

func TestExportFilterNarrowsByPaid(t *testing.T) {
	st := paidStore(t)

	dear := readPaidHoldings(t, st, "export", "--all", "--filter", "paid>5")
	if len(dear.Holdings.Rows) == 0 {
		t.Fatal("paid>5 matched nothing; the fixture holds a 9.99 and a 12.00")
	}
	for _, r := range dear.Holdings.Rows {
		if r.Paid == nil || *r.Paid <= 5 {
			t.Errorf("paid>5 kept %s at %v", r.Card.Name, r.Paid)
		}
	}

	cheap := readPaidHoldings(t, st, "export", "--all", "--filter", "paid<1")
	if len(cheap.Holdings.Rows) == 0 {
		t.Fatal("paid<1 matched nothing; the fixture holds a 0.40 and a 0.75")
	}
	for _, r := range cheap.Holdings.Rows {
		if r.Paid == nil || *r.Paid >= 1 {
			t.Errorf("paid<1 kept %s at %v", r.Card.Name, r.Paid)
		}
	}
}

func TestPaidFilterExcludesHoldingsWithNoCostBasis(t *testing.T) {
	doc := readPaidHoldings(t, exportStore(t), "export", "--all", "--filter", "paid>0")
	if n := len(doc.Holdings.Rows); n != 0 {
		t.Errorf("paid>0 kept %d rows in a hoard where nothing has a cost basis; "+
			"an unrecorded price is not a zero", n)
	}
}

func stubFetchCard(t *testing.T, cards ...scryfall.Card) {
	t.Helper()
	index := map[string]scryfall.Card{}
	for _, c := range cards {
		index[strings.ToLower(c.Set)+"/"+c.CollectorNumber] = c
	}
	old := cardResolver.Card
	t.Cleanup(func() { cardResolver.Card = old })
	cardResolver.Card = func(_ context.Context, set, number, _ string) (*scryfall.Card, error) {
		c, ok := index[strings.ToLower(set)+"/"+number]
		if !ok {
			return nil, fmt.Errorf("no stub card for %s/%s", set, number)
		}
		return &c, nil
	}
}

func TestAddRecordsWhatYouPaid(t *testing.T) {
	st := roundTripStore(t)
	stubFetch(t, roundTripCards()...)
	stubFetchCard(t, roundTripCards()...)

	_, err := execCmd(context.Background(), st,
		[]string{"add", "https://scryfall.com/card/c21/125/sol-ring", "--paid", "12.50"}, false)
	if err != nil {
		t.Fatalf("hoard add --paid: %v", err)
	}

	holdings, err := st.HoldingsOf("sol-id-1")
	if err != nil {
		t.Fatalf("HoldingsOf: %v", err)
	}
	if len(holdings) != 1 {
		t.Fatalf("holdings = %+v, want the one Sol Ring", holdings)
	}
	if holdings[0].PurchasePrice == nil || *holdings[0].PurchasePrice != 12.50 {
		t.Errorf("purchase price = %v, want 12.50", holdings[0].PurchasePrice)
	}
}

func TestAddRefusesPaidWhereItCannotApplyPerCopy(t *testing.T) {
	st := roundTripStore(t)
	stubFetch(t, roundTripCards()...)

	list := filepath.Join(t.TempDir(), "list.txt")
	if err := os.WriteFile(list, []byte("1 Sol Ring\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := execCmd(context.Background(), st,
		[]string{"add", "--file", list, "--paid", "3.00"}, false)
	if err == nil {
		t.Fatal("hoard add --file --paid was accepted; a list names its own copies")
	}
	if strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("--paid does not exist yet: %v", err)
	}
	if !strings.Contains(err.Error(), "--paid") {
		t.Errorf("refusal was %q, want it to name --paid", err)
	}
}

func TestCollectionTotalsCarryWhatWasSpent(t *testing.T) {
	st := paidStore(t)

	totals, err := st.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if totals.Spent == 0 {
		t.Fatalf("totals report no spend against %d copies worth %.2f",
			totals.TotalCopies, totals.Value)
	}
	if diff := totals.Spent - fixtureSpend; diff > 0.005 || diff < -0.005 {
		t.Errorf("spent = %.2f, want %.2f", totals.Spent, fixtureSpend)
	}
}

func TestCollectionTotalsSpendIsZeroWithoutACostBasis(t *testing.T) {
	totals, err := exportStore(t).CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if totals.Spent != 0 {
		t.Errorf("spent = %v in a hoard where nobody recorded a price", totals.Spent)
	}
}

func reportText(t *testing.T, st *store.Store) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "report.txt")
	if _, err := execCmd(context.Background(), st, []string{"report", "-o", out}, false); err != nil {
		t.Fatalf("hoard report: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(b)
}

func TestReportShowsSpendOnlyWhenThereIsSome(t *testing.T) {
	bare := reportText(t, exportStore(t))
	if strings.Contains(strings.ToUpper(bare), "COST BASIS") {
		t.Errorf("a hoard with no recorded price grew a cost basis section:\n%s", bare)
	}
	if strings.Contains(strings.ToLower(bare), "spent") {
		t.Errorf("a hoard with no recorded price mentions spend:\n%s", bare)
	}

	withPaid := reportText(t, paidStore(t))
	if !strings.Contains(strings.ToUpper(withPaid), "COST BASIS") {
		t.Errorf("a hoard that recorded prices shows no cost basis section:\n%s", withPaid)
	}
	if want := ui.Money(fixtureSpend); !strings.Contains(withPaid, want) {
		t.Errorf("report does not show the %s spent:\n%s", want, withPaid)
	}
}
