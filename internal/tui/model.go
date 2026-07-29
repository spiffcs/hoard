package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/cphillips918/mtg_index/internal/scryfall"
)

type state int

const (
	stateName state = iota
	stateLoading
	stateNamePick
	statePrintPick
	stateFinishPick
	stateQty
	stateConfirm
	stateError
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	helpStyle   = lipgloss.NewStyle().Faint(true)
	errStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	promptStyle = lipgloss.NewStyle().Bold(true)
)

// --- messages ---

type printsMsg struct {
	name  string
	cards []scryfall.Card
}
type namesMsg struct{ names []string }
type errMsg struct{ err error }

// --- list item types ---

type nameItem string

func (n nameItem) Title() string       { return string(n) }
func (n nameItem) Description() string  { return "" }
func (n nameItem) FilterValue() string { return string(n) }

type printItem struct{ card scryfall.Card }

func (p printItem) Title() string {
	return fmt.Sprintf("%s #%s · %s",
		strings.ToUpper(p.card.Set), p.card.CollectorNumber, p.card.SetName)
}
func (p printItem) Description() string {
	parts := []string{}
	if p.card.ReleasedAt != "" {
		parts = append(parts, p.card.ReleasedAt)
	}
	if mk := printMarkers(p.card); mk != "" {
		parts = append(parts, mk)
	}
	parts = append(parts, priceLabel(p.card))
	return strings.Join(parts, " · ")
}
func (p printItem) FilterValue() string { return p.Title() + " " + p.Description() }

type finishItem string

func (f finishItem) Title() string       { return string(f) }
func (f finishItem) Description() string  { return "" }
func (f finishItem) FilterValue() string { return string(f) }

// --- model ---

type model struct {
	ctx      context.Context
	searcher Searcher

	state state

	nameInput textinput.Model
	qtyInput  textinput.Model
	list      list.Model
	spinner   spinner.Model

	width, height int

	// cascade selections
	prints []scryfall.Card
	chosen *scryfall.Card
	finish string

	qtyErr string
	result *Result
	err    error
}

func newModel(ctx context.Context, s Searcher, initialName string) model {
	ni := textinput.New()
	ni.Placeholder = "Card name, e.g. Ulamog, the Infinite Gyre"
	ni.Focus()
	ni.CharLimit = 200
	ni.Width = 50

	qi := textinput.New()
	qi.Placeholder = "1"
	qi.CharLimit = 6
	qi.Width = 10

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.SetShowStatusBar(false)
	l.SetShowTitle(false)

	m := model{
		ctx:       ctx,
		searcher:  s,
		nameInput: ni,
		qtyInput:  qi,
		spinner:   sp,
		list:      l,
		width:     80,
		height:    22,
	}
	if strings.TrimSpace(initialName) != "" {
		m.nameInput.SetValue(strings.TrimSpace(initialName))
		m.state = stateLoading
	} else {
		m.state = stateName
	}
	return m
}

func (m model) Init() tea.Cmd {
	if m.state == stateLoading {
		return tea.Batch(m.spinner.Tick, m.searchPrintsCmd(m.nameInput.Value()))
	}
	return textinput.Blink
}

// --- commands ---

func (m model) searchPrintsCmd(name string) tea.Cmd {
	return func() tea.Msg {
		cards, err := m.searcher.SearchPrints(m.ctx, name)
		if err != nil {
			return errMsg{err}
		}
		return printsMsg{name: name, cards: cards}
	}
}

func (m model) autocompleteCmd(q string) tea.Cmd {
	return func() tea.Msg {
		names, err := m.searcher.Autocomplete(m.ctx, q)
		if err != nil {
			return errMsg{err}
		}
		return namesMsg{names: names}
	}
}

