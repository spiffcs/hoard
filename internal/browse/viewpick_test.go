package browse

import "testing"

func pickContainer(t *testing.T, m Model, name string) Model {
	t.Helper()
	m.focus = paneContainers
	for i, c := range m.containers {
		if c.Name != name {
			continue
		}
		m.moveTo(i)
		return m
	}
	t.Fatalf("no container named %q in %v", name, containerNames(m))
	return m
}

func containerNames(m Model) []string {
	var out []string
	for _, c := range m.containers {
		out = append(out, c.Name)
	}
	return out
}

func selectedName(m Model) string {
	if sel := m.selectedContainer(); sel != nil {
		return sel.Name
	}
	return ""
}

func TestViewCycleKeepsSelectedCollection(t *testing.T) {
	m := newTestModel(t, testStore())
	m = pickContainer(t, m, "Rich Deck")

	for i := range viewCycle {
		m = key(m, "v")
		if m.view == viewWatches {
			continue
		}
		if got := selectedName(m); got != "Rich Deck" {
			t.Fatalf("after %d view cycles (%s), selected %q, want Rich Deck",
				i+1, m.view, got)
		}
	}
	if m.view != viewHoldings {
		t.Fatalf("cycle ended on %s, want holdings", m.view)
	}
}

func TestViewCycleRestoresCollectionAfterIneligibleView(t *testing.T) {
	m := newTestModel(t, testStore())
	m = pickContainer(t, m, "Rich Deck")

	m.showView(viewWatches)
	if got := selectedName(m); got != allCardsName {
		t.Fatalf("watches selected %q, want the %s fallback", got, allCardsName)
	}

	m.showView(viewHoldings)
	if got := selectedName(m); got != "Rich Deck" {
		t.Fatalf("back on holdings selected %q, want Rich Deck", got)
	}
	if len(m.cards) == 0 {
		t.Fatal("restoring the collection left the card pane empty")
	}
}

func TestPickingCollectionInAnotherViewSticks(t *testing.T) {
	m := newTestModel(t, testStore())
	m = pickContainer(t, m, "Rich Deck")

	m.showView(viewMovers)
	m = pickContainer(t, m, "Cheap Deck")

	m.showView(viewWatches)
	m.showView(viewHoldings)
	if got := selectedName(m); got != "Cheap Deck" {
		t.Fatalf("selected %q, want the Cheap Deck picked while in movers", got)
	}
}
