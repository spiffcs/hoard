package browse

// The detail overlay's card image. Fetching is injected like every other
// network dependency; rendering happens here, per the terminal tier
// ui.DetectImageTier picked before the program started. The overlay
// renders its text immediately and the image slot fills when the fetch
// lands — a slow network must never make pressing enter feel broken.

import (
	"context"
	"image"
	"time"

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

// artColsMax is the art at its widest, in the overflow layout that runs
// it down the right edge beside HELD and PRICE. The tuning knob: ~0.7
// rows of height per column of width (a 40-col card is ~27 rows).
const artColsMax = 40

// artMinTextCols is the text column's floor in the overflow layout: the
// widest un-truncated hoard row (the PRICE spread caption, ~95 cells at
// its worst) — narrower and the analysis would clip against the art.
// Lowering it grows the art sooner at the cost of clipped captions.
const artMinTextCols = 96

// cardAspect approximates Scryfall's normal card scans (488×680) for
// sizing decisions made before the bytes arrive.
const cardAspectNum, cardAspectDen = 680, 488

// imageMsg carries a rendered image block back to the update loop. The id
// names the card it belongs to, so a slow fetch cannot decorate a detail
// the user has since navigated away from; cols is the width it was
// rendered at, so a resize mid-flight can be detected and re-rendered;
// transmit is the kitty upload that placed it, kept so it can be re-sent
// if the terminal loses it (see retransmitMsg).
type imageMsg struct {
	scryfallID string
	lines      []string
	cols       int
	transmit   string
}

// retransmitMsg declares a kitty upload delivered: while dirty, every
// frame embeds the transmit (dropped intermediate frames make any
// single-frame delivery unreliable), and this tick — scheduled by each
// landing and each resize — ends the embedding once things have been
// quiet long enough for a frame to actually flush. The gen field ignores
// all but the newest scheduled tick, so a drag keeps the upload dirty
// until it ends.
type retransmitMsg struct{ gen int }

// transmitSettleDelay is how long after the last landing or resize the
// upload stays dirty — comfortably past the renderer's flush cadence.
const transmitSettleDelay = 300 * time.Millisecond

// transmitSettle marks the art's upload dirty and schedules the tick
// that will declare it delivered.
func (m *Model) transmitSettle() tea.Cmd {
	m.detail.transmitSent = false
	m.resizeGen++
	gen := m.resizeGen
	return tea.Tick(transmitSettleDelay, func(time.Time) tea.Msg {
		return retransmitMsg{gen: gen}
	})
}

// fetchDetailImage starts the image fetch for the open detail, or returns
// nil when anything about the situation says text-only: no capable
// terminal, no injected fetcher, no stored image URL.
func (m *Model) fetchDetailImage() tea.Cmd {
	d := m.detail
	if m.imgTier == ui.ImageNone || m.imageFetch == nil ||
		d == nil || d.card.ImageURI == "" {
		return nil
	}
	// Fit the card into the rows and columns the overlay actually has:
	// rows ≈ cols·aspect/2, inverted inside detailImageCols.
	cols, aspect := m.detailImageCols(), m.artAspect()
	fetch, tier := m.imageFetch, m.imgTier
	ctx, id, url := m.ctx, d.card.ScryfallID, d.card.ImageURI
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
		lines, transmit, ok := renderImage(img, tier, cols, aspect)
		if !ok {
			return imageMsg{scryfallID: id}
		}
		return imageMsg{scryfallID: id, lines: lines, cols: cols, transmit: transmit}
	}
}

// artOverflows reports whether the overflow layout can host the wanted
// art: the analysis keeps its artMinTextCols column beside it.
func (m Model) artOverflows() bool {
	return m.width-m.detailImageCols()-2 >= artMinTextCols
}

// detailImageCols is the width the detail's art renders at — shared with
// the placeholder that reserves its space while the fetch runs. The size
// comes first, the placement second (owner's call): the art holds its
// ideal width — artColsMax capped by the window's height — and when the
// overflow layout can't host that beside the analysis, the layout goes
// vertical (art between the card details and HELD) instead of shrinking
// the card; only a window narrower than the art itself costs it columns.
func (m Model) detailImageCols() int {
	h := max(int(float64((m.visibleRows()-1)*cardAspectDen)*m.artAspect()/float64(cardAspectNum)), 8)
	ideal := min(artColsMax, h)
	if m.width-ideal-2 >= artMinTextCols {
		return ideal
	}
	return min(ideal, max(m.width-2, 8))
}

