package ui

import "time"

func AsOfDate(stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	return t.UTC().Format("2 Jan 2006")
}
