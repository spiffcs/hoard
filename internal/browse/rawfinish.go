package browse

import "strings"

const rawFinishTerm = "finish:nonfoil"

func rawTermIndex(text string) int {
	for at := 0; at+len(rawFinishTerm) <= len(text); at++ {
		if !strings.EqualFold(text[at:at+len(rawFinishTerm)], rawFinishTerm) {
			continue
		}
		end := at + len(rawFinishTerm)
		if (at == 0 || text[at-1] == ' ') && (end == len(text) || text[end] == ' ') {
			return at
		}
	}
	return -1
}

func withoutRawTerm(text string, at int) string {
	left := strings.TrimRight(text[:at], " ")
	right := strings.TrimLeft(text[at+len(rawFinishTerm):], " ")
	switch {
	case left == "":
		return right
	case right == "":
		return left
	}
	return left + " " + right
}

func withRawTerm(text string) string {
	if strings.TrimSpace(text) == "" {
		return rawFinishTerm
	}
	return strings.TrimRight(text, " ") + " " + rawFinishTerm
}

func (m *Model) toggleRawOnly() {
	if at := rawTermIndex(m.filterText); at >= 0 {
		m.filterText = withoutRawTerm(m.filterText, at)
		m.setFilter(m.filterText)
		m.status, m.statusErr = "raw only off", false
		return
	}
	m.filterText = withRawTerm(m.filterText)
	m.setFilter(m.filterText)
	m.status, m.statusErr = "raw only · hiding foil and etched", false
}
