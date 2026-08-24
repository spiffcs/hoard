package browse

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/spiffcs/hoard/internal/ui"
)

const artMark = "▀"

func cardShapedArt() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 61, 85))
	for y := range 85 {
		for x := range 61 {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	return img
}

type artRecorder struct {
	mu    sync.Mutex
	asked []string
}

func (r *artRecorder) fetch(_ context.Context, id, url string) (image.Image, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append(r.asked, id)
	if url == "" {
		return nil, fmt.Errorf("no image url for %s", id)
	}
	return cardShapedArt(), nil
}

func (r *artRecorder) ids() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.asked)
}

func manySets(n int) []container {
	out := make([]container, n)
	for i := range n {
		out[i] = container{ID: int64(i + 1), Name: fmt.Sprintf("Set %02d", i+1),
			Kind: kindSet, Counted: true, Value: float64(i)}
	}
	return out
}

func previewModel(t *testing.T, sets, height int) (Model, *artRecorder) {
	t.Helper()
	rec := &artRecorder{}
	m := newTestModel(t, testStore())
	m.imgTier = ui.ImageHalfblock
	m.imageFetch = rec.fetch
	m.containers = manySets(sets)
	m.cursor[paneContainers] = 0
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: height})
	return pump(t, next.(Model), cmd), rec
}

func leftColumn(t *testing.T, m Model) []string {
	t.Helper()
	lines := strings.Split(m.View(), "\n")
	col := make([]string, 0, m.visibleRows())
	for i := 1; i <= m.visibleRows() && i < len(lines); i++ {
		col = append(col, ansi.Truncate(lines[i], containerPaneWidth, ""))
	}
	return col
}

func TestContainerPaneStopsAtItsPinnedHeight(t *testing.T) {
	m, _ := previewModel(t, 60, 70)
	if m.visibleRows() <= 31 {
		t.Fatalf("this test needs a terminal taller than the pin; visibleRows = %d", m.visibleRows())
	}

	body := ansi.Strip(strings.Join(leftColumn(t, m), "\n"))

	rows := strings.Count(body, "Set ")
	if rows != 30 {
		t.Errorf("container pane drew %d set rows, want it pinned at 30:\n%s", rows, body)
	}
	if !strings.Contains(body, "Set 30") {
		t.Errorf("the pinned pane should fill all 30 of its rows:\n%s", body)
	}
	if strings.Contains(body, "Set 31") {
		t.Errorf("a 31st set row grew past the pin:\n%s", body)
	}
}

func TestSelectedCardArtFillsTheSpaceBelowTheContainerPane(t *testing.T) {
	m, _ := previewModel(t, 60, 70)
	col := leftColumn(t, m)

	first, rows := -1, 0
	for i, l := range col {
		if strings.Contains(l, artMark) {
			if first < 0 {
				first = i
			}
			rows++
		}
	}
	if first < 0 {
		t.Fatalf("no card art in the left column:\n%s", m.View())
	}
	if first <= 31 {
		t.Errorf("art starts at left-column row %d, want it below the 31-row pinned list", first)
	}
	if rows != len(m.preview.lines) {
		t.Errorf("art drew %d of the %d rows it rendered", rows, len(m.preview.lines))
	}
	for _, l := range col[first:] {
		if strings.Contains(ansi.Strip(l), "Set ") {
			t.Errorf("a set row sits inside the art block: %q", ansi.Strip(l))
		}
	}
}

