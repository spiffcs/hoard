package browse

import (
	"fmt"
	"image"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/ui"
)

func TestPreviewAndDetailUseDistinctKittyIDs(t *testing.T) {
	if detailImageID == previewImageID {
		t.Fatal("the two views must not share an image id: re-transmitting under one " +
			"leaves the other's placement geometry in place")
	}

	card := image.NewRGBA(image.Rect(0, 0, 488, 680))
	render := func(id int) (lines []string, transmit string) {
		l, tx, ok := renderImage(card, ui.ImageKitty, 30, 2, id)
		if !ok || len(l) == 0 {
			t.Fatalf("render for id %d failed", id)
		}
		return l, tx
	}

	detail, detailTx := render(detailImageID)
	preview, previewTx := render(previewImageID)

	for _, tc := range []struct {
		what string
		got  string
		id   int
	}{
		{"detail placeholder", detail[0], detailImageID},
		{"preview placeholder", preview[0], previewImageID},
	} {
		if want := fmt.Sprintf("\x1b[38;5;%dm", tc.id); !strings.HasPrefix(tc.got, want) {
			t.Errorf("%s should carry id %d in its foreground colour", tc.what, tc.id)
		}
	}

	for _, tc := range []struct {
		what string
		got  string
		id   int
	}{
		{"detail transmit", detailTx, detailImageID},
		{"preview transmit", previewTx, previewImageID},
	} {
		if want := fmt.Sprintf("i=%d", tc.id); !strings.Contains(tc.got, want) {
			t.Errorf("%s should address image %d", tc.what, tc.id)
		}
		if want := fmt.Sprintf("a=d,d=I,i=%d", tc.id); !strings.HasPrefix(tc.got, "\x1b_G"+want) {
			t.Errorf("%s should clear only its own id first", tc.what)
		}
	}
}