// --- update ---

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width, maxInt(msg.Height-4, 4))
		return m, nil

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			m.result = nil
			return m, tea.Quit
		}
		return m.handleKey(msg)

	case printsMsg:
		return m.onPrints(msg)

	case namesMsg:
		return m.onNames(msg)

	case errMsg:
		m.err = msg.err
		m.state = stateError
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	// Delegate to the active component for non-key messages.
	return m.updateActive(msg)
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateName:
		if msg.Type == tea.KeyEnter {
			name := strings.TrimSpace(m.nameInput.Value())
			if name == "" {
				return m, nil
			}
			m.state = stateLoading
			return m, tea.Batch(m.spinner.Tick, m.searchPrintsCmd(name))
		}
	case stateNamePick:
		if msg.Type == tea.KeyEsc {
			return m, tea.Quit
		}
		if msg.Type == tea.KeyEnter && !m.list.SettingFilter() {
			if it, ok := m.list.SelectedItem().(nameItem); ok {
				m.state = stateLoading
				return m, tea.Batch(m.spinner.Tick, m.searchPrintsCmd(string(it)))
			}
		}
	case statePrintPick:
		if msg.Type == tea.KeyEsc {
			return m, tea.Quit
		}
		if msg.Type == tea.KeyEnter && !m.list.SettingFilter() {
			if it, ok := m.list.SelectedItem().(printItem); ok {
				card := it.card
				m.chosen = &card
				return m.advanceAfterPrint()
			}
		}
	case stateFinishPick:
		if msg.Type == tea.KeyEsc {
			return m, tea.Quit
		}
		if msg.Type == tea.KeyEnter && !m.list.SettingFilter() {
			if it, ok := m.list.SelectedItem().(finishItem); ok {
				m.finish = string(it)
				return m.toQty()
			}
		}
	case stateQty:
		if msg.Type == tea.KeyEnter {
			return m.submitQty()
		}
	case stateConfirm:
		switch msg.Type {
		case tea.KeyEnter:
			m.result = &Result{Card: *m.chosen, Finish: m.finish, Qty: m.qtyValue()}
			return m, tea.Quit
		case tea.KeyEsc:
			m.result = nil
			return m, tea.Quit
		}
	case stateError:
		return m, tea.Quit
	}
	return m.updateActive(msg)
}

// updateActive forwards a message to whichever interactive component is active.
func (m model) updateActive(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.state {
	case stateName:
		m.nameInput, cmd = m.nameInput.Update(msg)
	case stateQty:
		m.qtyInput, cmd = m.qtyInput.Update(msg)
	case stateNamePick, statePrintPick, stateFinishPick:
		m.list, cmd = m.list.Update(msg)
	case stateLoading:
		m.spinner, cmd = m.spinner.Update(msg)
	}
	return m, cmd
}

// --- transitions ---

func (m model) onPrints(msg printsMsg) (tea.Model, tea.Cmd) {
	if len(msg.cards) == 0 {
		// The name wasn't an exact match; offer autocomplete suggestions.
		return m, m.autocompleteCmd(msg.name)
	}
	m.prints = msg.cards
	if len(msg.cards) == 1 {
		card := msg.cards[0]
		m.chosen = &card
		return m.advanceAfterPrint()
	}
	items := make([]list.Item, len(msg.cards))
	for i, c := range msg.cards {
		items[i] = printItem{card: c}
	}
	m.setListItems("Select a printing", items)
	m.state = statePrintPick
	return m, nil
}

func (m model) onNames(msg namesMsg) (tea.Model, tea.Cmd) {
	switch len(msg.names) {
	case 0:
		m.err = fmt.Errorf("no cards found matching %q", m.nameInput.Value())
		m.state = stateError
		return m, nil
	case 1:
		m.state = stateLoading
		return m, tea.Batch(m.spinner.Tick, m.searchPrintsCmd(msg.names[0]))
	default:
		items := make([]list.Item, len(msg.names))
		for i, n := range msg.names {
			items[i] = nameItem(n)
		}
		m.setListItems("Select a card", items)
		m.state = stateNamePick
		return m, nil
	}
}

// advanceAfterPrint moves past printing selection: it auto-skips the finish step
// when the printing has a single finish, otherwise shows the finish picker.
func (m model) advanceAfterPrint() (tea.Model, tea.Cmd) {
	finishes := finishOptions(*m.chosen)
	if len(finishes) <= 1 {
		if len(finishes) == 1 {
			m.finish = finishes[0]
		} else {
			m.finish = "normal"
		}
		return m.toQty()
	}
	items := make([]list.Item, len(finishes))
	for i, f := range finishes {
		items[i] = finishItem(f)
	}
	m.setListItems("Select a finish", items)
	m.state = stateFinishPick
	return m, nil
}

