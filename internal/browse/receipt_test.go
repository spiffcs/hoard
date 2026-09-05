package browse

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/tui"
)

func TestTheAddReceiptGroupsThousands(t *testing.T) {
	got := addReceiptLine(2, 1234, tui.Summary{})
	if !strings.Contains(got, "$1,234.00") {
		t.Errorf("receipt = %q, want it to contain $1,234.00", got)
	}
}
