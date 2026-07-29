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