func TestArtFollowsTheHighlightedCardRow(t *testing.T) {
	m, rec := previewModel(t, 5, 70)
	m.focus = paneCards
	if len(m.cards) < 2 {
		t.Fatalf("need at least two card rows, got %d", len(m.cards))
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = pump(t, next.(Model), cmd)

	want := m.cards[m.cursor[paneCards]].ScryfallID
	asked := rec.ids()
	if len(asked) == 0 || asked[len(asked)-1] != want {
		t.Fatalf("fetched %v, want the highlighted row %q last", asked, want)
	}

	before := m.View()
	stale, _ := m.Update(imageMsg{scryfallID: "some-other-card", preview: true,
		lines: []string{"STALEART"}, cols: containerPaneWidth})
	if after := stale.(Model).View(); after != before || strings.Contains(after, "STALEART") {
		t.Error("art for a card that is no longer highlighted was drawn anyway")
	}
}

func TestScrollingThreeRowsAsksForOneImage(t *testing.T) {
	m, rec := previewModel(t, 5, 70)
	m.focus = paneCards
	m.cursor[paneCards] = 0

	settled := len(rec.ids())
	var cmds []tea.Cmd
	for range 3 {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m, cmds = next.(Model), append(cmds, cmd)
	}
	for _, c := range cmds {
		m = pump(t, m, c)
	}

	if n := len(rec.ids()) - settled; n != 1 {
		t.Errorf("scrolling three rows made %d image requests, want 1 for where it landed", n)
	}
}

func TestContainerCursorScrollsInsideThePinnedPane(t *testing.T) {
	m, _ := previewModel(t, 60, 70)
	m.focus = paneContainers
	m.cursor[paneContainers] = 59
	m.scrollIntoView()

	body := ansi.Strip(strings.Join(leftColumn(t, m), "\n"))
	if !strings.Contains(body, "Set 60") {
		t.Errorf("the pinned pane did not scroll down to the cursor:\n%s", body)
	}
	if strings.Contains(body, "Set 01") {
		t.Errorf("the pinned pane never scrolled its first row away:\n%s", body)
	}
}

func TestShortTerminalsDropTheArtRatherThanClipIt(t *testing.T) {
	for _, height := range []int{20, 26, 30} {
		m, _ := previewModel(t, 60, height)
		col := leftColumn(t, m)
		body := strings.Join(col, "\n")

		drawn := 0
		for _, l := range col {
			if strings.Contains(l, artMark) {
				drawn++
			}
		}
		if drawn > 0 && drawn < m.artRows(containerPaneWidth) {
			t.Errorf("height %d: drew %d of %d art rows — a half card is worse than none",
				height, drawn, m.artRows(containerPaneWidth))
		}
		if drawn > 0 {
			continue
		}
		rows := strings.Count(ansi.Strip(body), "Set ")
		if want := len(col) - 1; rows != want {
			t.Errorf("height %d: art was dropped but the list only drew %d of %d rows",
				height, rows, want)
		}
	}
}

func TestTerminalsWithoutImagesKeepTheFullContainerHeight(t *testing.T) {
	m := newTestModel(t, testStore())
	m.imgTier = ui.ImageNone
	m.imageFetch = nil
	m.containers = manySets(60)
	m.cursor[paneContainers] = 0
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 70})
	m = next.(Model)

	body := ansi.Strip(strings.Join(leftColumn(t, m), "\n"))
	if !strings.Contains(body, "Set 40") {
		t.Errorf("with no art to make room for, the list should still use the whole column:\n%s", body)
	}
}

func TestReservedArtRowsCoverEveryRenderedRow(t *testing.T) {
	m := newTestModel(t, testStore())
	card := cardShapedArt()

	for _, tier := range []ui.ImageTier{ui.ImageHalfblock, ui.ImageKitty} {
		for _, aspect := range []float64{1.8, 2.0, 2.8} {
			m.imgTier, m.cellAspect = tier, aspect
			for _, cols := range []int{14, 24, 30, 42} {
				lines, _, ok := renderImage(card, tier, cols, m.artAspect(), previewImageID)
				if !ok {
					t.Fatalf("renderImage(tier %v, %d cols): not ok", tier, cols)
				}
				if got := m.artRows(cols); got < len(lines) {
					t.Errorf("tier %v aspect %.1f cols %d: reserved %d rows for %d rows of art",
						tier, aspect, cols, got, len(lines))
				}
			}
		}
	}
}

func TestContainerPaneTitleSaysHowFarItScrolls(t *testing.T) {
	m, _ := previewModel(t, 60, 70)

	head := ansi.Strip(strings.Split(m.View(), "\n")[0])
	if !strings.Contains(head, "1\u201330 of 60") {
		t.Errorf("a pinned list holding 30 of 60 sets should say so in its title, got %q", head)
	}

	m.cursor[paneContainers] = 59
	m.scrollIntoView()
	if head = ansi.Strip(strings.Split(m.View(), "\n")[0]); !strings.Contains(head, "31\u201360 of 60") {
		t.Errorf("scrolled to the end the title should read 31\u201360 of 60, got %q", head)
	}

	short, _ := previewModel(t, 4, 70)
	head = ansi.Strip(strings.Split(short.View(), "\n")[0])
	if strings.Contains(head, " of 4") {
		t.Errorf("a list that fits needs no range in its title, got %q", head)
	}
}

func TestRowBelowThePinnedListCountsWhatIsOutOfSight(t *testing.T) {
	m, _ := previewModel(t, 60, 70)
	gapRow := func(m Model) string {
		return ansi.Strip(leftColumn(t, m)[m.containerListRows()])
	}

	if got := gapRow(m); !strings.Contains(got, "30 more") {
		t.Errorf("with 30 sets hidden below, the row under the list should say so, got %q", got)
	}

	m.cursor[paneContainers] = 59
	m.scrollIntoView()
	got := gapRow(m)
	if !strings.Contains(got, "30 more") {
		t.Errorf("scrolled to the end it should count the 30 sets above, got %q", got)
	}
	if !strings.Contains(got, "\u2191") {
		t.Errorf("the hint should point up once the list has scrolled, got %q", got)
	}

	short, _ := previewModel(t, 4, 70)
	if got := gapRow(short); strings.TrimSpace(got) != "" {
		t.Errorf("a list that fits needs no hint, got %q", got)
	}
}
