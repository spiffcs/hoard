package browse

import (
	"context"
	"image"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

const (
	frontType   = "Legendary Enchantment"
	frontOracle = "Look at the top four cards of your library."
	frontArtist = "Front Artist"
	frontArt    = "http://img.test/Bitterblossom-id"

	backName   = "Itlimoc, Cradle of the Sun"
	backType   = "Legendary Land"
	backOracle = "{T}: Add {G} for each creature you control."
	backArtist = "Back Artist"
	backArt    = "http://img.test/Bitterblossom-id/back"
)

func itlimocBack() *store.CardFace {
	return &store.CardFace{
		Name:       backName,
		TypeLine:   backType,
		OracleText: backOracle,
		Artist:     backArtist,
		ImageURI:   backArt,
	}
}

type faceRecorder struct {
	mu   sync.Mutex
	asks [][2]string
}

func (r *faceRecorder) fetch(_ context.Context, id, url string) (image.Image, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asks = append(r.asks, [2]string{id, url})
	return cardShapedArt(), nil
}

func (r *faceRecorder) calls() [][2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]string(nil), r.asks...)
}

func (r *faceRecorder) idFor(url string) (string, bool) {
	for _, a := range r.calls() {
		if a[1] == url {
			return a[0], true
		}
	}
	return "", false
}

func flipStore(backs map[string]*store.CardFace, holdings ...store.Holding) *fakeStore {
	st := testStore()
	st.holdingsByName = map[string][]store.Holding{"Bitterblossom": holdings}
	st.detailPatch = func(d *store.CardDetail) {
		d.TypeLine, d.OracleText, d.Artist = frontType, frontOracle, frontArtist
		d.Back = backs[d.ScryfallID]
	}
	return st
}

func heldAt(id string) store.Holding {
	return store.Holding{
		ContainerID: 1, ContainerName: "Binder", ContainerKind: store.KindCollection,
		Finish: finish.Nonfoil, Quantity: 4,
		ScryfallID: id, SetCode: "uma", CollectorNumber: "85",
	}
}

func sleevedIn(id string) store.Holding {
	return store.Holding{
		ContainerID: 202, ContainerName: "Rich Deck", ContainerKind: store.KindDeck,
		Finish: finish.Nonfoil, Quantity: 1, Board: "main",
		ScryfallID: id, SetCode: "dom", CollectorNumber: "12",
	}
}

func openFlipDetail(t *testing.T, st *fakeStore) (Model, *faceRecorder) {
	t.Helper()
	prev := renders
	renders = newRenderCache(renderCacheSize)
	t.Cleanup(func() { renders = prev })

	rec := &faceRecorder{}
	m := newTestModel(t, st)
	m.imgTier = ui.ImageHalfblock
	m.imageFetch = rec.fetch
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = pump(t, next.(Model), cmd)
	m = key(m, "tab")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = pump(t, next.(Model), cmd)
	if m.detail == nil {
		t.Fatal("the detail screen did not open")
	}
	return m, rec
}

func press(t *testing.T, m Model, k string) Model {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	return pump(t, next.(Model), cmd)
}

func screen(m Model) string { return ansi.Strip(m.View()) }

func TestFlipKeyShowsTheBackFace(t *testing.T) {
	st := flipStore(map[string]*store.CardFace{"Bitterblossom-id": itlimocBack()},
		heldAt("Bitterblossom-id"))
	m, rec := openFlipDetail(t, st)

	if front := screen(m); !strings.Contains(front, frontOracle) || strings.Contains(front, backOracle) {
		t.Fatalf("the detail screen must open on the front face:\n%s", front)
	}

	m = press(t, m, "f")
	flipped := screen(m)

	for _, want := range []string{backName, backType, backOracle, backArtist} {
		if !strings.Contains(flipped, want) {
			t.Errorf("after f, want %q on screen:\n%s", want, flipped)
		}
	}
	for _, gone := range []string{frontOracle, frontType, frontArtist} {
		if strings.Contains(flipped, gone) {
			t.Errorf("after f, the front face's %q is still on screen:\n%s", gone, flipped)
		}
	}
	if _, ok := rec.idFor(backArt); !ok {
		t.Errorf("the back face's art was never fetched; asked for %v", rec.calls())
	}

	back := screen(press(t, m, "f"))
	if !strings.Contains(back, frontOracle) || strings.Contains(back, backOracle) {
		t.Errorf("a second f must turn the card back over:\n%s", back)
	}
}

func TestBackFaceArtGetsItsOwnCacheIdentity(t *testing.T) {
	st := flipStore(map[string]*store.CardFace{"Bitterblossom-id": itlimocBack()},
		heldAt("Bitterblossom-id"))
	m, rec := openFlipDetail(t, st)
	press(t, m, "f")

	frontID, okFront := rec.idFor(frontArt)
	backID, okBack := rec.idFor(backArt)
	if !okFront || !okBack {
		t.Fatalf("want both faces fetched, got %v", rec.calls())
	}
	if frontID == backID {
		t.Errorf("both faces were fetched under the id %q; the image cache is keyed by that id, "+
			"so the back face would be served the front face's picture", frontID)
	}
}

func TestFlipIsOfferedOnlyWhenThereIsABackFace(t *testing.T) {
	twoFaced, _ := openFlipDetail(t, flipStore(
		map[string]*store.CardFace{"Bitterblossom-id": itlimocBack()}, heldAt("Bitterblossom-id")))
	if help := twoFaced.helpLine(); !strings.Contains(help, "f flip") {
		t.Errorf("help = %q, want the flip key advertised on a two-faced card", help)
	}

	m, rec := openFlipDetail(t, flipStore(nil, heldAt("Bitterblossom-id")))
	if help := m.helpLine(); strings.Contains(help, "f flip") {
		t.Errorf("help = %q, want no flip key on a single-faced card", help)
	}
	before, asked := screen(m), len(rec.calls())

	m = press(t, m, "f")
	if after := screen(m); after != before {
		t.Errorf("f changed a single-faced card's detail screen:\n%s", after)
	}
	if now := rec.calls(); len(now) != asked {
		t.Errorf("f started an image fetch on a single-faced card: %v", now[asked:])
	}
}

func TestMovingToAnotherPrintingTurnsTheCardBackOver(t *testing.T) {
	st := flipStore(map[string]*store.CardFace{"Bitterblossom-id": itlimocBack()},
		heldAt("Bitterblossom-id"), sleevedIn("Bitterblossom-alt"))
	m, _ := openFlipDetail(t, st)

	m = press(t, m, "f")
	if !strings.Contains(screen(m), backOracle) {
		t.Fatalf("this test needs the card flipped first:\n%s", screen(m))
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = pump(t, next.(Model), cmd)
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = pump(t, next.(Model), cmd)
	if m.detail.card.ScryfallID != "Bitterblossom-alt" {
		t.Fatalf("held cursor did not move to the other printing, still on %q", m.detail.card.ScryfallID)
	}

	moved := screen(m)
	if strings.Contains(moved, backOracle) || strings.Contains(moved, backName) {
		t.Errorf("a printing with no back face is still showing the previous card's back:\n%s", moved)
	}
	if !strings.Contains(moved, frontOracle) {
		t.Errorf("want the new printing's front face:\n%s", moved)
	}
	if help := m.helpLine(); strings.Contains(help, "f flip") {
		t.Errorf("help = %q, want no flip key once the cursor is on a single-faced printing", help)
	}
}
