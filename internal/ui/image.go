package ui

// Card images in the terminal. Two tiers render a bitmap:
//
//   - Kitty graphics with Unicode placeholders (Ghostty, Kitty, WezTerm):
//     the image is transmitted once under an id, then placed by ordinary
//     text cells that carry the id in their foreground color and their
//     row/column in diacritics. Because the placement is plain text it
//     flows through View() and survives bubbletea's repaints — the reason
//     this protocol works under a cell-diffing renderer and cursor-anchored
//     ones (iTerm2's OSC 1337) do not.
//   - Halfblock cells (any truecolor terminal, iTerm2 included): each ▀
//     carries two pixels, top in the foreground, bottom in the background.
//     Pure text, no protocol, universally safe.

import (
	"fmt"
	"image"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi/kitty"
)

// ImageTier is how capable the terminal is of drawing a bitmap.
type ImageTier int

const (
	ImageNone      ImageTier = iota
	ImageHalfblock           // truecolor cells, two pixels per cell
	ImageKitty               // kitty graphics protocol, Unicode placeholders
)

// DetectImageTier reads the environment once, before the program starts —
// the same moment lipgloss probes the background. Images are on by default
// wherever the terminal can draw them; HOARD_CARD_IMAGES is the override:
// "0" or "off" disables them, "kitty" or "halfblock" forces a tier past
// the fingerprinting, for a terminal this list doesn't know. An explicit
// force wins over everything — the user typed it this invocation.
// Otherwise NO_COLOR turns images off with the rest of the color, and
// multiplexers (tmux, screen) get nothing: graphics passthrough is its
// own project, and mangled cells are worse than no picture.
func DetectImageTier() ImageTier {
	switch os.Getenv("HOARD_CARD_IMAGES") {
	case "0", "off":
		return ImageNone
	case "kitty":
		return ImageKitty
	case "halfblock":
		return ImageHalfblock
	}
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return ImageNone
	}
	term, prog := os.Getenv("TERM"), os.Getenv("TERM_PROGRAM")
	if strings.Contains(term, "tmux") || strings.Contains(term, "screen") {
		return ImageNone
	}
	if strings.Contains(term, "kitty") || os.Getenv("KITTY_WINDOW_ID") != "" ||
		prog == "ghostty" || prog == "WezTerm" {
		return ImageKitty
	}
	// iTerm2's own protocol is cursor-anchored and gets clobbered by the
	// renderer's repaints, so it takes the halfblock tier with every other
	// truecolor terminal.
	if os.Getenv("COLORTERM") == "truecolor" || os.Getenv("COLORTERM") == "24bit" ||
		prog == "iTerm.app" {
		return ImageHalfblock
	}
	return ImageNone
}

// Halfblocks renders img as cols-wide rows of ▀ cells, two pixels per
// cell. Row count follows the image's aspect: a terminal cell is about
// half as wide as it is tall, so a cols×(2·rows) pixel grid has roughly
// square pixels.
func Halfblocks(img image.Image, cols int) []string {
	b := img.Bounds()
	if cols <= 0 || b.Dx() <= 0 || b.Dy() <= 0 {
		return nil
	}
	rows := max(cols*b.Dy()/(b.Dx()*2), 1)
	px := sample(img, cols, rows*2)

	lines := make([]string, rows)
	for y := range rows {
		var sb strings.Builder
		for x := range cols {
			top, bot := px[2*y][x], px[2*y+1][x]
			fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀",
				top.r, top.g, top.b, bot.r, bot.g, bot.b)
		}
		sb.WriteString("\x1b[0m")
		lines[y] = sb.String()
	}
	return lines
}

type rgb struct{ r, g, b uint8 }

// sample scales img to w×h with nearest-neighbour box centres — crude next
// to a real resampler, but this is thumbnail duty, and it keeps the image
// path dependency-free.
func sample(img image.Image, w, h int) [][]rgb {
	b := img.Bounds()
	out := make([][]rgb, h)
	for y := range h {
		out[y] = make([]rgb, w)
		sy := b.Min.Y + (2*y+1)*b.Dy()/(2*h)
		for x := range w {
			sx := b.Min.X + (2*x+1)*b.Dx()/(2*w)
			r, g, bl, _ := img.At(sx, sy).RGBA()
			out[y][x] = rgb{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8)}
		}
	}
	return out
}

// KittyImage builds the two halves of a virtual placement: the transmit
// sequence (written to the terminal once) and the placeholder lines that
// place it (rendered like any other text). The terminal scales the image
// into the cols×rows rectangle, so no pixel geometry is needed. Rows
// follow the image's aspect the same way Halfblocks' do. Retransmitting
// under the same id replaces the previous image.
func KittyImage(img image.Image, id, cols int) (transmit string, placeholder []string, err error) {
	b := img.Bounds()
	if cols <= 0 || b.Dx() <= 0 || b.Dy() <= 0 {
		return "", nil, fmt.Errorf("empty image")
	}
	rows := max(cols*b.Dy()/(b.Dx()*2), 1)

	var buf strings.Builder
	err = kitty.EncodeGraphics(&buf, img, &kitty.Options{
		Action:           kitty.TransmitAndPut,
		Transmission:     kitty.Direct,
		Format:           kitty.PNG,
		ID:               id,
		Quite:            2, // silence OK and error replies; nobody is reading them
		Chunk:            true,
		VirtualPlacement: true,
		Columns:          cols,
		Rows:             rows,
	})
	if err != nil {
		return "", nil, err
	}
	return buf.String(), kittyPlaceholder(id, cols, rows), nil
}

// kittyPlaceholder renders the placement cells: each is U+10EEEE carrying
// its row and column in diacritics, with the image id in the foreground
// color (a 256-color index — ids stay under 256 by construction).
func kittyPlaceholder(id, cols, rows int) []string {
	lines := make([]string, rows)
	for r := range rows {
		var b strings.Builder
		fmt.Fprintf(&b, "\x1b[38;5;%dm", id)
		for c := range cols {
			b.WriteRune(kitty.Placeholder)
			b.WriteRune(kitty.Diacritic(r))
			b.WriteRune(kitty.Diacritic(c))
		}
		b.WriteString("\x1b[0m")
		lines[r] = b.String()
	}
	return lines
}
