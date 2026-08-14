package tui

import (
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

const (
	tierBulk     = "bulk"
	tierWin      = "win"
	tierJackpot  = "jackpot"
	tierUnpriced = "unpriced"
	tierReview   = "review"
)

const (
	winThreshold     = 1.0
	jackpotThreshold = 20.0
)

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

func priceValuePtr(c scryfall.Card, finish finish.Finish) *float64 {
	return finishPrice(c, finish)
}
