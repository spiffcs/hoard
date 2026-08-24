package ui

import (
	"image"
	"image/color"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

func TestDetectImageTier(t *testing.T) {
	clear := func(t *testing.T) {
		t.Helper()

		for _, k := range []string{"HOARD_CARD_IMAGES", "TERM",
			"TERM_PROGRAM", "KITTY_WINDOW_ID", "COLORTERM"} {
			t.Setenv(k, "")
		}
		t.Setenv("NO_COLOR", "x")
		os.Unsetenv("NO_COLOR")
	}
	cases := []struct {
		name string
		env  map[string]string
		want ImageTier
	}{
		{"kitty term", map[string]string{"TERM": "xterm-kitty"}, ImageKitty},
		{"ghostty", map[string]string{"TERM_PROGRAM": "ghostty"}, ImageKitty},
		{"wezterm", map[string]string{"TERM_PROGRAM": "WezTerm"}, ImageKitty},
		{"kitty window id", map[string]string{"KITTY_WINDOW_ID": "3"}, ImageKitty},
		{"iterm2 halfblocks", map[string]string{"TERM_PROGRAM": "iTerm.app"}, ImageHalfblock},
		{"plain truecolor", map[string]string{"COLORTERM": "truecolor"}, ImageHalfblock},
		{"dumb terminal", map[string]string{"TERM": "xterm-256color"}, ImageNone},
		{"opted out", map[string]string{"HOARD_CARD_IMAGES": "0", "TERM": "xterm-kitty"}, ImageNone},
		{"opted out off", map[string]string{"HOARD_CARD_IMAGES": "off", "TERM": "xterm-kitty"}, ImageNone},
		{"forced kitty", map[string]string{"HOARD_CARD_IMAGES": "kitty", "TERM": "xterm-256color"}, ImageKitty},
		{"forced halfblock", map[string]string{"HOARD_CARD_IMAGES": "halfblock", "TERM": "xterm-kitty"}, ImageHalfblock},
		{"force beats NO_COLOR", map[string]string{"HOARD_CARD_IMAGES": "halfblock", "NO_COLOR": "1"}, ImageHalfblock},
		{"no color wins", map[string]string{"TERM": "xterm-kitty", "NO_COLOR": "1"}, ImageNone},
		{"tmux gets nothing", map[string]string{"TERM": "tmux-256color", "COLORTERM": "truecolor"}, ImageNone},
		{"screen gets nothing", map[string]string{"TERM": "screen-256color", "TERM_PROGRAM": "iTerm.app"}, ImageNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clear(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := DetectImageTier(); got != tc.want {
				t.Errorf("tier = %v, want %v", got, tc.want)
			}
		})
	}
}

func testImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 2, 4))
	colors := []color.RGBA{
		{255, 0, 0, 255}, {0, 255, 0, 255},
		{0, 0, 255, 255}, {255, 255, 255, 255},
		{10, 20, 30, 255}, {40, 50, 60, 255},
		{70, 80, 90, 255}, {100, 110, 120, 255},
	}
	for i, c := range colors {
		img.Set(i%2, i/2, c)
	}
	return img
}

func TestHalfblocks(t *testing.T) {
	lines := Halfblocks(testImage(), 2)
	if len(lines) != 2 {
		t.Fatalf("2×4 image at 2 cols = %d rows, want 2 (two pixels per cell)", len(lines))
	}
	for _, l := range lines {
		if got := ansi.StringWidth(l); got != 2 {
			t.Errorf("line width %d, want 2: %q", got, l)
		}
		if !strings.HasSuffix(l, "\x1b[0m") {
			t.Errorf("line does not reset: %q", l)
		}
	}

	if !strings.Contains(lines[0], "\x1b[38;2;255;0;0m\x1b[48;2;0;0;255m▀") {
		t.Errorf("top-left cell wrong: %q", lines[0])
	}
}

func TestSampleAveragesTheBlockItCovers(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))

	for qy := range 2 {
		for qx := range 2 {
			base := uint8(10 + 40*(qy*2+qx))
			for i, v := range []uint8{base, base + 10, base + 20, base + 30} {
				img.Set(qx*2+i%2, qy*2+i/2, color.RGBA{v, v, v, 255})
			}
		}
	}

	got := sample(img, 2, 2)
	for qy := range 2 {
		for qx := range 2 {
			want := uint8(10 + 40*(qy*2+qx) + 15)
			if p := got[qy][qx]; p.r != want || p.g != want || p.b != want {
				t.Errorf("cell (%d,%d) = %v, want %d on every channel — the mean of its 2×2 block",
					qx, qy, p, want)
			}
		}
	}
}

func TestKittyImage(t *testing.T) {
	transmit, placeholder, err := KittyImage(testImage(), 91, 2, 2)
	if err != nil {
		t.Fatalf("KittyImage: %v", err)
	}
	if !strings.HasPrefix(transmit, "\x1b_G") || !strings.Contains(transmit, "U=1") ||
		!strings.Contains(transmit, "i=91") {
		t.Errorf("transmit lacks the virtual-placement APC shape: %.80q", transmit)
	}
	if len(placeholder) == 0 {
		t.Fatal("no placeholder lines")
	}
	for _, l := range placeholder {
		if got := ansi.StringWidth(l); got != 2 {
			t.Errorf("placeholder width %d, want 2: %q", got, l)
		}
		if !strings.ContainsRune(l, kitty.Placeholder) {
			t.Errorf("placeholder line lacks U+10EEEE: %q", l)
		}
		if !strings.Contains(l, "\x1b[38;5;91m") {
			t.Errorf("placeholder does not carry the image id: %q", l)
		}
	}
}

func TestKittyPlacementNeverLeavesHorizontalSlack(t *testing.T) {
	card := image.NewRGBA(image.Rect(0, 0, 488, 680))

	for _, aspect := range []float64{1.8, 2.0, 2.2, 2.8} {
		for _, cols := range []int{14, 24, 30, 42} {
			_, placeholder, err := KittyImage(card, 91, cols, aspect)
			if err != nil {
				t.Fatalf("KittyImage(%d cols, aspect %.1f): %v", cols, aspect, err)
			}
			need := float64(cols) * 680 / (488 * aspect)
			if got := float64(len(placeholder)); got < need {
				t.Errorf("cols %d aspect %.1f: placement is %v rows but the card needs %.2f — "+
					"the terminal will shrink it to fit the height and float it sideways",
					cols, aspect, got, need)
			}
		}
	}
}

func TestKittyImageClearsTheOldPlacementFirst(t *testing.T) {
	card := image.NewRGBA(image.Rect(0, 0, 488, 680))

	transmit, _, err := KittyImage(card, 91, 30, 1.929)
	if err != nil {
		t.Fatalf("KittyImage: %v", err)
	}

	del := "\x1b_Ga=d,d=I,i=91\x1b\\"
	if !strings.HasPrefix(transmit, del) {
		head := transmit
		if len(head) > 60 {
			head = head[:60]
		}
		t.Errorf("transmit must open by deleting image 91 so the terminal drops the previous\n"+
			"placement geometry; without it a re-transmit under the same id keeps the old box.\n"+
			"want prefix %q\ngot          %q...", del, head)
	}
	if strings.Count(transmit, "a=d") != 1 {
		t.Errorf("expected exactly one delete, got %d", strings.Count(transmit, "a=d"))
	}
}