// kittyCellAspect is the kitty tier's cell ratio: 2.8 is the owner's
// tuned value for the card sitting flush above HELD (Ghostty, observed
// live). Not measured from the kernel — its reported pixel grid gave a
// ratio that still letterboxed a multi-row gap under the card.
// HOARD_CELL_ASPECT overrides it (see ui.CellAspectOverride).
const kittyCellAspect = 2.8

// artAspect is the cell height-to-width ratio the art's rows are computed
// against: the measured terminal geometry on the kitty tier — falling
// back to kittyCellAspect unmeasured — and exactly 2 on the halfblock
// tier, where every cell is two pixels by construction.
func (m Model) artAspect() float64 {
	if m.imgTier != ui.ImageKitty {
		return 2
	}
	if m.cellAspect > 0 {
		return m.cellAspect
	}
	return kittyCellAspect
}

// artRows is how many rows a card of the standard aspect occupies at the
// given width — shared by the reservation and the height budget so they
// cannot disagree with the renderer.
func (m Model) artRows(cols int) int {
	return max(int(float64(cols*cardAspectNum)/(float64(cardAspectDen)*m.artAspect())), 1)
}

// blankArt reserves an image's footprint before the pixels arrive, in
// empty lines.
func (m Model) blankArt(cols int) []string {
	return make([]string, m.artRows(cols))
}

// renderImage turns the bitmap into the tier's cell block. For kitty the
// transmit comes back for detailView to embed into the next frame —
// writing it raw from this goroutine raced bubbletea's renderer, and a
// transmit interleaved with frame bytes is a corrupted escape sequence
// the terminal discards, leaving the placeholders addressing an image it
// never received (a zoomed crop, observed live on every resize).
// Halfblock is pure text and needs none.
func renderImage(img image.Image, tier ui.ImageTier, cols int, cellAspect float64) ([]string, string, bool) {
	switch tier {
	case ui.ImageHalfblock:
		lines := ui.Halfblocks(img, cols)
		return lines, "", len(lines) > 0
	case ui.ImageKitty:
		transmit, placeholder, err := ui.KittyImage(img, kittyImageID, cols, cellAspect)
		if err != nil {
			return nil, "", false
		}
		return placeholder, transmit, true
	}
	return nil, "", false
}

// kittyImageID is the one image slot hoard uses: each detail's transmit
// replaces the previous image, so closing a detail needs no cleanup and
// the terminal holds at most one card.
const kittyImageID = 91

// onImage attaches a finished image block to the detail that asked for it.
// A nil-lined answer (fetch failed, nothing renderable) releases the
// reserved space; the layout falls back to text-only. An answer rendered
// for a size the window no longer has re-renders at the current one — the
// bytes are disk-cached, so the correction is near-instant.
func (m Model) onImage(msg imageMsg) (tea.Model, tea.Cmd) {
	if m.detail != nil && m.detail.card.ScryfallID == msg.scryfallID {
		m.detail.image = msg.lines
		m.detail.imagePending = false
		m.detail.imageColsDrawn = msg.cols
		m.detail.imageTransmit = msg.transmit
		var cmds []tea.Cmd
		if msg.transmit != "" {
			cmds = append(cmds, m.transmitSettle())
		}
		if len(msg.lines) > 0 && msg.cols != m.detailImageCols() {
			cmds = append(cmds, m.fetchDetailImage())
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

// onRetransmit declares the current upload delivered once things settle —
// frames stop carrying the transmit. Stale generations are ticks a later
// landing or resize superseded; a pending fetch keeps the upload dirty
// (its landing schedules its own settle).
func (m Model) onRetransmit(msg retransmitMsg) (tea.Model, tea.Cmd) {
	if msg.gen == m.resizeGen && m.detail != nil && !m.detail.imagePending {
		m.detail.transmitSent = true
	}
	return m, nil
}
