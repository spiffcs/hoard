package browse

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/ui"
)

func plainSetList(t *testing.T, sets, height int) Model {
	t.Helper()
	m := newTestModel(t, testStore())
	m.imgTier = ui.ImageNone
	m.imageFetch = nil
	m.containers = manySets(sets)
	m.cursor[paneContainers] = 0
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: height})
	m = next.(Model)
	m.focus = paneContainers
	return m
}

func TestPageUpKeepsTheTopSetRowWithNoArtShowing(t *testing.T) {
	m := plainSetList(t, 715, 31)
	if m.previewRows() != 0 {
		t.Fatalf("this test needs the art pane off; previewRows = %d", m.previewRows())
	}
	m = key(m, "end")

	lo, hi, total := setWindow(t, m)
	if total != 715 || hi != 715 {
		t.Fatalf("after end the window is %d–%d of %d, want it to reach the last set", lo, hi, total)
	}

	m = key(m, "pgup")
	lo2, hi2, _ := setWindow(t, m)

	if hi2 != lo {
		t.Errorf("page up moved the window from %d–%d to %d–%d; row %d is at the top before "+
			"the keypress, so it must be at the bottom after it", lo, hi, lo2, hi2, lo)
	}
}

func setWindow(t *testing.T, m Model) (lo, hi, total int) {
	t.Helper()
	note := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m.containerScrollNote()), "·"))
	if _, err := fmt.Sscanf(note, "%d–%d of %d", &lo, &hi, &total); err != nil {
		t.Fatalf("containerScrollNote() = %q, cannot read a window from it: %v", note, err)
	}
	return lo, hi, total
}

func TestPageUpPutsTheTopSetRowAtTheBottom(t *testing.T) {
	m, _ := previewModel(t, 715, 70)
	m.focus = paneContainers
	m = key(m, "end")

	lo, hi, total := setWindow(t, m)
	if total != 715 || hi != 715 {
		t.Fatalf("after end the window is %d–%d of %d, want it to reach the last set", lo, hi, total)
	}

	m = key(m, "pgup")
	lo2, hi2, _ := setWindow(t, m)

	if hi2 != lo {
		t.Errorf("page up moved the window from %d–%d to %d–%d; row %d is at the top before "+
			"the keypress, so it must be at the bottom after it", lo, hi, lo2, hi2, lo)
	}
	if got, want := hi2-lo2, hi-lo; got != want {
		t.Errorf("the window changed size across the page: %d rows then %d", want+1, got+1)
	}
}

func TestPageDownPutsTheBottomSetRowAtTheTop(t *testing.T) {
	m, _ := previewModel(t, 715, 70)
	m.focus = paneContainers
	m = key(m, "home")

	lo, hi, _ := setWindow(t, m)
	if lo != 1 {
		t.Fatalf("after home the window is %d–%d, want it to start at the first set", lo, hi)
	}

	m = key(m, "pgdown")
	lo2, hi2, _ := setWindow(t, m)

	if lo2 != hi {
		t.Errorf("page down moved the window from %d–%d to %d–%d; row %d is at the bottom "+
			"before the keypress, so it must be at the top after it", lo, hi, lo2, hi2, hi)
	}
	if got, want := hi2-lo2, hi-lo; got != want {
		t.Errorf("the window changed size across the page: %d rows then %d", want+1, got+1)
	}
}

func TestPagingTheSetListWalksEveryRow(t *testing.T) {
	m, _ := previewModel(t, 715, 70)
	m.focus = paneContainers
	m = key(m, "home")

	seen := map[int]bool{}
	for range 200 {
		lo, hi, _ := setWindow(t, m)
		for r := lo; r <= hi; r++ {
			seen[r] = true
		}
		if hi == 715 {
			break
		}
		m = key(m, "pgdown")
	}
	for r := 1; r <= 715; r++ {
		if !seen[r] {
			t.Fatalf("paging down from the top never showed set %d of 715", r)
		}
	}
}
