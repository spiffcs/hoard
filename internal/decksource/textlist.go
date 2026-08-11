package decksource

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// lineRE matches common decklist lines exported by Moxfield, Archidekt, MTGGoldfish,
// etc.:
//
//	"2 Sol Ring", "1x Lightning Bolt", "1 Ulamog, the Infinite Gyre (UMA) 7",
//	"1 Sol Ring (C21) 1 *F*", "SB: 2 Duress" (MTGO .dek exports mark the
//	sideboard per line, not with a section header)
//
// Groups: optional SB marker, quantity, name, optional set code, optional
// collector number.
var lineRE = regexp.MustCompile(`^(?:([Ss][Bb]):\s*)?(\d+)\s*[xX]?\s+(.+?)(?:\s+\(([A-Za-z0-9]+)\)(?:\s+(\S+))?)?\s*(\*[EFef]\*)?\s*$`)

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

		entry, ok := parseLine(line)
		if !ok {
			// Skip and report rather than abort: one odd line — a URL, a
			// separator, an export quirk — used to refuse a whole 99-card
			// file. The caller says what was dropped; the error below stands
			// only when nothing at all parsed, where "no cards" is the truth.
			d.Skipped = append(d.Skipped, fmt.Sprintf("line %d: %s", lineNo, line))
			continue
		}
		// A per-line board marker (SB:) beats the section the line sits in.
		if entry.Board == "" {
			entry.Board = board
		}
		d.Entries = append(d.Entries, entry)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(d.Entries) == 0 {
		if len(d.Skipped) > 0 {
			return nil, fmt.Errorf("no cards found in decklist; %d lines could not be read (e.g. %s)",
				len(d.Skipped), d.Skipped[0])
		}
		return nil, fmt.Errorf("no cards found in decklist")
	}
	return d, nil
}

// parseLine reads one card line — "2 Sol Ring", "1x Bolt (2X2) 117 *F*" —
// into an Entry whose board is empty unless the line names one itself (an
// "SB:" prefix); the caller fills the section it is reading otherwise.
func parseLine(line string) (Entry, bool) {
	m := lineRE.FindStringSubmatch(line)
	if m == nil {
		return Entry{}, false
	}
	board := ""
	if m[1] != "" {
		board = BoardSide
	}
	qty, _ := strconv.Atoi(m[2])
	if qty < 1 {
		qty = 1
	}
	// Trim format characters as well as whitespace. A zero-width space is not
	// unicode.IsSpace, so "2 ​" survives the caller's TrimSpace and used
	// to parse as a card named "​" — which reaches Scryfall as a name
	// search. Found by FuzzParseLine. A line with no name is not a card.
	name := strings.TrimFunc(m[3], func(r rune) bool {
		return unicode.IsSpace(r) || unicode.Is(unicode.Cf, r)
	})
	if name == "" {
		return Entry{}, false
	}
	set, number := m[4], m[5]

	var ident scryfall.Identifier
	switch {
	case set != "" && number != "":
		ident = scryfall.Identifier{Set: strings.ToLower(set), CollectorNumber: number}
	default:
		ident = scryfall.Identifier{Name: name}
	}
	finish := "nonfoil"
	switch strings.ToUpper(m[6]) {
	case "*F*":
		finish = "foil"
	case "*E*":
		finish = "etched"
	}
	return Entry{Ident: ident, Name: name, Quantity: qty, Finish: finish, Board: board}, true
}

// ParseLoose reads a pasted card list destined for a binder: every parseable
// line becomes a board-main entry, section headers are ignored outright (a
// binder has no boards), and lines nothing can read are skipped and reported
// rather than failing the paste — a hand-typed list earns a tolerant reader,
// and the caller says what was dropped.
func ParseLoose(r io.Reader) (entries []Entry, skipped []string, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
			continue
		}
		if _, ok := sectionHeaders[strings.ToLower(strings.TrimRight(line, ":"))]; ok {
			continue
		}
		entry, ok := parseLine(line)
		if !ok {
			skipped = append(skipped, fmt.Sprintf("line %d: %s", lineNo, line))
			continue
		}
		entry.Board = BoardMain
		entries = append(entries, entry)
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	return entries, skipped, nil
}
