package command

import "github.com/spiffcs/hoard/internal/ui"

func unresolvedHeading(n int) string {
	return ui.Plural(n,
		"card could not be resolved and was skipped:",
		"cards could not be resolved and were skipped:")
}

func unreadableHeading(n int) string {
	return ui.Plural(n,
		"line could not be read and was skipped:",
		"lines could not be read and were skipped:")
}
