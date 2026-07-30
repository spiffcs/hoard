package ui

import (
	"strconv"
	"strings"
)

// unknown is shown where a market price hasn't been fetched.
const unknown = "—"

// Money formats a dollar amount with thousands separators and exactly two
// decimals: 1901.7 renders as "$1,901.70".
//
// Note that go-humanize's Commaf is deliberately not used here — it formats
// with -1 precision and strips trailing zeros, which turns $1,901.70 into
// "$1,901.7" and breaks decimal alignment down a column of money.
func Money(v float64) string {
	if v < 0 {
		return "-$" + group(strconv.FormatFloat(-v, 'f', 2, 64))
	}
	return "$" + group(strconv.FormatFloat(v, 'f', 2, 64))
}

// MoneyPtr formats an optional price, rendering an em dash when it's unknown.
func MoneyPtr(p *float64) string {
	if p == nil {
		return unknown
	}
	return Money(*p)
}

// Count formats a card count with thousands separators: 1878 -> "1,878".
func Count(n int) string {
	return group(strconv.Itoa(n))
}

// Percent formats a fraction (0…1) as a share, e.g. 0.0509 -> "5.1%".
func Percent(frac float64) string {
	if frac <= 0 {
		return ""
	}
	return strconv.FormatFloat(frac*100, 'f', 1, 64) + "%"
}

// group inserts thousands separators into the integer part of a plain decimal
// string, leaving any fractional part alone.
func group(s string) string {
	intPart, frac, hasFrac := strings.Cut(s, ".")

	neg := strings.HasPrefix(intPart, "-")
	if neg {
		intPart = intPart[1:]
	}

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(intPart[i])
	}
	if hasFrac {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	return b.String()
}

// Finish labels a card's finish for a column.
//
// "normal" renders as a dash: a column reading "normal" down every row is noise,
// and the foils are what want pointing out.
func Finish(finish string) string {
	if finish == "normal" {
		return "-"
	}
	return finish
}

// Estimated marks a value the primary source could not supply, so a fallback
// vendor's figure never passes for the real one.
func Estimated(s, altSource string) string {
	if altSource == "" {
		return s
	}
	return s + "*"
}

// Printing is the set/number label shown beside a card's name.
func Printing(setCode, collectorNumber string) string {
	return setCode + "/" + collectorNumber
}

// Qty labels an owned quantity. The × keeps a bare count from reading as yet
// another price in a row full of numbers.
func Qty(n int) string { return "×" + Count(n) }

// SignedMoney formats a movement, always carrying its sign. Money already
// writes a minus; only the rise needs marking, so a column of them reads as
// direction rather than as a column of amounts.
func SignedMoney(v float64) string {
	if v > 0 {
		return "+" + Money(v)
	}
	return Money(v)
}

// SignedPercent formats a movement as a percentage.
//
// Percent is for shares of a total and renders anything at or below zero as
// empty, which is exactly the half of a movers list that matters. An empty
// string is returned only for zero, where the movement has no direction (or,
// for a caller dividing by an old price, where a percentage is meaningless).
func SignedPercent(frac float64) string {
	if frac == 0 {
		return ""
	}
	s := strconv.FormatFloat(frac*100, 'f', 1, 64) + "%"
	if frac > 0 {
		return "+" + s
	}
	return s
}
