package tui

import (
	"os"
	"strconv"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// The camera HUD's casino treatment: every resolved card flashes its price on
// the camera window with a tier-appropriate celebration — muted grey for
// bulk, gold for a win, a coin shower for a jackpot. The tier decision lives
// here, Go-side, where prices already are and where a table test can pin the
// boundaries; the Swift helper just renders what it is told.

// Tier names on the wire; the helper switches on these strings. tierReview
// is not a price tier: it marks a card that queued for review, which the
// helper renders as a muted "Needs Review" with a rising two-note question
// sound.
const (
	tierBulk     = "bulk"
	tierWin      = "win"
	tierJackpot  = "jackpot"
	tierUnpriced = "unpriced"
	tierReview   = "review"
)

// Default tier thresholds in dollars, overridable per run via the
// HOARD_SCAN_WIN / HOARD_SCAN_JACKPOT environment variables — the same
// knob-style the helper's own tunables use.
const (
	defaultWinThreshold     = 1.0
	defaultJackpotThreshold = 20.0
)

// envFloat reads a float tunable from the environment, falling back on
// absence or garbage — a mistyped threshold should degrade to the default,
// not break scanning.
func envFloat(name string, fallback float64) float64 {
	v, err := strconv.ParseFloat(os.Getenv(name), 64)
	if err != nil {
		return fallback
	}
	return v
}

// tierFor maps a price to its celebration tier. It takes the price as a
// pointer, not a collapsed zero: an unpriced card is "unpriced" (a shrug, the
// plain chime), never bulk-with-$0.00.
func tierFor(price *float64, win, jackpot float64) string {
	switch {
	case price == nil:
		return tierUnpriced
	case *price >= jackpot:
		return tierJackpot
	case *price >= win:
		return tierWin
	default:
		return tierBulk
	}
}

// priceValuePtr is priceValue's pointer-preserving sibling: the finish's
// price, or nil when the printing has no price for it — so tierFor can tell
// unpriced from worthless.
func priceValuePtr(c scryfall.Card, finish string) *float64 {
	return finishPrice(c, finish)
}
