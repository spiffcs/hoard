package tui

import (
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

// The tier lines, in dollars — fixed. The knobs that once moved them (the
// HOARD_SCAN_WIN / HOARD_SCAN_JACKPOT environment variables and the capture
// step's `win 5` command line) are gone: sound configuration lives on the
// phone now, in Hoardling's Settings tab, which re-tiers every priced card
// from the amount this side already sends. What is decided here only feeds
// the wire's three-tier verdict — kept for the macOS helper's HUD and for
// older phone builds that still take the wire's word.
const (
	winThreshold     = 1.0
	jackpotThreshold = 20.0
)

// tierFor maps a price to its celebration tier. It takes the price as a
// pointer, not a collapsed zero: an unpriced card is "unpriced" (a shrug, the
// plain chime), never bulk-with-$0.00.
func tierFor(price *float64) string {
	switch {
	case price == nil:
		return tierUnpriced
	case *price >= jackpotThreshold:
		return tierJackpot
	case *price >= winThreshold:
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
