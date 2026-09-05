package collsource

import (
	"strings"
	"testing"
)

const manaboxPaidHeader = "Binder Name,Name,Set code,Collector number,Quantity," +
	"Foil,Condition,Language,Scryfall ID,Purchase price,Purchase price currency\n"

func parsePaidCSV(t *testing.T, rows string) *Collection {
	t.Helper()
	c, err := Parse(strings.NewReader(manaboxPaidHeader+rows), "auto")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return c
}

func TestAnAmountHoardCannotReadIsReportedNotDiscarded(t *testing.T) {
	c := parsePaidCSV(t,
		"Trade,Sol Ring,c21,263,1,normal,near_mint,en,sol-id,12.34 USD,USD\n"+
			"Trade,Ancient Tomb,uma,236,1,normal,near_mint,en,tomb-id,,USD\n")

	if len(c.Rows) != 2 {
		t.Fatalf("got %d rows, want both cards imported", len(c.Rows))
	}
	if c.Rows[0].PurchasePrice != nil {
		t.Errorf("sol ring paid = %v, want nothing recorded", *c.Rows[0].PurchasePrice)
	}
	if got := c.Dropped["purchase price"]; got != 1 {
		t.Errorf("dropped purchase price = %d, want the unreadable amount counted once (dropped = %v)",
			got, c.Dropped)
	}
}

func TestAThousandsSeparatorIsStillAPrice(t *testing.T) {
	c := parsePaidCSV(t,
		"Trade,Black Lotus,lea,232,1,normal,near_mint,en,lotus-id,\"1,234.56\",USD\n")

	if len(c.Rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(c.Rows))
	}
	if c.Rows[0].PurchasePrice == nil {
		t.Fatal("a thousands separator was treated as unreadable")
	}
	if got := *c.Rows[0].PurchasePrice; got != 1234.56 {
		t.Errorf("paid = %v, want 1234.56", got)
	}
	if got := c.Dropped["purchase price"]; got != 0 {
		t.Errorf("dropped = %d, want nothing dropped", got)
	}
}

func TestAnEmptyAmountIsNotADroppedOne(t *testing.T) {
	c := parsePaidCSV(t,
		"Trade,Sol Ring,c21,263,1,normal,near_mint,en,sol-id,,USD\n")

	if c.Rows[0].PurchasePrice != nil {
		t.Errorf("paid = %v, want nothing recorded", *c.Rows[0].PurchasePrice)
	}
	if got := c.Dropped["purchase price"]; got != 0 {
		t.Errorf("dropped = %d, want 0: no price given is not a lost price", got)
	}
}

func TestAEuropeanDecimalIsNeverReadAsThousands(t *testing.T) {
	c := parsePaidCSV(t,
		"Trade,Sol Ring,c21,263,1,normal,near_mint,en,sol-id,\"12,34\",USD\n")

	if p := c.Rows[0].PurchasePrice; p != nil {
		t.Fatalf("12,34 was read as %v; a comma there is ambiguous and must be reported, not guessed", *p)
	}
	if got := c.Dropped["purchase price"]; got != 1 {
		t.Errorf("dropped = %d, want the ambiguous amount reported once", got)
	}
}
