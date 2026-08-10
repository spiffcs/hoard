package export

// The text decklist writer, and the reason it exists: every other shape in
// this package leaves hoard and cannot come back. `hoard import` skips deck
// rows on purpose — loose-importing them would pour a deck into a binder and
// count its cards twice — and the command it sends you to instead, `deck add
// --file`, reads text decklists only, which nothing here emitted. A deck could
// be exported and never restored. This writer speaks decksource/textlist.go's
// own grammar, so that reader takes the file back unchanged.

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
)

// sections are the boards a decklist can hold, in the order they are written,
// each paired with the header decksource/textlist.go recognizes for it.
var sections = []struct{ board, header string }{
	{"main", "Deck"},
	{"commander", "Commander"},
	{"side", "Sideboard"},
	{"maybe", "Maybeboard"},
}

// setCodeRE is textlist.go's own set group, restated so this writer can tell
// whether a pair will survive the trip. A set code that cannot match it would
// be read back as part of the card's name.
var setCodeRE = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// WriteText writes rows as a text decklist — the one shape `hoard deck add
// --file` reads, and therefore the only way a deck exported from hoard comes
// home.
//
// Condition and price have nowhere to go in this grammar and are dropped: this
// format carries what a deck *is*, not what it is worth or how worn it is. The
// canonical CSV remains the lossless one.
//
// The rows are expected to be a single container's, because the reader builds
// exactly one deck out of the file it is handed; `hoard export` enforces the
// scope, since a file that merges every deck into one is not a restore.
func WriteText(w io.Writer, rows []Row) error {
	byBoard := make(map[string][]Row, len(sections))
	for _, r := range Sorted(mergedForText(rows)) {
		b := boardOf(r.Board)
		byBoard[b] = append(byBoard[b], r)
	}

	// A list that is nothing but a main board needs no header: the reader
	// starts every file in main, and a bare "3 Sol Ring" list is what a
	// person pastes. Any other board is written under its header even when
	// it is the only one — a commander read back as a main-deck card is a
	// quiet corruption of the deck.
	used := 0
	for _, s := range sections {
		if len(byBoard[s.board]) > 0 {
			used++
		}
	}
	bare := used == 1 && len(byBoard["main"]) > 0

	var out []string
	for _, s := range sections {
		group := byBoard[s.board]
		if len(group) == 0 {
			continue
		}
		if !bare {
			if len(out) > 0 {
				// Blank lines are skipped by the reader and make a long
				// list readable to the person who has to edit one.
				out = append(out, "")
			}
			out = append(out, s.header)
		}
		for _, r := range group {
			out = append(out, textLine(r))
		}
	}
	if len(out) == 0 {
		return nil
	}
	_, err := io.WriteString(w, strings.Join(out, "\n")+"\n")
	return err
}

// textLine renders one holding the way textlist.go reads it back:
// "3 Cryptolith Rite (soi) 200", with the finish markers that reader knows.
func textLine(r Row) string {
	line := fmt.Sprintf("%d %s", r.Count, r.Name)
	// The set and collector number are what pin the printing on the way back
	// in; without them the reader falls back to the bare name and Scryfall
	// picks a printing on your behalf. They go out only when they can survive
	// the trip: textlist.go's set group takes alphanumerics only and its
	// number group is one whitespace-free token, so anything else would be
	// swallowed by the (non-greedy) name group and re-imported as a different
	// card's name — worse than falling back to the name honestly.
	if setCodeRE.MatchString(r.Set) && r.CollectorNumber != "" &&
		!strings.ContainsFunc(r.CollectorNumber, unicode.IsSpace) {
		line += fmt.Sprintf(" (%s) %s", r.Set, r.CollectorNumber)
	}
	switch r.Finish {
	case "foil":
		line += " *F*"
	case "etched":
		line += " *E*"
	}
	return line
}

// boardOf maps a stored board onto the sections above. A board this writer has
// no header for folds into main rather than inventing one: textlist.go treats a
// line as a section header only when it knows the word, so an invented header
// would be read as a card line, dropped as unparseable, and every card under it
// filed under whichever board came before. The wrong board is recoverable by
// hand; a dropped card is not.
func boardOf(board string) string {
	for _, s := range sections {
		if board == s.board {
			return s.board
		}
	}
	return "main"
}

// mergedForText sums the rows this grammar cannot tell apart, so a holding
// split across rows comes back as one deck entry rather than several.
//
// Condition is the split that bites: two rows of one printing differing only
// by wear are distinct holdings in the canonical CSV and correctly so, but
// they render as two identical decklist lines, which import as two entries for
// the same card. Container is left out of the key because the caller passes
// one container's rows.
func mergedForText(rows []Row) []Row {
	type key struct{ name, set, number, finish, board string }
	idx := make(map[key]int, len(rows))
	var out []Row
	for _, r := range rows {
		k := key{r.Name, r.Set, r.CollectorNumber, r.Finish, boardOf(r.Board)}
		if i, ok := idx[k]; ok {
			out[i].Count += r.Count
			continue
		}
		// Cleared rather than kept from whichever row landed first: the
		// merged row can cover several conditions, and no writer downstream
		// should be able to read one of them as the answer for all.
		r.Condition = ""
		idx[k] = len(out)
		out = append(out, r)
	}
	return out
}
