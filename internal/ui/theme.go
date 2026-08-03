package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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
	// Gain reuses the ok hue without any bold: a delta is data, not an
	// alarm. The +/− sign stays the piped-safe channel. Its old pair Loss
	// went with the binary Delta — graded losses ride the Diverge ramp.
	lgGain = lipgloss.NewStyle().Foreground(semOK)
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

// Accent styles the focused element: pane titles, selection markers.
func (e Env) Accent() Style { return e.styled(lgAccent) }

// gradeLo and gradeHi anchor the Grade ramp: a muted amber for "barely
// qualifies" blending to the gain green for "as good as this column gets".
// Truecolor values; lipgloss degrades them to the nearest 256/16 color.
var (
	gradeLo = [3]uint8{0xb0, 0x8a, 0x2a}
	gradeHi = [3]uint8{0x37, 0xc4, 0x5c}
)

// Grade styles a normalized 0..1 ratio on a color ramp — the market view's
// PAYS and BELOW columns, where "how good" is a position on a scale rather
// than a gain/loss direction. Callers normalize with the domain's own
// floors (market.LiquidityGrade and friends); this only paints.
func (e Env) Grade(frac float64) Style {
	if !e.Color {
		return plain
	}
	frac = min(max(frac, 0), 1)
	return e.styled(lipgloss.NewStyle().Foreground(blend(gradeLo, gradeHi, frac)))
}

// blend linearly interpolates between two RGB anchors at t in 0..1 — the
// one lerp every ramp shares, so three ramps cannot drift apart in how
// they mix color.
func blend(a, b [3]uint8, t float64) lipgloss.Color {
	l := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", l(a[0], b[0]), l(a[1], b[1]), l(a[2], b[2])))
}

// heatLo, heatMid and heatHi anchor the Heat ramp: the Grade ramp's green
// for full agreement, through a light red, saturating at a dark red for
// full disagreement.
var (
	heatLo  = gradeHi // agreement wears the same green everywhere
	heatMid = [3]uint8{0xd9, 0x7b, 0x73}
	heatHi  = [3]uint8{0x9c, 0x2a, 0x24}
)

// Heat styles a normalized 0..1 ratio on a green-to-red ramp — agreement
// green, disagreement red, light to dark as it grows. Grade's opposite
// temperament: there high is the virtue; here high is the warning.
func (e Env) Heat(frac float64) Style {
	if !e.Color {
		return plain
	}
	frac = min(max(frac, 0), 1)
	a, b := heatLo, heatMid
	t := frac * 2
	if frac > 0.5 {
		a, b = heatMid, heatHi
		t = (frac - 0.5) * 2
	}
	return e.styled(lipgloss.NewStyle().Foreground(blend(a, b, t)))
}

// divergeLoss, divergeMid and divergeGain anchor the Diverge ramp: its
// own trio, a step brighter than the Grade/Heat anchors — the movers
// table is the one surface where the color is the headline, and the
// muted shared hues read washed-out there (owner feedback). The loss red
// and gain green sit at comparable luminance on a dark background so the
// biggest sinker reads as loud as the biggest riser; the mid gray is a
// measured zero — data, not an alarm — and no collision with Dim, which
// is the faint attribute rather than a color.
var (
	divergeLoss = [3]uint8{0xf2, 0x63, 0x63}
	divergeMid  = [3]uint8{0x9e, 0x9e, 0x9e}
	divergeGain = [3]uint8{0x3f, 0xe0, 0x6c}
)

// Diverge styles a signed -1..1 fraction on a diverging ramp: the loss
// red at -1 through the neutral gray at zero to the gain green at +1,
// intensity carrying magnitude. The movers columns' ramp — each column
// normalizes against its own extreme (see DivergeFrac), so sorting by the
// column fades monotonically. The +/− sign stays in the text, so piped
// output keeps the direction without the color.
func (e Env) Diverge(frac float64) Style {
	if !e.Color {
		return plain
	}
	frac = min(max(frac, -1), 1)
	c := blend(divergeMid, divergeGain, frac)
	if frac < 0 {
		c = blend(divergeMid, divergeLoss, -frac)
	}
	return e.styled(lipgloss.NewStyle().Foreground(c))
}

// DivergeFrac maps a signed value onto Diverge's -1..1: sign preserved,
// magnitude square-root-compressed against the column's largest absolute
// value — movers are heavy-tailed, and under a linear scale one whale
// grays out every mid-size move worth noticing. A zero extent (an
// all-zero column) maps everything to the neutral midpoint.
func DivergeFrac(v, extent float64) float64 {
	if extent <= 0 || v == 0 {
		return 0
	}
	f := min(math.Sqrt(math.Abs(v)/extent), 1)
	if v < 0 {
		return -f
	}
	return f
}

// Pip styles one identity letter with its own color: W parchment, U island,
// and so on. Unknown letters render plain.
func (e Env) Pip(letter byte) Style {
	c, ok := identityColors[letter]
	if !ok {
		return plain
	}
	return e.styled(lipgloss.NewStyle().Foreground(c))
}

// PipsStyle styles a whole pip string (ui.Pips) letter by letter, each
// identity letter in its own color; anything else — the unknown dash —
// passes through untouched. This is the ID column's cell style.
func (e Env) PipsStyle() Style {
	if !e.Color {
		return plain
	}
	return func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if r < 128 {
				if c, ok := identityColors[byte(r)]; ok {
					b.WriteString(lipgloss.NewStyle().Foreground(c).Render(string(r)))
					continue
				}
			}
			b.WriteRune(r)
		}
		return b.String()
	}
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

// Identity is the Theme-side of Env.Identity: the lipgloss style for a
// card's whole color identity — one color tints, several read gold,
// colorless reads wastes, unknown (nil) stays unstyled.
func (t Theme) Identity(colors []string) lipgloss.Style {
	st, ok := identityStyle(colors)
	if !ok {
		return lipgloss.NewStyle()
	}
	return st
}

// PipString styles a string character by character, each identity letter in
// its own pip color, everything else untouched — the Theme-side of
// Env.PipsStyle.
func (t Theme) PipString(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 {
			if st, ok := t.Pips[byte(r)]; ok {
				b.WriteString(st.Render(string(r)))
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ManaCost renders a printed cost ("{2}{W}{U}") with each colored symbol in
// its pip color. Braces, numbers and the symbols outside the wheel ({X},
// {T}, the phyrexian/hybrid punctuation) stay plain — only W/U/B/R/G/C
// carry meaning as color. Unknown or empty costs pass through untouched.
func (t Theme) ManaCost(cost string) string {
	return t.PipString(cost)
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
