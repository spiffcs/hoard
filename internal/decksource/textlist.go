package decksource

import (
	"bufio"
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/spiffcs/hoard/internal/safetext"
	"github.com/spiffcs/hoard/internal/scryfall"
)

var sbPrefixRE = regexp.MustCompile(`^[Ss][Bb]:\s`)

var lineRE = regexp.MustCompile(`^(?:([Ss][Bb]):\s*)?(\d+)\s*[xX]?\s+(.+?)(?:\s+\(([A-Za-z0-9]+)\)(?:\s+([^\s*]\S*))?)?\s*(\*[EFef]\*)?\s*$`)

var (
	archidektCategoriesRE = regexp.MustCompile(`\s*\[([^\]]*)\]`)
	archidektLabelRE      = regexp.MustCompile(`\s*\^[^^]*\^`)
)

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

func headerKey(line string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(line), ":"))
}

func deckstatsCategory(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "//")
	if !ok || rest == "" || rest[0] == ' ' || rest[0] == '\t' {
		return "", false
	}
	return rest, true
}

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

func readLines(r io.Reader) ([]string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func usesBoardMarkers(lines []string) bool {
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if _, ok := deckstatsCategory(line); ok {
			return true
		}
		if _, ok := sectionHeaders[headerKey(line)]; ok {
			return true
		}
		if sbPrefixRE.MatchString(line) {
			return true
		}
		if _, categories, noDeck := splitArchidektAnnotations(line); noDeck {
			return true
		} else if _, named := namedBoard(categories); named {
			return true
		}
	}
	return false
}

func ParseText(name, sourceID, sourceURL, provider string, r io.Reader) (*Deck, error) {
	if provider == "" {
		provider = "text"
	}
	if sourceID == "" {
		sourceID = strings.ToLower(strings.TrimSpace(name))
	}
	d := &Deck{Name: name, Source: provider, SourceID: sourceID, SourceURL: sourceURL}

	lines, err := readLines(r)
	if err != nil {
		return nil, err
	}
	blankStartsSideboard := !usesBoardMarkers(lines)

	board := BoardMain
	for i, raw := range lines {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" {
			if blankStartsSideboard && len(d.Entries) > 0 {
				board = BoardSide
			}
			continue
		}

		if cat, ok := deckstatsCategory(line); ok {
			if b, known := sectionHeaders[headerKey(cat)]; known {
				board = b
			} else {
				board = BoardMain
			}
			continue
		}

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

			d.Skipped = append(d.Skipped, fmt.Sprintf("line %d: %s", lineNo, line))
			continue
		}

		if entry.Board == "" {
			entry.Board = board
		}

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

	d.clean()
	if len(d.Entries) == 0 {
		if len(d.Skipped) > 0 {
			return nil, fmt.Errorf("no cards found in decklist; %d lines could not be read (e.g. %s)",
				len(d.Skipped), d.Skipped[0])
		}
		return nil, fmt.Errorf("no cards found in decklist")
	}
	return d, nil
}

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

		ident = scryfall.Identifier{Name: name, Set: strings.ToLower(set)}
	default:
		ident = scryfall.Identifier{Name: name}
	}
	fin := finish.Nonfoil
	switch strings.ToUpper(m[6]) {
	case "*F*":
		fin = finish.Foil
	case "*E*":
		fin = finish.Etched
	}
	return Entry{Ident: ident, Name: name, Quantity: qty, Finish: fin, Board: board}, true
}

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

	cleanEntries(entries)
	for i, s := range skipped {
		skipped[i] = safetext.Clean(s)
	}
	return entries, skipped, nil
}
