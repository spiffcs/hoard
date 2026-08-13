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
//
// The collector-number group excludes a leading "*" so that it cannot swallow
// the foil marker. "1 Bolt (2X2) 117 *F*" was always read correctly, but
// Archidekt writes the set with no number at all — "1x Atla Palani, Nest Tender
// (c19) *F*" — and there `(\S+)` took "*F*" as the collector number, losing the
// finish and sending Scryfall a collector number that does not exist. No
// collector number begins with "*".
var lineRE = regexp.MustCompile(`^(?:([Ss][Bb]):\s*)?(\d+)\s*[xX]?\s+(.+?)(?:\s+\(([A-Za-z0-9]+)\)(?:\s+([^\s*]\S*))?)?\s*(\*[EFef]\*)?\s*$`)

// Archidekt's text export appends two annotations that no other site writes and
// that no card name contains, so they are lifted off the line before lineRE ever
// sees it:
//
//	1x Arcane Signet (c21) [Rock/Dork,Artifact]
//	1x Austere Command (cmr) [Maybeboard{noDeck}{noPrice}]  ^Getting,#2ccce4^
//
// Stripping them is not cosmetic. lineRE's collector-number group is `(\S+)`,
// so "[Rock/Dork,Artifact]" was being read as a collector number and sent to
// Scryfall as one — a 400 that failed the *whole* request, taking all 99 other
// cards with it. Measured against a real export: the entire import died on the
// second line.
//
// The category block is kept rather than discarded, because it carries the
// board. Labels are colour-coded user annotations with no meaning to hoard, so
// those go.
var (
	archidektCategoriesRE = regexp.MustCompile(`\s*\[([^\]]*)\]`)
	archidektLabelRE      = regexp.MustCompile(`\s*\^[^^]*\^`)
)

// splitArchidektAnnotations lifts the category and label blocks off a line,
// returning the line without them and the categories it carried.
//
// noDeck reports Archidekt's {noDeck} modifier, which marks a card the deck
// does not actually contain — its maybeboard and its "considering" pile both
// carry it. That modifier outranks the category *name*, because a user's own
// category ("Maybe-mana{noDeck}{noPrice}" in the export this was built from)
// is not one of the three built-in names and would otherwise be read as part of
// the deck, inflating it with cards the owner deliberately set aside.
func splitArchidektAnnotations(line string) (rest string, categories []string, noDeck bool) {
	rest = archidektLabelRE.ReplaceAllString(line, "")
	for _, m := range archidektCategoriesRE.FindAllStringSubmatch(rest, -1) {
		for _, cat := range strings.Split(m[1], ",") {
			if cat = strings.TrimSpace(cat); cat != "" {
				categories = append(categories, cat)
			}
		}
	}
	if len(categories) == 0 {
		return strings.TrimSpace(rest), nil, false
	}
	rest = archidektCategoriesRE.ReplaceAllString(rest, "")
	for _, cat := range categories {
		if strings.Contains(strings.ToLower(cat), "{nodeck}") {
			noDeck = true
		}
	}
	return strings.TrimSpace(rest), categories, noDeck
}

// headerKey reduces a line to the form sectionHeaders is keyed by.
func headerKey(line string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(line), ":"))
}

// deckstatsCategory reports the category a Deckstats header line names.
//
// Deckstats writes its categories as comments — a real export opens
// "//Commander" and goes on with "//Mimeoplasm Targets", "//Mill", "//Lands",
// thirteen in all; the same deck exported without categories says "//Main". So
// a "//" prefix cannot mean "ignore this line" on its own. It did, and every
// Deckstats deck imported with its commander in the main deck.
//
// The marker is "//" with the name against it, no space. That is what
// distinguishes a category from a written comment, and it is the distinction
// the format itself draws: all thirteen headers in that export are "//Name",
// and a person writing a note types "// note". A file that breaks the
// convention loses only its sectioning, and its cards still import.
func deckstatsCategory(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "//")
	if !ok || rest == "" || rest[0] == ' ' || rest[0] == '\t' {
		return "", false
	}
	return rest, true
}

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
		if line == "" {
			continue
		}
		// A Deckstats category header. One that names a board selects it; one
		// of the owner's own ends the section before it, which is the whole
		// reason this is not just "skip unknown headers": leaving the previous
		// section standing filed an entire 100-card deck under Commander,
		// because "//Commander" was followed by twelve categories that named
		// no board and so never closed it.
		if cat, ok := deckstatsCategory(line); ok {
			if b, known := sectionHeaders[headerKey(cat)]; known {
				board = b
			} else {
				board = BoardMain
			}
			continue
		}
		// A bare, non-quantity line is a section header in every other format.
		if b, ok := sectionHeaders[headerKey(line)]; ok {
			board = b
			continue
		}
		if strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
			continue
		}

		line, categories, noDeck := splitArchidektAnnotations(line)
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
		// An Archidekt category beats both: it is the most specific thing the
		// line says about where the card lives. {noDeck} is checked first
		// because it is the only signal that survives a custom category name.
		switch {
		case noDeck:
			entry.Board = BoardMaybe
		case len(categories) > 0:
			if b, named := namedBoard(categories); named {
				entry.Board = b
			}
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
	case set != "":
		// Name plus set is one of Scryfall's identifier shapes, and keeping the
		// set is the difference between the printing the deck names and an
		// arbitrary one. Archidekt's text export makes this the common case
		// rather than an edge: every line carries "(c21)" and none carries a
		// collector number, so dropping the set here re-prints a whole deck.
		ident = scryfall.Identifier{Name: name, Set: strings.ToLower(set)}
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
		// Stripped here too: a paste into a binder is as likely to have come
		// from an Archidekt export as one into a deck, and the collector-number
		// misread that follows is not a board question.
		line, _, _ = splitArchidektAnnotations(line)
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
