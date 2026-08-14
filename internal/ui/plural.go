package ui

import "fmt"

func Plural(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func PluralCount(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return Count(n) + " " + plural
}
