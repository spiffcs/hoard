package decksource

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// lineRE matches common decklist lines exported by Moxfield, Archidekt, MTGGoldfish,
// etc.:
//
//	"2 Sol Ring", "1x Lightning Bolt", "1 Ulamog, the Infinite Gyre (UMA) 7",
//	"1 Sol Ring (C21) 1 *F*"
//
// Groups: quantity, name, optional set code, optional collector number.
var lineRE = regexp.MustCompile(`^(\d+)\s*[xX]?\s+(.+?)(?:\s+\(([A-Za-z0-9]+)\)(?:\s+(\S+))?)?\s*(\*[EFef]\*)?\s*$`)

// sectionHeaders maps a lowercased header line to a board.
var sectionHeaders = map[string]string{
	"commander":  BoardCommander,
	"commanders": BoardCommander,
	"sideboard":  BoardSide,
	"maybeboard": BoardMaybe,
	"maybe":      BoardMaybe,
	"deck":       BoardMain,
	"mainboard":  BoardMain,
	"main":       BoardMain,
	"companion":  BoardSide,
}

// ParseText parses a pasted/exported decklist into a normalized Deck. Card names
// are resolved later by the caller via Scryfall's collection endpoint. sourceID
// should be a stable id for this deck (e.g. the Moxfield public id if known, else
// the deck name).
func ParseText(name, sourceID, sourceURL, provider string, r io.Reader) (*Deck, error) {
	if provider == "" {
		provider = "text"
	}
	if sourceID == "" {
		sourceID = strings.ToLower(strings.TrimSpace(name))
	}
	d := &Deck{Name: name, Source: provider, SourceID: sourceID, SourceURL: sourceURL}

	board := BoardMain
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
			continue
		}
		// A bare, non-quantity line is treated as a section header.
		if b, ok := sectionHeaders[strings.ToLower(strings.TrimRight(line, ":"))]; ok {
			board = b
			continue
		}

		m := lineRE.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("line %d: cannot parse decklist entry %q", lineNo, line)
		}
		qty, _ := strconv.Atoi(m[1])
		if qty < 1 {
			qty = 1
		}
		name := strings.TrimSpace(m[2])
		set, number := m[3], m[4]

		var ident scryfall.Identifier
		switch {
		case set != "" && number != "":
			ident = scryfall.Identifier{Set: strings.ToLower(set), CollectorNumber: number}
		default:
			ident = scryfall.Identifier{Name: name}
		}
		finish := "normal"
		switch strings.ToUpper(m[5]) {
		case "*F*":
			finish = "foil"
		case "*E*":
			finish = "etched"
		}

		d.Entries = append(d.Entries, Entry{Ident: ident, Quantity: qty, Finish: finish, Board: board})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(d.Entries) == 0 {
		return nil, fmt.Errorf("no cards found in decklist")
	}
	return d, nil
}
