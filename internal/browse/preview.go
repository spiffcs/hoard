package browse

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/ui"
)

const containerPaneRows = 31

const previewGap = 1

const minContainerRows = 8

const minPreviewCols = 8

const previewSettleDelay = 120 * time.Millisecond

type previewArt struct {
	scryfallID string
	cols       int
	lines      []string
	transmit   string
	sent       bool
	pending    bool
}

type previewDueMsg struct{ gen int }

type previewSentMsg struct{ gen int }

func (m Model) previewCols() int {
	left, _ := m.paneWidths()
	return left
}

func (m Model) previewRows() int {
	if m.imgTier == ui.ImageNone || m.imageFetch == nil {
		return 0
	}
	cols := m.previewCols()
	if cols < minPreviewCols {
		return 0
	}
	art := m.artRows(cols)
	if m.visibleRows()-art-previewGap < minContainerRows {
		return 0
	}
	return art
}

func (m Model) containerListRows() int {
	total := m.visibleRows()
	art := m.previewRows()
	if art <= 0 {
		return total
	}
	return max(min(containerPaneRows, total-art-previewGap), minContainerRows)
}

func (m Model) paneRows(p pane) int {
	if p == paneContainers {
		return m.containerListRows()
	}
	return m.visibleRows()
}

func (m Model) selectedCardID() string {
	switch m.view {
	case viewHoldings:
		if c := m.selectedCard(); c != nil {
			return c.ScryfallID
		}
	case viewWatches:
		if w := m.selectedWatch(); w != nil {
			return w.ScryfallID
		}
		if r := m.selectedUnpricedRow(); r != nil {
			return r.ScryfallID
		}
	case viewMarket:
		if c := m.selectedComp(); c != nil {
			return c.Card.ScryfallID
		}
		if r := m.selectedMarketRow(); r != nil {
			return r.Card.ScryfallID
		}
	case viewMovers:
		if i := m.cursor[paneCards]; i >= 0 && i < len(m.movers) {
			return m.movers[i].ScryfallID
		}
	case viewDip:
		if r := m.selectedTrendRow(); r != nil {
			return r.ScryfallID
		}
	}
	return ""
}

func (m Model) fromArbitrageRow() bool {
	if m.view != viewMarket || m.selectedComp() != nil {
		return false
	}
	r := m.selectedMarketRow()
	return r != nil && r.Kind == market.KindProfit
}

func (m *Model) syncPreview() tea.Cmd {
	if m.previewRows() <= 0 || m.detail != nil || m.text != nil || m.addChild != nil {
		m.preview = previewArt{}
		return nil
	}
	id, cols := m.selectedCardID(), m.previewCols()
	if id == m.preview.scryfallID && cols == m.preview.cols {
		return nil
	}
	m.preview = previewArt{scryfallID: id, cols: cols}
	if id == "" {
		return nil
	}
	m.previewGen++
	gen := m.previewGen
	return tea.Tick(previewSettleDelay, func(time.Time) tea.Msg {
		return previewDueMsg{gen: gen}
	})
}

func (m Model) onPreviewDue(msg previewDueMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.previewGen || m.preview.pending {
		return m, nil
	}
	return m, m.fetchPreviewImage()
}

func (m *Model) fetchPreviewImage() tea.Cmd {
	id := m.preview.scryfallID
	if id == "" || m.imageFetch == nil || m.imgTier == ui.ImageNone {
		return nil
	}
	d, err := m.store.CardDetail(id)
	if err != nil || d.ImageURI == "" {
		return nil
	}
	cols, aspect := m.preview.cols, m.artAspect()
	fetch, tier := m.imageFetch, m.imgTier
	ctx, url := m.ctx, d.ImageURI
	m.preview.pending = true

	key := imageKey{id: id, cols: cols, tier: tier, imgID: previewImageID, aspect: aspect}
	return func() tea.Msg {
		if e, hit := renders.get(key); hit {
			return imageMsg{scryfallID: id, lines: e.lines, cols: cols,
				transmit: e.transmit, preview: true}
		}
		img, ferr := fetch(ctx, id, url)
		if ferr != nil {
			return imageMsg{scryfallID: id, preview: true}
		}
		lines, transmit, ok := renderImage(img, tier, cols, aspect, previewImageID)
		if !ok {
			return imageMsg{scryfallID: id, preview: true}
		}
		renders.put(key, imageEntry{lines: lines, transmit: transmit})
		return imageMsg{scryfallID: id, lines: lines, cols: cols,
			transmit: transmit, preview: true}
	}
}

func (m Model) onPreviewImage(msg imageMsg) (tea.Model, tea.Cmd) {
	if m.preview.scryfallID != msg.scryfallID {
		return m, nil
	}
	m.preview.pending = false
	m.preview.lines = msg.lines
	m.preview.transmit = msg.transmit
	m.preview.sent = false
	if msg.transmit == "" {
		return m, nil
	}
	return m, m.previewSettle()
}

func (m *Model) previewSettle() tea.Cmd {
	m.previewGen++
	gen := m.previewGen
	return tea.Tick(transmitSettleDelay, func(time.Time) tea.Msg {
		return previewSentMsg{gen: gen}
	})
}

func (m Model) onPreviewSent(msg previewSentMsg) (tea.Model, tea.Cmd) {
	if msg.gen == m.previewGen && !m.preview.pending {
		m.preview.sent = true
	}
	return m, nil
}

func (m Model) containerRowBudget() int {
	return max(m.paneRows(paneContainers)-1, 0)
}

func (m Model) containerScrollNote() string {
	return pagePhrase(m.offset[paneContainers], m.containerRowBudget(),
		len(m.containers), 0, 0, 1)
}

func (m Model) containerScrollHint() string {
	budget, n := m.containerRowBudget(), len(m.containers)
	if budget <= 0 || n <= budget {
		return ""
	}
	off := min(max(m.offset[paneContainers], 0), max(n-budget, 0))
	var parts []string
	if off > 0 {
		parts = append(parts, fmt.Sprintf("\u2191 %d more", off))
	}
	if below := n - off - budget; below > 0 {
		parts = append(parts, fmt.Sprintf("\u2193 %d more", below))
	}
	return strings.Join(parts, " \u00b7 ")
}

func (m Model) leftColumn(width, rows int) []string {
	out := make([]string, rows)
	lines := m.containerLines(width)
	for i := range rows {
		out[i] = fit(lineAt(lines, i), width)
	}

	art := m.previewRows()
	if art <= 0 || width <= 0 {
		return out
	}
	gap := m.containerListRows()
	if hint := m.containerScrollHint(); hint != "" && gap < rows {
		out[gap] = fit(m.theme.Help.Render(hint), width)
	}
	start := gap + previewGap
	for i := start; i < rows && i-start < art; i++ {
		out[i] = fit(lineAt(m.preview.lines, i-start), width)
	}
	if m.preview.transmit != "" && !m.preview.sent &&
		len(m.preview.lines) > 0 && start < rows {
		out[start] = m.preview.transmit + out[start]
	}
	return out
}