func (m model) toQty() (tea.Model, tea.Cmd) {
	m.qtyInput.SetValue("1")
	m.qtyInput.Focus()
	m.state = stateQty
	return m, textinput.Blink
}

func (m model) submitQty() (tea.Model, tea.Cmd) {
	v := strings.TrimSpace(m.qtyInput.Value())
	if v == "" {
		v = "1"
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		m.qtyErr = "enter a whole number ≥ 1"
		return m, nil
	}
	m.qtyErr = ""
	m.qtyInput.SetValue(strconv.Itoa(n))
	m.state = stateConfirm
	return m, nil
}

func (m model) qtyValue() int {
	n, err := strconv.Atoi(strings.TrimSpace(m.qtyInput.Value()))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

func (m *model) setListItems(title string, items []list.Item) {
	m.list.Title = title
	m.list.SetItems(items)
	m.list.ResetFilter()
	m.list.Select(0)
	m.list.SetSize(m.width, maxInt(m.height-4, 4))
}

// --- view ---

func (m model) View() string {
	switch m.state {
	case stateName:
		return titleStyle.Render("Add a card by name") + "\n\n" +
			m.nameInput.View() + "\n\n" +
			helpStyle.Render("enter to search · ctrl+c to cancel")
	case stateLoading:
		return fmt.Sprintf("%s searching Scryfall…\n\n%s",
			m.spinner.View(), helpStyle.Render("ctrl+c to cancel"))
	case stateNamePick, statePrintPick, stateFinishPick:
		return m.list.View() + "\n" +
			helpStyle.Render("↑/↓ move · / filter · enter select · esc back · ctrl+c cancel")
	case stateQty:
		out := promptStyle.Render("Quantity for "+m.chosen.Name) + "\n\n" + m.qtyInput.View()
		if m.qtyErr != "" {
			out += "\n" + errStyle.Render(m.qtyErr)
		}
		return out + "\n\n" + helpStyle.Render("enter to continue · ctrl+c to cancel")
	case stateConfirm:
		return titleStyle.Render("Confirm") + "\n\n" + m.confirmSummary() + "\n\n" +
			helpStyle.Render("enter to add · esc to cancel")
	case stateError:
		return errStyle.Render("Error: "+m.err.Error()) + "\n\n" +
			helpStyle.Render("press any key to exit")
	}
	return ""
}

func (m model) confirmSummary() string {
	c := m.chosen
	finish := m.finish
	price := priceForFinish(*c, finish)
	return fmt.Sprintf("%d× %s\n%s #%s · %s\nfinish: %s   price: %s",
		m.qtyValue(), c.Name, strings.ToUpper(c.Set), c.CollectorNumber, c.SetName,
		finish, price)
}

// --- pure helpers (unit-tested) ---

// finishOptions maps a card's Scryfall finishes to the tool's finish names,
// preserving a stable normal→foil→etched order.
func finishOptions(c scryfall.Card) []string {
	has := map[string]bool{}
	for _, f := range c.Finishes {
		switch f {
		case "nonfoil":
			has["normal"] = true
		case "foil":
			has["foil"] = true
		case "etched":
			has["etched"] = true
		}
	}
	var out []string
	for _, f := range []string{"normal", "foil", "etched"} {
		if has[f] {
			out = append(out, f)
		}
	}
	return out
}

// printMarkers summarizes distinguishing traits of a printing for display.
func printMarkers(c scryfall.Card) string {
	var parts []string
	if c.BorderColor == "borderless" {
		parts = append(parts, "borderless")
	}
	parts = append(parts, c.FrameEffects...)
	parts = append(parts, c.PromoTypes...)
	return strings.Join(parts, "/")
}

func priceLabel(c scryfall.Card) string {
	return "$" + priceStr(c.PriceUSD) + " / $" + priceStr(c.PriceUSDFoil) + "f"
}

func priceForFinish(c scryfall.Card, finish string) string {
	p := c.PriceUSD
	if finish == "foil" || finish == "etched" {
		p = c.PriceUSDFoil
	}
	if p == nil {
		return "—"
	}
	return "$" + priceStr(p)
}

func priceStr(p *float64) string {
	if p == nil {
		return "—"
	}
	return strconv.FormatFloat(*p, 'f', 2, 64)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
