package ui

import (
	"fmt"
	"strconv"
	"strings"
)

// Bytes renders a size the way a person would say it.
//
// The smallest tier is bytes rather than kilobytes so that a nonzero size
// never reads as "0 KB" — a download prompt that claims to be about to
// transfer nothing is worse than one that says an awkward number.
func Bytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// The two marks a column uses when a cell has no ordinary value, and the
// difference between them. Both exist; conflating them loses information.
//
//   - unknown (em dash) says nobody knows: no price was fetched, no document was
//     stored, nobody assessed the card. The value is genuinely absent.
//   - suppressed (hyphen) says the value is known and dull. A non-foil card is
//     definitely non-foil; printing the word down every row buries the handful
//     of foils that are worth seeing.
//
// A reader can tell them apart, which is the point: an em-dash column is a gap
// in hoard's knowledge and may be worth filling, a hyphen column is just the
// ordinary case.
const (
	unknown    = "—"
	suppressed = "-"
)

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

// PercentAlways formats a fraction as a percentage even at or below zero.
// Percent's empty-for-nonpositive contract reads as "unknown" in a column
// where zero and negative are the interesting cases — a spread of -4.2%
// is a bid beating the low ask, not a missing number (observed live).
func PercentAlways(frac float64) string {
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
// "nonfoil" renders as the suppressed mark, not the unknown one: the card is
// definitely non-foil. A column reading "nonfoil" down every row is noise, and
// the foils are what want pointing out.
func Finish(finish string) string {
	if finish == "nonfoil" {
		return suppressed
	}
	return finish
}

// FinishTreated is Finish with the printing's foil treatment in reach: a
// foil copy of a treated printing names its treatment — "ripple" beats
// "foil" for a card whose price and physical reality are the ripple foil.
// Non-foil copies stay plain: the treatment describes the foiling.
func FinishTreated(finish, treatment string) string {
	if finish == "foil" && treatment != "" {
		return treatment
	}
	return Finish(finish)
}

// Condition labels a holding's condition for a column.
//
// Condition is wear on a raw card — near mint through damaged — as assessed by
// whoever owned it. Upper case, because these read as abbreviations, and
// because it keeps the column visually distinct from the lower-case finish
// beside it.
//
// An unassessed card renders as the *unknown* mark, not the suppressed one:
// nobody has looked at it, which is a gap rather than a dull-but-known value.
// That is exactly the distinction Finish's hyphen draws against — a non-foil
// card is definitely non-foil, an unassessed one is not definitely near mint.
//
// The empty string is accepted alongside "unknown" because a zero-valued Go
// struct field can reach here before the store's orUnknown has normalized it.
// The column itself is NOT NULL and holds exactly one unknown value.
func Condition(condition string) string {
	if condition == "" || condition == "unknown" {
		return unknown
	}
	return strings.ToUpper(condition)
}

// wubrgOrder is the canonical identity ordering: the color wheel as printed
// on the back of every card, not the alphabet.
const wubrgOrder = "WUBRG"

// IdentityKey canonicalises a color identity into WUBRG order ("UW" and
// "WU" are the same identity, and must sort and render the same). Letters
// outside the wheel are dropped.
func IdentityKey(colors []string) string {
	var b strings.Builder
	for _, want := range wubrgOrder {
		for _, c := range colors {
			if c != "" && rune(c[0]) == want {
				b.WriteRune(want)
				break
			}
		}
	}
	return b.String()
}

// Pips renders a color identity as its pip letters: "WU" for Azorius, "C"
// for a colorless card, and the em dash for an unknown identity (nil — the
// card's document was never stored), matching the column convention that a
// dash is "unknown", never "empty". Styling is applied at render via
// Env.Pip, never here — piped output gets these exact letters.
func Pips(colors []string) string {
	if colors == nil {
		return unknown
	}
	key := IdentityKey(colors)
	if key == "" {
		return "C"
	}
	return key
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
	// A merged row spanning printings has no one set to name; the unknown
	// mark beats a bare "/".
	if setCode == "" && collectorNumber == "" {
		return unknown
	}
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
