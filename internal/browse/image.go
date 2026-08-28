package browse

import (
	"context"
	"image"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/ui"
)

type CardImageFunc func(ctx context.Context, scryfallID, url string) (image.Image, error)

func WithCardImage(f CardImageFunc) Option {
	return func(m *Model) { m.imageFetch = f }
}

const artColsMax = 42

const artMinTextCols = 96

const cardAspectNum, cardAspectDen = 680, 488

type imageMsg struct {
	scryfallID string
	lines      []string
	cols       int
	transmit   string
	preview    bool
}

type retransmitMsg struct{ gen int }

const transmitSettleDelay = 300 * time.Millisecond

func (m *Model) transmitSettle() tea.Cmd {
	m.detail.transmitSent = false
	m.resizeGen++
	gen := m.resizeGen
	return tea.Tick(transmitSettleDelay, func(time.Time) tea.Msg {
		return retransmitMsg{gen: gen}
	})
}

func (m *Model) fetchDetailImage() tea.Cmd {
	d := m.detail
	if m.imgTier == ui.ImageNone || m.imageFetch == nil || d == nil {
		return nil
	}
	face := d.shownCard()
	if face.ImageURI == "" {
		return nil
	}

	cols, aspect := m.detailImageCols(), m.artAspect()
	fetch, tier := m.imageFetch, m.imgTier
	ctx, id, url := m.ctx, d.imageIdentity(), face.ImageURI
	d.imagePending = true

	key := imageKey{id: id, cols: cols, tier: tier, imgID: detailImageID, aspect: aspect}
	return func() tea.Msg {
		if e, hit := renders.get(key); hit {
			return imageMsg{scryfallID: id, lines: e.lines, cols: cols, transmit: e.transmit}
		}
		img, err := fetch(ctx, id, url)
		if err != nil {

			return imageMsg{scryfallID: id}
		}
		lines, transmit, ok := renderImage(img, tier, cols, aspect, detailImageID)
		if !ok {
			return imageMsg{scryfallID: id}
		}
		renders.put(key, imageEntry{lines: lines, transmit: transmit})
		return imageMsg{scryfallID: id, lines: lines, cols: cols, transmit: transmit}
	}
}

func (m Model) artOverflows() bool {
	return m.width-m.detailImageCols()-2 >= artMinTextCols
}

func (m Model) detailImageCols() int {
	h := max(int(float64((m.visibleRows()-1)*cardAspectDen)*m.artAspect()/float64(cardAspectNum)), 8)
	ideal := min(artColsMax, h)
	if m.width-ideal-2 >= artMinTextCols {
		return ideal
	}
	return min(ideal, max(m.width-2, 8))
}

const kittyCellAspect = 1.9

func (m Model) artAspect() float64 {
	if m.imgTier != ui.ImageKitty {
		return 2
	}
	if m.cellAspect > 0 {
		return m.cellAspect
	}
	return kittyCellAspect
}

func (m Model) artRows(cols int) int {
	return ui.CellRows(cols, cardAspectDen, cardAspectNum, m.artAspect())
}

func (m Model) blankArt(cols int) []string {
	return make([]string, m.artRows(cols))
}

func renderImage(img image.Image, tier ui.ImageTier, cols int, cellAspect float64, id int) ([]string, string, bool) {
	switch tier {
	case ui.ImageHalfblock:
		lines := ui.Halfblocks(img, cols)
		return lines, "", len(lines) > 0
	case ui.ImageKitty:
		transmit, placeholder, err := ui.KittyImage(img, id, cols, cellAspect)
		if err != nil {
			return nil, "", false
		}
		return placeholder, transmit, true
	}
	return nil, "", false
}

const (
	detailImageID  = 91
	previewImageID = 92
)

func (m Model) onImage(msg imageMsg) (tea.Model, tea.Cmd) {
	if msg.preview {
		return m.onPreviewImage(msg)
	}
	if m.detail != nil && m.detail.imageIdentity() == msg.scryfallID {
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

func (m Model) onRetransmit(msg retransmitMsg) (tea.Model, tea.Cmd) {
	if msg.gen == m.resizeGen && m.detail != nil && !m.detail.imagePending {
		m.detail.transmitSent = true
	}
	return m, nil
}
