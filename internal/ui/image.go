package ui

import (
	"fmt"
	"image"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi/kitty"
)

type ImageTier int

const (
	ImageNone ImageTier = iota
	ImageHalfblock
	ImageKitty
)

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

	if os.Getenv("COLORTERM") == "truecolor" || os.Getenv("COLORTERM") == "24bit" ||
		prog == "iTerm.app" {
		return ImageHalfblock
	}
	return ImageNone
}

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

func sample(img image.Image, w, h int) [][]rgb {
	b := img.Bounds()
	out := make([][]rgb, h)
	for y := range h {
		out[y] = make([]rgb, w)

		y0 := b.Min.Y + y*b.Dy()/h
		y1 := max(b.Min.Y+(y+1)*b.Dy()/h, y0+1)
		for x := range w {
			x0 := b.Min.X + x*b.Dx()/w
			x1 := max(b.Min.X+(x+1)*b.Dx()/w, x0+1)

			var sr, sg, sb, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					r, g, bl, _ := img.At(sx, sy).RGBA()
					sr += uint64(r >> 8)
					sg += uint64(g >> 8)
					sb += uint64(bl >> 8)
					n++
				}
			}
			out[y][x] = rgb{uint8(sr / n), uint8(sg / n), uint8(sb / n)}
		}
	}
	return out
}

func KittyImage(img image.Image, id, cols int, cellAspect float64) (transmit string, placeholder []string, err error) {
	b := img.Bounds()
	if cols <= 0 || b.Dx() <= 0 || b.Dy() <= 0 {
		return "", nil, fmt.Errorf("empty image")
	}
	if cellAspect <= 0 {
		cellAspect = 2
	}

	rows := max(int(float64(cols*b.Dy())/(float64(b.Dx())*cellAspect)), 1)

	var buf strings.Builder
	err = kitty.EncodeGraphics(&buf, img, &kitty.Options{
		Action:           kitty.TransmitAndPut,
		Transmission:     kitty.Direct,
		Format:           kitty.PNG,
		ID:               id,
		Quite:            2,
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
