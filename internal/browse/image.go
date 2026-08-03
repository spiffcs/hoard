package browse

// The detail overlay's card image. Fetching is injected like every other
// network dependency; rendering happens here, per the terminal tier
// ui.DetectImageTier picked before the program started. The overlay
// renders its text immediately and the image slot fills when the fetch
// lands — a slow network must never make pressing enter feel broken.

import (
	"context"
	"image"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/ui"
)

// CardImageFunc fetches one card's image — from a cache or the network;
// the browser does not care which. It must honour ctx.
type CardImageFunc func(ctx context.Context, scryfallID, url string) (image.Image, error)

// WithCardImage supplies the image fetch. Without it (or without a capable
// terminal) the detail overlay is text-only, exactly as before.
func WithCardImage(f CardImageFunc) Option {
	return func(m *Model) { m.imageFetch = f }
}

// imageCols is the card image's width in cells at its widest; the fetch
// shrinks it when the window is short, keeping the card's aspect.
const imageCols = 26

// cardAspect approximates Scryfall's normal card scans (488×680) for
// sizing decisions made before the bytes arrive.
const cardAspectNum, cardAspectDen = 680, 488

// imageMsg carries a rendered image block back to the update loop. The id
// names the card it belongs to, so a slow fetch cannot decorate a detail
// the user has since navigated away from.
type imageMsg struct {
	scryfallID string
	lines      []string
}

// fetchDetailImage starts the image fetch for the open detail, or returns
// nil when anything about the situation says text-only: no capable
// terminal, no injected fetcher, no stored image URL.
func (m *Model) fetchDetailImage() tea.Cmd {
	d := m.detail
	if m.imgTier == ui.ImageNone || m.imageFetch == nil ||
		d == nil || d.card.ImageURI == nil {
		return nil
	}
	// Fit the card into the rows the overlay actually has: rows ≈
	// cols·aspect/2, inverted here, and never wider than imageCols.
	cols := m.detailImageCols()
	fetch, tier := m.imageFetch, m.imgTier
	ctx, id, url := m.ctx, d.card.ScryfallID, *d.card.ImageURI
	d.imagePending = true

	return func() tea.Msg {
		img, err := fetch(ctx, id, url)
		if err != nil {
			// A nil-lined message rather than silence: the layout reserved
			// the art's space, and only an answer releases it. The status
			// line stays quiet either way — a missing thumbnail must not
			// outrank real answers.
			return imageMsg{scryfallID: id}
		}
		lines, ok := renderImage(img, tier, cols)
		if !ok {
			return imageMsg{scryfallID: id}
		}
		return imageMsg{scryfallID: id, lines: lines}
	}
}

// detailImageCols is the width the detail's art renders at — shared with
// the placeholder that reserves its space while the fetch runs.
func (m Model) detailImageCols() int {
	return min(imageCols, max(2*(m.visibleRows()-1)*cardAspectDen/cardAspectNum, 8))
}

// blankImage reserves an image's footprint before the pixels arrive: the
// height a card of the standard aspect renders at, in empty lines.
func blankImage(cols int) []string {
	rows := max(cols*cardAspectNum/(cardAspectDen*2), 1)
	return make([]string, rows)
}

// renderImage turns the bitmap into the tier's cell block.
func renderImage(img image.Image, tier ui.ImageTier, cols int) ([]string, bool) {
	switch tier {
	case ui.ImageHalfblock:
		lines := ui.Halfblocks(img, cols)
		return lines, len(lines) > 0
	case ui.ImageKitty:
		transmit, placeholder, err := ui.KittyImage(img, kittyImageID, cols)
		if err != nil {
			return nil, false
		}
		// The transmission goes straight to the terminal from this
		// goroutine: bubbletea v1 has no raw-write API, and the sequence
		// is not cursor-anchored (virtual placement), so the worst
		// interleaving costs one torn frame that the next repaint fixes.
		// The placeholder cells that actually place it render through
		// View like any other text.
		os.Stdout.WriteString(transmit)
		return placeholder, true
	}
	return nil, false
}

// kittyImageID is the one image slot hoard uses: each detail's transmit
// replaces the previous image, so closing a detail needs no cleanup and
// the terminal holds at most one card.
const kittyImageID = 91

// onImage attaches a finished image block to the detail that asked for it.
// A nil-lined answer (fetch failed, nothing renderable) releases the
// reserved space; the layout falls back to text-only.
func (m Model) onImage(msg imageMsg) (tea.Model, tea.Cmd) {
	if m.detail != nil && m.detail.card.ScryfallID == msg.scryfallID {
		m.detail.image = msg.lines
		m.detail.imagePending = false
	}
	return m, nil
}
