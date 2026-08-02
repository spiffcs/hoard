package ui

import "github.com/charmbracelet/lipgloss"

// This file is the one definition of every style hoard renders with. Two
// consumers share it: plain-CLI render paths take Env methods (Style funcs
// that collapse to plain text when Env.Color is off), and the bubbletea
// programs take a Theme of lipgloss.Style values. Both are built from the
// same color constants, so "error red" or "the white pip" is decided in
// exactly one place.
//
// Two palettes, deliberately distinct families:
//
//   - The identity (data) palette: the community-standard Mana-font pip
//     pastels, tied to the cards' WUBRG color identity. Data color only.
//   - The semantic (UI) palette: vivid ANSI-16, for what the UI itself has
//     to say — errors, warnings, gains, losses, focus. UI state must never
//     read as card identity, so this family never borrows the pastels.
//
// Every style here is SGR-only — no width, padding, margin or alignment —
// so styling never changes a string's display width. The table engine
// measures before it styles and relies on that; theme_test.go enforces it.

// Identity colors, dark-background values straight from the Mana project's
// .ms-cost pip backgrounds; light-background values darkened for contrast
// (starting points, tuned by eye against real terminals).
var (
	identityW     = lipgloss.AdaptiveColor{Light: "#9a8f4d", Dark: "#f0f2c0"} // parchment
	identityU     = lipgloss.AdaptiveColor{Light: "#2a6395", Dark: "#b5cde3"} // island
	identityB     = lipgloss.AdaptiveColor{Light: "#6b5f66", Dark: "#aca29a"} // swamp
	identityR     = lipgloss.AdaptiveColor{Light: "#b0492a", Dark: "#db8664"} // clay
	identityG     = lipgloss.AdaptiveColor{Light: "#3a7d4f", Dark: "#93b483"} // sage
	identityC     = lipgloss.AdaptiveColor{Light: "#7d786f", Dark: "#beb9b2"} // wastes
	identityMulti = lipgloss.AdaptiveColor{Light: "#a8860b", Dark: "#d4af37"} // frame gold
)

// identityColors maps a WUBRG identity letter (plus C for colorless) to its
// color. Unknown letters are simply absent — callers fall back to plain.
var identityColors = map[byte]lipgloss.AdaptiveColor{
	'W': identityW,
	'U': identityU,
	'B': identityB,
	'R': identityR,
	'G': identityG,
	'C': identityC,
}

// Semantic colors: vivid ANSI-16, so they degrade predictably everywhere
// and stay visually apart from the identity pastels.
var (
	semErr    = lipgloss.Color("9")
	semOK     = lipgloss.Color("10")
	semWarn   = lipgloss.Color("11")
	semAccent = lipgloss.Color("12")
)

// The lipgloss styles behind both consumers.
var (
	lgBold   = lipgloss.NewStyle().Bold(true)
	lgFaint  = lipgloss.NewStyle().Faint(true)
	lgErr    = lipgloss.NewStyle().Bold(true).Foreground(semErr)
	lgOK     = lipgloss.NewStyle().Foreground(semOK)
	lgWarn   = lipgloss.NewStyle().Foreground(semWarn)
	lgAccent = lipgloss.NewStyle().Bold(true).Foreground(semAccent)
	// Gain and loss reuse the ok/error hues without the error's bold: a
	// delta is data, not an alarm. The +/− sign stays the piped-safe channel.
	lgGain = lipgloss.NewStyle().Foreground(semOK)
	lgLoss = lipgloss.NewStyle().Foreground(semErr)
)

// styled adapts a lipgloss style to the Env idiom: plain when color is off,
// so piped output never sees an escape sequence.
func (e Env) styled(st lipgloss.Style) Style {
	if !e.Color {
		return plain
	}
	return func(s string) string { return st.Render(s) }
}

// OK styles a success confirmation ("✓ Added …").
func (e Env) OK() Style { return e.styled(lgOK) }

// Err styles a failure: the error prefix, a refused row.
func (e Env) Err() Style { return e.styled(lgErr) }

