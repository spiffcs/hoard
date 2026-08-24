package browse

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

func risingSeries(prices ...float64) []store.PricePoint {
	out := make([]store.PricePoint, len(prices))
	for i, p := range prices {
		out[i] = store.PricePoint{
			AsOf:  fmt.Sprintf("2026-06-%02dT00:00:00Z", i+1),
			Price: p, Source: "scryfall",
		}
	}
	return out
}

func detailWithSeries(t *testing.T, prices ...float64) string {
	t.Helper()
	f := testStore()
	f.priceSeries = map[string][]store.PricePoint{
		"Bitterblossom-id|nonfoil": risingSeries(prices...),
	}
	m := atAllCards(t, newTestModel(t, f))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 45})
	m = next.(Model)
	for i, c := range m.filteredCards {
		if c.ScryfallID == "Bitterblossom-id" && c.Finish == finish.Nonfoil {
			m.cursor[paneCards] = i
		}
	}
	m.focus = paneCards
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("enter did not open the card detail")
	}
	return strings.Join(m.detailLines(*m.detail, 120), "\n")
}

func TestDetailNamesALongRise(t *testing.T) {
	out := detailWithSeries(t, 10, 11, 12, 13, 14)
	if !strings.Contains(out, "4 up") {
		t.Errorf("a four-check rise is not called out on the detail page:\n%s", out)
	}
}

func TestDetailNamesALongFall(t *testing.T) {
	out := detailWithSeries(t, 20, 19, 18, 17)
	if !strings.Contains(out, "3 down") {
		t.Errorf("a three-check fall is not called out on the detail page:\n%s", out)
	}
}

func TestDetailStaysQuietAboutAShortRun(t *testing.T) {
	out := detailWithSeries(t, 10, 9, 10, 11)
	if strings.Contains(out, " up") || strings.Contains(out, " down") {
		t.Errorf("a two-check run is noise, not a streak:\n%s", out)
	}
	if !strings.Contains(out, "checks since") {
		t.Fatalf("the price caption is missing entirely:\n%s", out)
	}
}
