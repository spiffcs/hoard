package export

import (
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"
	"io"
	"regexp"
	"strings"
	"unicode"
)

var sections = []struct{ board, header string }{
	{"main", "Deck"},
	{"commander", "Commander"},
	{"side", "Sideboard"},
	{"maybe", "Maybeboard"},
}

var setCodeRE = regexp.MustCompile(`^[A-Za-z0-9]+$`)

func WriteText(w io.Writer, rows []Row) error {
	byBoard := make(map[string][]Row, len(sections))
	for _, r := range Sorted(mergedForText(rows)) {
		b := boardOf(r.Board)
		byBoard[b] = append(byBoard[b], r)
	}

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

func textLine(r Row) string {
	line := fmt.Sprintf("%d %s", r.Count, r.Name)

	if setCodeRE.MatchString(r.Set) && r.CollectorNumber != "" &&
		!strings.ContainsFunc(r.CollectorNumber, unicode.IsSpace) {
		line += fmt.Sprintf(" (%s) %s", r.Set, r.CollectorNumber)
	}
	switch r.Finish {
	case finish.Foil:
		line += " *F*"
	case finish.Etched:
		line += " *E*"
	}
	return line
}

func boardOf(board string) string {
	for _, s := range sections {
		if board == s.board {
			return s.board
		}
	}
	return "main"
}

func mergedForText(rows []Row) []Row {
	type key struct {
		name, set, number string
		finish            finish.Finish
		board             string
	}
	idx := make(map[key]int, len(rows))
	var out []Row
	for _, r := range rows {
		k := key{r.Name, r.Set, r.CollectorNumber, r.Finish, boardOf(r.Board)}
		if i, ok := idx[k]; ok {
			out[i].Count += r.Count
			continue
		}

		r.Condition = ""
		idx[k] = len(out)
		out = append(out, r)
	}
	return out
}