// Warn styles a partial-outcome warning: skipped rows, missing data.
func (e Env) Warn() Style { return e.styled(lgWarn) }

// Gain styles a positive delta. The sign stays in the text.
func (e Env) Gain() Style { return e.styled(lgGain) }

// Loss styles a negative delta. The sign stays in the text.
func (e Env) Loss() Style { return e.styled(lgLoss) }

// Delta picks Gain or Loss by v's sign; zero returns nil so the cell
// inherits its column's style. The +/− sign stays in the text — piped
// output carries the direction without the color.
func (e Env) Delta(v float64) Style {
	switch {
	case v > 0:
		return e.Gain()
	case v < 0:
		return e.Loss()
	}
	return nil
}

// Accent styles the focused element: pane titles, selection markers.
func (e Env) Accent() Style { return e.styled(lgAccent) }

// Pip styles one identity letter with its own color: W parchment, U island,
// and so on. Unknown letters render plain.
func (e Env) Pip(letter byte) Style {
	c, ok := identityColors[letter]
	if !ok {
		return plain
	}
	return e.styled(lipgloss.NewStyle().Foreground(c))
}

// Identity styles text by a card's whole color identity: one color tints
// with that color, two or more read frame-gold, colorless (an empty but
// known identity) reads wastes-grey. A nil identity — the card's document
// was never stored — stays plain rather than claiming colorlessness.
func (e Env) Identity(colors []string) Style {
	st, ok := identityStyle(colors)
	if !ok {
		return plain
	}
	return e.styled(st)
}

func identityStyle(colors []string) (lipgloss.Style, bool) {
	switch {
	case colors == nil:
		return lipgloss.Style{}, false
	case len(colors) == 0:
		return lipgloss.NewStyle().Foreground(identityC), true
	case len(colors) > 1:
		return lipgloss.NewStyle().Foreground(identityMulti), true
	}
	c, ok := identityColors[letterOf(colors[0])]
	if !ok {
		return lipgloss.Style{}, false
	}
	return lipgloss.NewStyle().Foreground(c), true
}

// letterOf reduces an identity element ("W", "u") to its map key.
func letterOf(s string) byte {
	if s == "" {
		return 0
	}
	b := s[0]
	if 'a' <= b && b <= 'z' {
		b -= 'a' - 'A'
	}
	return b
}

// Theme is the bubbletea-side view of the same definitions: concrete
// lipgloss styles for programs that render with them directly.
type Theme struct {
	Title    lipgloss.Style // pane and section titles
	Help     lipgloss.Style // help lines, hints, secondary chrome
	Err      lipgloss.Style // failures on the status line
	OK       lipgloss.Style // success confirmations
	Warn     lipgloss.Style // partial outcomes
	Accent   lipgloss.Style // the focused or active element
	Prompt   lipgloss.Style // input prompt labels
	Cursor   lipgloss.Style // the selection bar: reverse only, never color
	Inactive lipgloss.Style // the unfocused pane's cursor mark and title

	// Identity styles, same palette the Env methods use.
	Pips        map[byte]lipgloss.Style
	Multi       lipgloss.Style
	ColorlessID lipgloss.Style
}

// DefaultTheme builds the Theme from the shared definitions.
func DefaultTheme() Theme {
	pips := make(map[byte]lipgloss.Style, len(identityColors))
	for letter, c := range identityColors {
		pips[letter] = lipgloss.NewStyle().Foreground(c)
	}
	return Theme{
		Title:    lgBold,
		Help:     lgFaint,
		Err:      lgErr,
		OK:       lgOK,
		Warn:     lgWarn,
		Accent:   lgAccent,
		Prompt:   lgBold,
		Cursor:   lipgloss.NewStyle().Reverse(true),
		Inactive: lgFaint,

		Pips:        pips,
		Multi:       lipgloss.NewStyle().Foreground(identityMulti),
		ColorlessID: lipgloss.NewStyle().Foreground(identityC),
	}
}
