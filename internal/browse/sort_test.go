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
