package browse

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func markedFolderModel(t *testing.T) Model {
	t.Helper()
	f := foldersStore()
	m := newTestModel(t, f)
	if err := m.store.SetContainerCounted(202, false); err != nil {
		t.Fatalf("SetContainerCounted: %v", err)
	}
	m.reload()
	return m
}

func nameColumnOf(t *testing.T, line, name string) int {
	t.Helper()
	before, _, found := strings.Cut(ansi.Strip(line), name)
	if !found {
		t.Fatalf("no %q in %q", name, line)
	}
	return ansi.StringWidth(before)
}

func TestExcludedDeckInAFolderWearsTheMarkBesideItsName(t *testing.T) {
	m := markedFolderModel(t)
	line := ansi.Strip(containerLineFor(t, m, "Rich Deck"))

	before, _, found := strings.Cut(line, "Rich Deck")
	if !found {
		t.Fatalf("no deck row: %q", line)
	}
	trimmed := strings.TrimRight(before, " ")
	if !strings.HasSuffix(trimmed, settlingMark) {
		t.Errorf("row = %q, want the %q sitting just before the name, not out in the gutter",
			line, settlingMark)
	}
	if gap := len(before) - len(trimmed); gap != 1 {
		t.Errorf("row = %q, want exactly one space between the mark and the name, got %d",
			line, gap)
	}
}

func TestMarkingAnExcludedDeckKeepsTheFolderNamesAligned(t *testing.T) {
	m := markedFolderModel(t)

	marked := nameColumnOf(t, containerLineFor(t, m, "Rich Deck"), "Rich Deck")
	plain := nameColumnOf(t, containerLineFor(t, m, "Cheap Deck"), "Cheap Deck")
	if marked != plain {
		t.Errorf("nested deck names start at column %d when marked and %d when not; "+
			"the mark must not shift the name", marked, plain)
	}
}

func TestATopLevelExcludedDeckKeepsTheMarkInTheGutter(t *testing.T) {
	f := foldersStore()
	m := newTestModel(t, f)
	if err := m.store.SetContainerCounted(203, false); err != nil {
		t.Fatalf("SetContainerCounted: %v", err)
	}
	m.reload()

	line := ansi.Strip(containerLineFor(t, m, "Loose Deck"))
	if !strings.HasPrefix(line, settlingMark) {
		t.Errorf("row = %q, want an unnested deck to keep its mark in the gutter", line)
	}
}
