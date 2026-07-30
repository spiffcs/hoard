package export

import (
	"strings"
	"testing"
)

func f(v float64) *float64 { return &v }

// rows deliberately arrive unsorted and split across containers, since the
// writers promise deterministic output and Moxfield/Archidekt aggregation.
func fixture() []Row {
	return []Row{
		{Count: 1, Name: "Sol Ring", Set: "c21", CollectorNumber: "125", Finish: "foil",
			ScryfallID: "sol-1", Container: "Trade", Board: "main", PriceUSD: f(12.5)},
		{Count: 2, Name: "Sol Ring", Set: "c21", CollectorNumber: "125", Finish: "normal",
			ScryfallID: "sol-1", Container: "Binder", Board: "main", PriceUSD: f(2)},
		{Count: 1, Name: "Sol Ring", Set: "c21", CollectorNumber: "125", Finish: "normal",
			ScryfallID: "sol-1", Container: "Trade", Board: "main", PriceUSD: f(2)},
		{Count: 1, Name: "Mystic Remora", Set: "ice", CollectorNumber: "78", Finish: "normal",
			ScryfallID: "remora-1", Container: "Binder", Board: "main", PriceUSD: nil},
		{Count: 1, Name: "Aether Vial", Set: "dst", CollectorNumber: "91", Finish: "etched",
			ScryfallID: "vial-1", Container: "Vintage", Board: "main", PriceUSD: f(30)},
	}
}

func TestWriteCanonicalIsSortedAndKeepsContainers(t *testing.T) {
	var b strings.Builder
	if err := WriteCanonical(&b, fixture()); err != nil {
		t.Fatalf("WriteCanonical: %v", err)
	}
	want := strings.Join([]string{
		"Count,Name,Set,Collector Number,Finish,Scryfall ID,Container,Board,Price USD",
		"1,Mystic Remora,ice,78,normal,remora-1,Binder,main,", // unpriced: empty cell, not 0.00
		"2,Sol Ring,c21,125,normal,sol-1,Binder,main,2.00",
		"1,Sol Ring,c21,125,foil,sol-1,Trade,main,12.50",
		"1,Sol Ring,c21,125,normal,sol-1,Trade,main,2.00",
		"1,Aether Vial,dst,91,etched,vial-1,Vintage,main,30.00",
		"",
	}, "\n")
	if b.String() != want {
		t.Errorf("canonical CSV:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestWriteMoxfieldAggregatesAndMapsFoil(t *testing.T) {
	var b strings.Builder
	if err := WriteMoxfield(&b, fixture()); err != nil {
		t.Fatalf("WriteMoxfield: %v", err)
	}
	// The two normal Sol Rings merge across binders; etched stays a distinct
	// finish word in the Foil column; normal maps to an empty cell.
	want := strings.Join([]string{
		"Count,Name,Edition,Condition,Language,Foil,Collector Number",
		"1,Aether Vial,dst,Near Mint,English,etched,91",
		"1,Mystic Remora,ice,Near Mint,English,,78",
		"1,Sol Ring,c21,Near Mint,English,foil,125",
		"3,Sol Ring,c21,Near Mint,English,,125",
		"",
	}, "\n")
	if b.String() != want {
		t.Errorf("moxfield CSV:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestWriteArchidektAggregatesAndCapitalizesFinish(t *testing.T) {
	var b strings.Builder
	if err := WriteArchidekt(&b, fixture()); err != nil {
		t.Fatalf("WriteArchidekt: %v", err)
	}
	want := strings.Join([]string{
		"Quantity,Name,Finish,Edition Code,Collector Number,Scryfall ID",
		"1,Aether Vial,Etched,dst,91,vial-1",
		"1,Mystic Remora,Normal,ice,78,remora-1",
		"1,Sol Ring,Foil,c21,125,sol-1",
		"3,Sol Ring,Normal,c21,125,sol-1",
		"",
	}, "\n")
	if b.String() != want {
		t.Errorf("archidekt CSV:\n%s\nwant:\n%s", b.String(), want)
	}
}

// A name containing a comma must survive the trip: encoding/csv quotes it.
func TestFieldsWithCommasAreQuoted(t *testing.T) {
	rows := []Row{{Count: 1, Name: "Borrowing 100,000 Arrows", Set: "ptk",
		CollectorNumber: "31", Finish: "normal", ScryfallID: "arrows-1",
		Container: "Binder", Board: "main"}}
	var b strings.Builder
	if err := WriteCanonical(&b, rows); err != nil {
		t.Fatalf("WriteCanonical: %v", err)
	}
	if !strings.Contains(b.String(), `"Borrowing 100,000 Arrows"`) {
		t.Errorf("comma-bearing name was not quoted:\n%s", b.String())
	}
}

// The input slice must come back untouched — callers may hold rows in their
// own order.
func TestWritersDoNotReorderTheCallersSlice(t *testing.T) {
	rows := fixture()
	first := rows[0]
	var b strings.Builder
	if err := WriteMoxfield(&b, rows); err != nil {
		t.Fatalf("WriteMoxfield: %v", err)
	}
	if rows[0] != first {
		t.Errorf("caller's slice was mutated: rows[0] = %+v", rows[0])
	}
}
