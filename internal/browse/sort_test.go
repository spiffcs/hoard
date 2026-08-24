package browse

import (
	"cmp"
	"slices"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

func sortBy(t *testing.T, m Model, want string) Model {
	t.Helper()
	for range 12 {
		if m.sortLabel() == want {
			return m
		}
		m = key(m, "s")
	}
	t.Fatalf("never reached the %q sort; stopped at %q", want, m.sortLabel())
	return m
}

func TestSortBySetOrdersByCollectorNumber(t *testing.T) {
	f := &fakeStore{collection: []store.CollectionRow{
		row("Bravo", "mh3", "10", finish.Nonfoil, 1, 50),
		row("Alpha", "mh3", "9", finish.Nonfoil, 1, 5),
		row("Charlie", "mh3", "2", finish.Nonfoil, 1, 30),
		row("Delta", "c21", "7", finish.Nonfoil, 1, 1),
	}}
	m := sortBy(t, atAllCards(t, newTestModel(t, f)), "set")

	want := []string{"Delta", "Charlie", "Alpha", "Bravo"}
	if got := cardNames(m.filteredCards); !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v — c21 first, then mh3 by collector number 2, 9, 10", got, want)
	}

	m = key(m, "S")
	slices.Reverse(want)
	if got := cardNames(m.filteredCards); !slices.Equal(got, want) {
		t.Errorf("reversed order = %v, want %v — the same order upside down", got, want)
	}
}

func TestUnownedViewOffersNoFinishOrQuantitySort(t *testing.T) {
	m := key(eoeModel(t, eoeStore()), "b")

	seen := map[string]bool{}
	for range 10 {
		seen[m.sortLabel()] = true
		m = key(m, "s")
	}
	var labels []string
	for k := range seen {
		labels = append(labels, k)
	}
	slices.Sort(labels)

	for _, gone := range []string{"finish", "qty"} {
		if seen[gone] {
			t.Errorf("%q is still offered in the unowned view · sorts seen: %s",
				gone, strings.Join(labels, ", "))
		}
	}
	for _, kept := range []string{"value", "name", "set", "price"} {
		if !seen[kept] {
			t.Errorf("%q went missing from the unowned view · sorts seen: %s",
				kept, strings.Join(labels, ", "))
		}
	}
}

func TestOwnedViewKeepsEverySort(t *testing.T) {
	m := eoeModel(t, eoeStore())

	seen := map[string]bool{}
	for range 10 {
		seen[m.sortLabel()] = true
		m = key(m, "s")
	}
	for _, kept := range []string{"value", "name", "set", "finish", "qty", "price"} {
		if !seen[kept] {
			t.Errorf("%q is missing from the owned view, which still splits by finish", kept)
		}
	}
}

func TestComparePrintingOrdersCollectorNumbersNaturally(t *testing.T) {
	ordered := []string{"1", "2", "7", "7a", "7b", "9", "10", "64s", "312", "312★", "GR1", "T2"}
	for i := range ordered {
		for j := range ordered {
			got := comparePrinting("mh3", ordered[i], "mh3", ordered[j])
			want := cmp.Compare(i, j)
			if got != want {
				t.Errorf("comparePrinting(%q, %q) = %d, want %d — %q sorts before %q",
					ordered[i], ordered[j], got, want, ordered[min(i, j)], ordered[max(i, j)])
			}
		}
	}
}

func TestComparePrintingPutsTheSetFirst(t *testing.T) {
	if comparePrinting("c21", "999", "mh3", "1") >= 0 {
		t.Error("c21/999 must sort before mh3/1 — the set is the primary key")
	}
	if comparePrinting("mh3", "1", "mh3", "1") != 0 {
		t.Error("the same printing must compare equal")
	}
}

func printedAs(rows []card) []string {
	out := make([]string, len(rows))
	for i, c := range rows {
		out[i] = c.Name + "/" + c.CollectorNumber
	}
	return out
}

func printing(name, num string, value float64) store.CollectionRow {
	r := row(name, "eoe", num, finish.Nonfoil, 1, value)
	r.ScryfallID = name + "-" + num
	return r
}

func manyPrintings() *fakeStore {
	return &fakeStore{collection: []store.CollectionRow{
		printing("Ugin", "312", 50),
		printing("Sami", "9", 5),
		printing("Ugin", "1", 12),
		printing("Ugin", "44", 30),
	}}
}

func TestSortByNameBreaksTiesOnCollectorNumber(t *testing.T) {
	m := sortBy(t, eoeModel(t, manyPrintings()), "name")

	want := []string{"Sami/9", "Ugin/1", "Ugin/44", "Ugin/312"}
	if got := printedAs(m.filteredCards); !slices.Equal(got, want) {
		t.Errorf("by name = %v, want %v — same name, ascending collector number", got, want)
	}

	m = key(m, "S")
	want = []string{"Ugin/1", "Ugin/44", "Ugin/312", "Sami/9"}
	if got := printedAs(m.filteredCards); !slices.Equal(got, want) {
		t.Errorf("by name reversed = %v, want %v — names flip, printings stay ascending", got, want)
	}
}

func trendPrintedAs(rows []store.TrendRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name + "/" + r.CollectorNumber
	}
	return out
}

func numberedDip(name, num string, high, last float64) store.TrendRow {
	r := dipRow(name, high, last, last)
	r.CollectorNumber = num
	r.ScryfallID = name + num
	return r
}

func TestDipSortByNameBreaksTiesOnCollectorNumber(t *testing.T) {
	m := onDipRows(t, []store.TrendRow{
		numberedDip("Ugin", "312", 100, 50),
		numberedDip("Sami", "9", 100, 40),
		numberedDip("Ugin", "1", 100, 70),
		numberedDip("Ugin", "44", 100, 60),
	}, nil)

	m = sortBy(t, m, "DIP · name")
	want := []string{"Sami/9", "Ugin/1", "Ugin/44", "Ugin/312"}
	if got := trendPrintedAs(m.dips); !slices.Equal(got, want) {
		t.Errorf("by name = %v, want %v", got, want)
	}

	m = key(m, "S")
	want = []string{"Ugin/1", "Ugin/44", "Ugin/312", "Sami/9"}
	if got := trendPrintedAs(m.dips); !slices.Equal(got, want) {
		t.Errorf("by name reversed = %v, want %v — names flip, printings stay ascending", got, want)
	}
}
