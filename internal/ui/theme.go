package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	identityW     = lipgloss.AdaptiveColor{Light: "#9a8f4d", Dark: "#f0f2c0"}
	identityU     = lipgloss.AdaptiveColor{Light: "#2a6395", Dark: "#b5cde3"}
	identityB     = lipgloss.AdaptiveColor{Light: "#6b5f66", Dark: "#aca29a"}
	identityR     = lipgloss.AdaptiveColor{Light: "#b0492a", Dark: "#db8664"}
	identityG     = lipgloss.AdaptiveColor{Light: "#3a7d4f", Dark: "#93b483"}
	identityC     = lipgloss.AdaptiveColor{Light: "#7d786f", Dark: "#beb9b2"}
	identityMulti = lipgloss.AdaptiveColor{Light: "#a8860b", Dark: "#d4af37"}
)

var identityColors = map[byte]lipgloss.AdaptiveColor{
	'W': identityW,
	'U': identityU,
	'B': identityB,
	'R': identityR,
	'G': identityG,
	'C': identityC,
}

var (
	semErr    = lipgloss.Color("9")
	semOK     = lipgloss.Color("10")
	semWarn   = lipgloss.Color("11")
	semAccent = lipgloss.Color("12")
)

var (
	lgBold   = lipgloss.NewStyle().Bold(true)
	lgFaint  = lipgloss.NewStyle().Faint(true)
	lgErr    = lipgloss.NewStyle().Bold(true).Foreground(semErr)
	lgOK     = lipgloss.NewStyle().Foreground(semOK)
	lgWarn   = lipgloss.NewStyle().Foreground(semWarn)
	lgAccent = lipgloss.NewStyle().Bold(true).Foreground(semAccent)

	lgGain = lipgloss.NewStyle().Foreground(semOK)
)

func (e Env) styled(st lipgloss.Style) Style {
	if !e.Color {
		return plain
	}
	return func(s string) string { return st.Render(s) }
}

func (e Env) OK() Style { return e.styled(lgOK) }

func (e Env) Err() Style { return e.styled(lgErr) }

func (e Env) Warn() Style { return e.styled(lgWarn) }

func (e Env) Gain() Style { return e.styled(lgGain) }

func (e Env) Accent() Style { return e.styled(lgAccent) }

var (
	gradeLo = [3]uint8{0xb0, 0x8a, 0x2a}
	gradeHi = [3]uint8{0x37, 0xc4, 0x5c}
)

func (e Env) Grade(frac float64) Style {
	if !e.Color {
		return plain
	}
	frac = min(max(frac, 0), 1)
	return e.styled(lipgloss.NewStyle().Foreground(blend(gradeLo, gradeHi, frac)))
}

func blend(a, b [3]uint8, t float64) lipgloss.Color {
	l := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", l(a[0], b[0]), l(a[1], b[1]), l(a[2], b[2])))
}

var (
	heatLo  = gradeHi
	heatMid = [3]uint8{0xd9, 0x7b, 0x73}
	heatHi  = [3]uint8{0x9c, 0x2a, 0x24}
)

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

var (
	divergeLoss = [3]uint8{0xf2, 0x63, 0x63}
	divergeMid  = [3]uint8{0x9e, 0x9e, 0x9e}
	divergeGain = [3]uint8{0x3f, 0xe0, 0x6c}
)

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

func (e Env) Pip(letter byte) Style {
	c, ok := identityColors[letter]
	if !ok {
		return plain
	}
	return e.styled(lipgloss.NewStyle().Foreground(c))
}

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

type Theme struct {
	Title    lipgloss.Style
	Help     lipgloss.Style
	Err      lipgloss.Style
	OK       lipgloss.Style
	Warn     lipgloss.Style
	Accent   lipgloss.Style
	Prompt   lipgloss.Style
	Cursor   lipgloss.Style
	Inactive lipgloss.Style

	Pips        map[byte]lipgloss.Style
	Multi       lipgloss.Style
	ColorlessID lipgloss.Style
}

func (t Theme) Identity(colors []string) lipgloss.Style {
	st, ok := identityStyle(colors)
	if !ok {
		return lipgloss.NewStyle()
	}
	return st
}

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

func (t Theme) ManaCost(cost string) string {
	return t.PipString(cost)
}

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
