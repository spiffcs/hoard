package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/finish"
)

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

const (
	unknown    = "—"
	suppressed = "-"
)

func Money(v float64) string {
	if v < 0 {
		return "-$" + group(strconv.FormatFloat(-v, 'f', 2, 64))
	}
	return "$" + group(strconv.FormatFloat(v, 'f', 2, 64))
}

func MoneyPtr(p *float64) string {
	if p == nil {
		return unknown
	}
	return Money(*p)
}

func Count(n int) string {
	return group(strconv.Itoa(n))
}

func Percent(frac float64) string {
	if frac <= 0 {
		return ""
	}
	return strconv.FormatFloat(frac*100, 'f', 1, 64) + "%"
}

func PercentAlways(frac float64) string {
	return strconv.FormatFloat(frac*100, 'f', 1, 64) + "%"
}

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

func Finish(fin finish.Finish) string {
	if fin == finish.Nonfoil {
		return suppressed
	}
	return fin.String()
}

func FinishTreated(fin finish.Finish, treatment string) string {
	if fin == finish.Foil && treatment != "" {
		return treatment
	}
	return Finish(fin)
}

func Condition(condition string) string {
	if condition == "" || condition == "unknown" {
		return unknown
	}
	return strings.ToUpper(condition)
}

const wubrgOrder = "WUBRG"

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

const HeaderIdentity = "WUBRG"

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

func Printing(setCode, collectorNumber string) string {

	if setCode == "" && collectorNumber == "" {
		return unknown
	}
	return setCode + "/" + collectorNumber
}

func Qty(n int) string { return "×" + Count(n) }

func SignedMoney(v float64) string {
	if v > 0 {
		return "+" + Money(v)
	}
	return Money(v)
}

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
