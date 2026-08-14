package tui

import (
	"cmp"
	"context"
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
)

type scanMatch int

const (
	scanMatchNone scanMatch = iota
	scanMatchNumberAmbiguous
	scanMatchNumberTail
	scanMatchYearOnly
	scanMatchYearAndMarks
	scanMatchYearAndFrame
	scanMatchNumberOnly
	scanMatchSinglePrint
	scanMatchNumberAndYear
	scanMatchSetAndNumber
	scanMatchSetNumberAndLang
)

func numberVerified(r scanMatch) bool {
	return r == scanMatchSetNumberAndLang || r == scanMatchSetAndNumber ||
		r == scanMatchNumberAndYear || r == scanMatchNumberOnly
}

func corroboratedPrinting(r scanMatch) bool {
	return r == scanMatchSetNumberAndLang || r == scanMatchSetAndNumber ||
		r == scanMatchNumberAndYear
}

func printingPinned(r scanMatch) bool {

	return numberVerified(r) || r == scanMatchNumberTail ||
		r == scanMatchYearAndFrame
}

func (m scanMatch) String() string {
	switch m {
	case scanMatchNumberAmbiguous:
		return "number-ambiguous"
	case scanMatchNumberTail:
		return "number-tail"
	case scanMatchYearAndMarks:
		return "year+marks"
	case scanMatchYearAndFrame:
		return "year+frame"
	case scanMatchYearOnly:
		return "year-only"
	case scanMatchNumberOnly:
		return "number-only"
	case scanMatchNumberAndYear:
		return "number+year"
	case scanMatchSinglePrint:
		return "single-print"
	case scanMatchSetAndNumber:
		return "set+number"
	case scanMatchSetNumberAndLang:
		return "set+number+lang"
	default:
		return "none"
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func matchDesc(m cardname.Match) string {
	if m.Exact {
		return "exact"
	}
	return fmt.Sprintf("%d%%", int(m.Similarity*100))
}

func numberSourceSuffix(src string) string {
	if src == "" {
		return ""
	}
	return "(" + src + ")"
}

func borderSuffix(it queueItem) string {
	if it.raw.BorderColor == "" {
		return ""
	}
	s := " border=" + it.raw.BorderColor
	if it.raw.BorderSource != "" {
		s += "(" + it.raw.BorderSource + ")"
	}
	if !it.borderFiltered {

		s += " unused"
	}
	return s
}

func finishSuffix(it queueItem) string {
	c := it.raw
	if it.finishHint == "" && c.FinishHint == "" && c.SparkleScore == nil {
		return ""
	}

	s := " finish=" + orDash(it.finishHint)
	if c.FinishSource != "" {
		s += "(" + c.FinishSource + ")"
	}
	if it.finishHint != c.FinishHint {
		s += fmt.Sprintf(" read=%s", orDash(c.FinishHint))
	}
	if c.SparkleScore != nil {
		s += fmt.Sprintf(" sparkle=%.3f", *c.SparkleScore)
		if c.SparkleOffsetU != nil && c.SparkleOffsetV != nil {
			s += fmt.Sprintf("@%+.4f,%+.4f", *c.SparkleOffsetU, *c.SparkleOffsetV)
		}

		if c.SparkleContrast != nil {
			s += fmt.Sprintf("/%.4f", *c.SparkleContrast)
		}
	}

	if c.SparkleChromaScore != nil {
		s += fmt.Sprintf(" chroma=%.3f", *c.SparkleChromaScore)
		if c.SparkleChromaContrast != nil {
			s += fmt.Sprintf("/%.4f", *c.SparkleChromaContrast)
		}
	}
	return s
}

func siblingSuffix(it queueItem) string {
	var s string
	if it.siblings > 1 {
		s += fmt.Sprintf(" siblings=%d", it.siblings)
	}
	if it.fromNudge {
		s += " nudged"
	}
	if it.fromMoved {
		s += " moved"
	}
	if it.fromReplaced {
		s += " replaced"
	}

	if it.faceDelta != nil {
		s += fmt.Sprintf(" face=%.1f", *it.faceDelta)
	}
	if it.numberOverridden {
		s += " number-overridden"
	}
	return s
}

type queueItem struct {
	id        int
	raw       scan.Card
	siblings  int
	fromNudge bool

	fromMoved bool

	fromReplaced bool
	faceDelta    *float64
	canonical    string
	ocrLine      string
	lineIdx      int
	match        cardname.Match
	prints       []scryfall.Card
	rank         scanMatch

	finishHint string

	captureSeq int

	queuedAt time.Time
	dup      bool
	note     string
	errText  string

	numberOverridden bool

	viaBlock bool

	borderFiltered bool
}

type resolveDoneMsg struct {
	gen  int
	item queueItem

	supplementary bool

	nameDur   time.Duration
	printsDur time.Duration
}

var typeLineWords = map[string]bool{
	"legendary": true, "creature": true, "enchantment": true,
	"planeswalker": true, "sorcery": true, "instant": true, "artifact": true,
	"battle": true, "tribal": true, "snow": true, "basic": true, "token": true,
}

func fallbackLineSuspect(line string) bool {
	for _, w := range strings.Fields(strings.ToLower(line)) {
		clean := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' {
				return r
			}
			return -1
		}, w)
		if typeLineWords[clean] {
			return true
		}
	}
	return false
}

var collectorish = regexp.MustCompile(`\d+\s*/\s*\d+`)

func titleLikely(line string) bool {
	s := strings.TrimSpace(line)
	if s == "" {
		return false
	}
	if r := []rune(s)[0]; !unicode.IsUpper(r) {
		return false
	}
	var letters, digits int
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			letters++
		case unicode.IsDigit(r):
			digits++
		}
	}
	if letters < 4 || digits > letters {
		return false
	}
	return !strings.ContainsAny(s, "™©®") && !collectorish.MatchString(s)
}

var typeLineRE = regexp.MustCompile(`(?i)^(?:(?:legendary|basic|snow|world|tribal|kindred|token|artifact|enchantment|creature|instant|sorcery|land|planeswalker|battle)s?\s+)*` +
	`(?:creature|artifact|enchantment|instant|sorcery|land|planeswalker|battle)s?\s*[-–—~=]`)

func typeLineShape(line string) bool {
	return typeLineRE.MatchString(strings.TrimSpace(line))
}

var abilityWords = map[string]bool{
	"haste": true, "flying": true, "lifelink": true, "trample": true,
	"vigilance": true, "deathtouch": true, "menace": true, "reach": true,
	"defender": true, "hexproof": true, "indestructible": true, "flash": true,
	"rebound": true, "ward": true, "prowess": true, "first": true,
	"double": true, "strike": true,
}

func keywordLine(line string) bool {
	fields := strings.Fields(strings.ToLower(line))
	if len(fields) == 0 || len(fields) > 2 {
		return false
	}
	for _, f := range fields {
		if !abilityWords[strings.Trim(f, ".,;")] {
			return false
		}
	}
	return true
}

func hasCollectorBlock(c scan.Card) bool {
	if c.SetCode != "" && c.CollectorNumber != "" && c.NumberSource != "copyright" {
		return true
	}
	return slices.ContainsFunc(c.CollectorAlts, func(a scan.CollectorAlt) bool {
		return a.Set != "" && a.Number != "" && a.Source != "copyright"
	})
}

func hasPrintingEvidence(c scan.Card) bool {
	if c.SetCode != "" || c.CollectorNumber != "" || c.CopyrightYear != 0 {
		return true
	}
	return slices.ContainsFunc(c.CollectorAlts, func(a scan.CollectorAlt) bool {
		return a.Set != "" || a.Number != "" || a.Year != 0
	})
}

type BlockSearcher interface {
	PrintBySetNumberLang(ctx context.Context, set, number, lang string) (*scryfall.Card, error)
}

func resolveByBlock(ctx context.Context, s Searcher, c scan.Card) (*scryfall.Card, scan.CollectorAlt) {
	byBlock, ok := s.(BlockSearcher)
	if !ok {
		return nil, scan.CollectorAlt{}
	}
	blocks := append([]scan.CollectorAlt{
		{Number: c.CollectorNumber, Set: c.SetCode, Finish: c.FinishHint,
			Source: c.NumberSource, Language: c.Language}},
		c.CollectorAlts...)
	for _, b := range blocks {
		if b.Set == "" || b.Number == "" || b.Source == "copyright" {
			continue
		}
		card, err := byBlock.PrintBySetNumberLang(ctx, b.Set, b.Number, b.Language)
		if err == nil && card != nil {
			return card, b
		}
	}
	return nil, scan.CollectorAlt{}
}

type localOnlySearcher interface {
	NamedFuzzyLocal(ctx context.Context, text string) (*scryfall.Card, cardname.Match, error)
}

const nudgeDelay = 5500 * time.Millisecond

const nameTimeout = 250 * time.Millisecond

func searchLine(ctx context.Context, s Searcher, local localOnlySearcher,
	hasLocal bool, line string, i int,
) (*scryfall.Card, cardname.Match, error) {
	if !hasLocal {
		return s.NamedFuzzy(ctx, line)
	}
	card, m, err := local.NamedFuzzyLocal(ctx, line)
	if err == nil && card != nil {
		return card, m, nil
	}
	if i > 0 || !titleLikely(line) {
		return card, m, err
	}
	rctx, cancel := context.WithTimeout(ctx, nameTimeout)
	defer cancel()
	rc, rm, rerr := s.NamedFuzzy(rctx, line)

	if rerr != nil && rctx.Err() != nil {
		return card, m, nil
	}
	return rc, rm, rerr
}

type prefixNominee struct {
	name  string
	match cardname.Match
}

func resolveName(ctx context.Context, s Searcher, lines []string) (canonical, ocr string, lineIdx int, match cardname.Match, nominee *prefixNominee, err error) {
	var firstErr error
	local, hasLocal := s.(localOnlySearcher)
	for i, line := range lines {
		if i >= maxFuzzyTries {
			break
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if i > 0 && (fallbackLineSuspect(line) || !titleLikely(line) || keywordLine(line)) {
			continue
		}

		if typeLineShape(line) {
			continue
		}

		card, m, ferr := searchLine(ctx, s, local, hasLocal, line, i)
		if ferr != nil {
			if firstErr == nil {
				firstErr = ferr
			}
			continue
		}
		if card == nil {
			continue
		}

		if m.PrefixOnly {
			if i == 0 && nominee == nil {
				nominee = &prefixNominee{name: card.Name, match: m}
			}
			continue
		}
		if cardname.Plausible(line, card.Name) {
			return card.Name, line, i, m, nominee, nil
		}
	}
	var top string
	if len(lines) > 0 {
		top = lines[0]
	}
	return "", top, 0, cardname.Match{}, nominee, firstErr
}

func (m model) resolveCardCmd(id int, c scan.Card, siblings int) tea.Cmd {
	gen := m.resolveGen
	ctx, s := m.ctx, m.searcher
	fromNudge := m.lastScanNudged
	fromMoved := m.lastScanMoved
	fromReplaced := m.lastScanReplaced
	faceDelta := m.lastScanFaceDelta
	captureSeq := m.captureSeq

	priorSets := recentSets(m.recent)
	return func() tea.Msg {
		it := queueItem{id: id, raw: c, siblings: siblings, fromNudge: fromNudge,
			fromMoved: fromMoved, fromReplaced: fromReplaced, faceDelta: faceDelta,
			captureSeq: captureSeq}
		tName := time.Now()
		canonical, ocr, idx, match, nominee, err := resolveName(ctx, s, c.Lines())
		nameDur := time.Since(tName)
		var printsDur time.Duration
		it.canonical, it.ocrLine, it.lineIdx, it.match = canonical, ocr, idx, match
		if err != nil {
			it.errText = err.Error()
		}

		if canonical == "" {
			if card, blk := resolveByBlock(ctx, s, c); card != nil {
				it.canonical, it.match = card.Name, cardname.Match{Exact: true}
				it.lineIdx = 0
				it.prints, it.rank = []scryfall.Card{*card}, scanMatchSetAndNumber
				it.raw.SetCode, it.raw.CollectorNumber = blk.Set, blk.Number
				it.finishHint = blk.Finish
				it.viaBlock = true
				return resolveDoneMsg{gen: gen, item: it,
					nameDur: nameDur, printsDur: printsDur}
			}
		}
		if canonical != "" {
			tPrints := time.Now()
			prints, perr := s.SearchPrints(ctx, canonical)
			printsDur = time.Since(tPrints)
			if perr != nil {
				it.errText = perr.Error()
			} else {
				rankPrints(&it, c, prints, match, priorSets)
			}
		}

		if it.canonical == "" && nominee != nil && bandNumberRead(c) {
			tPrints := time.Now()
			prints, perr := s.SearchPrints(ctx, nominee.name)
			printsDur += time.Since(tPrints)
			if perr == nil && len(prints) > 0 {
				trial := it
				trial.canonical, trial.match = nominee.name, nominee.match
				rankPrints(&trial, c, prints, nominee.match, priorSets)
				if numberVerified(trial.rank) {
					it = trial
				}
			}
		}
		return resolveDoneMsg{gen: gen, item: it, nameDur: nameDur, printsDur: printsDur}
	}
}

func bandNumberRead(c scan.Card) bool {
	if c.CollectorNumber != "" && c.NumberSource != "copyright" {
		return true
	}
	return slices.ContainsFunc(c.CollectorAlts, func(a scan.CollectorAlt) bool {
		return a.Number != ""
	})
}

func rankPrints(it *queueItem, c scan.Card, prints []scryfall.Card, match cardname.Match, priorSets []string) {
	{

		cands := append([]scan.CollectorAlt{
			{Number: c.CollectorNumber, Set: c.SetCode, Finish: c.FinishHint,
				Source: c.NumberSource, Year: c.CopyrightYear}},
			c.CollectorAlts...)

		trusted := slices.ContainsFunc(cands, func(cd scan.CollectorAlt) bool {
			return cd.Number != "" && cd.Source != "copyright"
		})

		if !trusted || match.Exact {
			cands = append(cands, scan.CollectorAlt{
				Finish: c.FinishHint, Year: c.CopyrightYear})
		}
		it.prints, it.rank, it.finishHint = prints, scanMatchNone, c.FinishHint

		frame, priors := c.FrameStyle, priorSets
		if !match.Exact && match.Similarity < frameNameFloor {
			frame, priors = "", nil
		}
		for _, cd := range cands {

			ranked, r := rankByScanStrength(
				prints, cd.Set, cd.Number, cmp.Or(cd.Year, c.CopyrightYear),
				c.BorderColor, c.FinishHint,
				cmp.Or(cd.Language, c.Language), frame, priors)
			if r > it.rank {
				it.prints, it.rank = ranked, r

				it.raw.SetCode, it.raw.CollectorNumber = cd.Set, cd.Number
				it.finishHint = cd.Finish
			}
		}

		it.numberOverridden = it.raw.CollectorNumber == "" &&
			slices.ContainsFunc(cands, func(cd scan.CollectorAlt) bool {
				return cd.Number != ""
			})
	}

	if !printingPinned(it.rank) {
		it.prints, it.borderFiltered = applyBorderEvidence(
			it.prints, c.BorderColor, c.CopyrightYear)
	}
}

const autoCommitOCRConfidence = 0.8

func printingUnverified(it queueItem) (short bool, note string) {
	if len(it.prints) == 0 {
		return false, ""
	}
	switch it.rank {
	case scanMatchSetNumberAndLang, scanMatchSetAndNumber, scanMatchNumberAndYear,
		scanMatchNumberOnly, scanMatchSinglePrint,

		scanMatchNumberTail:
		return false, ""
	case scanMatchYearOnly, scanMatchYearAndMarks, scanMatchYearAndFrame:

		if y := it.raw.CopyrightYear; y > 0 && len(it.prints) > 0 &&
			!strings.HasPrefix(it.prints[0].ReleasedAt, fmt.Sprintf("%d", y)) {
			return true, fmt.Sprintf(
				"printing unverified: %d printings, and the front one is not from %d",
				len(it.prints), y)
		}
		return false, ""
	case scanMatchNumberAmbiguous:

		if y := it.raw.CopyrightYear; y > 0 &&
			!strings.HasPrefix(it.prints[0].ReleasedAt, fmt.Sprintf("%d", y)) {
			return true, fmt.Sprintf(
				"printing unverified: %d printings, and the front one is not from %d",
				len(it.prints), y)
		}
		return false, ""
	default:
		return true, fmt.Sprintf("printing unverified: %d printings", len(it.prints))
	}
}

func verdict(it queueItem) (auto bool, fin finish.Finish, note string) {
	if it.errText != "" {
		return false, finish.Finish{}, "lookup failed: " + it.errText
	}
	if it.canonical == "" {
		if it.ocrLine == "" {
			return false, finish.Finish{}, "nothing readable"
		}
		return false, finish.Finish{}, fmt.Sprintf("couldn't identify %q", it.ocrLine)
	}
	if len(it.prints) == 0 {
		return false, finish.Finish{}, "no printings found"
	}
	if short, note := printingUnverified(it); short {
		return false, finish.Finish{}, note
	}

	if it.lineIdx != 0 && !numberVerified(it.rank) {
		return false, finish.Finish{}, "matched a fallback OCR line; check it's the right card"
	}

	if !corroboratedPrinting(it.rank) {
		if !it.match.Exact && it.match.Similarity < cardname.AutoCommitSimilarity {
			return false, finish.Finish{}, fmt.Sprintf("uncertain name match (%d%%)", int(it.match.Similarity*100))
		}
	}

	if !numberVerified(it.rank) {
		if c := it.raw.Confidence; !it.match.Exact && c > 0 && c < autoCommitOCRConfidence {
			return false, finish.Finish{}, fmt.Sprintf("low OCR confidence (%d%%)", int(c*100))
		}
	}

	fin, _ = finishFromEvidence(it.prints[0], it.finishHint)
	return true, fin, ""
}

func finishFromEvidence(card scryfall.Card, hint string) (fin finish.Finish, evidenced bool) {
	finishes := finishOptions(card)
	if len(finishes) == 1 {
		return finishes[0], true
	}
	if hinted, err := finish.Parse(hint); err == nil && slices.Contains(finishes, hinted) {
		return hinted, true
	}
	return finish.Nonfoil, false
}

func collapseVariants(cards []scryfall.Card) ([]scryfall.Card, bool) {
	base := func(c scryfall.Card) string {
		return strings.ToLower(c.Set) + "/" + scryfall.BaseNumber(c.CollectorNumber)
	}
	for _, c := range cards[1:] {
		if base(c) != base(cards[0]) {
			return cards, false
		}
	}
	ranked := make([]scryfall.Card, 0, len(cards))
	for _, c := range cards {
		if c.CollectorNumber == scryfall.BaseNumber(c.CollectorNumber) {
			ranked = append(ranked, c)
		}
	}
	for _, c := range cards {
		if c.CollectorNumber != scryfall.BaseNumber(c.CollectorNumber) {
			ranked = append(ranked, c)
		}
	}
	return ranked, true
}

func soleIndexInYear(cards []scryfall.Card, idxs []int, year int) int {
	prefix := fmt.Sprintf("%d", year)
	found := -1
	for _, i := range idxs {
		if !strings.HasPrefix(cards[i].ReleasedAt, prefix) {
			continue
		}
		if found >= 0 {
			return -1
		}
		found = i
	}
	return found
}

func borderRulesOut(c scryfall.Card, border string) bool {
	if border == "" {
		return false
	}

	switch strings.ToLower(border) {
	case "black":
		return isLightBorder(c.BorderColor)
	case "white":
		return strings.EqualFold(c.BorderColor, "black")
	default:
		return false
	}
}

func isLightBorder(color string) bool {
	switch strings.ToLower(color) {
	case "white", "gold", "silver":
		return true
	}
	return false
}

func finishRulesOut(c scryfall.Card, hint string) bool {
	if hint == "" {
		return false
	}
	hinted, err := finish.Parse(hint)
	if err != nil {
		return true
	}
	return !slices.Contains(finishOptions(c), hinted)
}

func markingsAgree(c scryfall.Card, border, hint string) bool {
	if border != "" && !strings.EqualFold(c.BorderColor, border) {
		return false
	}
	if hint != "" && finishRulesOut(c, hint) {
		return false
	}
	return true
}

func applyBorderEvidence(prints []scryfall.Card, border string, year int) ([]scryfall.Card, bool) {
	if border == "" || len(prints) < 2 {
		return prints, false
	}

	yearPrefix := fmt.Sprintf("%d", year)
	var both, borderOnly, rest []scryfall.Card
	ruledOut := func(c scryfall.Card) bool { return borderRulesOut(c, border) }
	for _, c := range prints {
		switch {
		case ruledOut(c):
			rest = append(rest, c)
		case year > 0 && strings.HasPrefix(c.ReleasedAt, yearPrefix) &&
			strings.EqualFold(c.BorderColor, border):
			both = append(both, c)
		default:
			borderOnly = append(borderOnly, c)
		}
	}

	if len(rest) == len(prints) || (len(rest) == 0 && len(both) == 0) {
		return prints, false
	}
	ranked := make([]scryfall.Card, 0, len(prints))
	ranked = append(ranked, both...)
	ranked = append(ranked, borderOnly...)
	return append(ranked, rest...), true
}

func allIndexes(n int) []int {
	idxs := make([]int, n)
	for i := range idxs {
		idxs[i] = i
	}
	return idxs
}

func moveToFront(cards []scryfall.Card, i int) []scryfall.Card {
	ranked := make([]scryfall.Card, 0, len(cards))
	ranked = append(ranked, cards[i])
	ranked = append(ranked, cards[:i]...)
	return append(ranked, cards[i+1:]...)
}

func numberMatches(cards []scryfall.Card, set, number, lang string) []int {
	var out []int
	for i, c := range cards {
		switch {
		case strings.EqualFold(c.CollectorNumber, number):
			out = append(out, i)
		case set != "" && lang != "" &&
			strings.EqualFold(c.Set, set) && strings.EqualFold(c.Lang, lang) &&
			strings.EqualFold(scryfall.BaseNumber(c.CollectorNumber), number):
			out = append(out, i)
		}
	}
	return out
}

const numberTailMatchMinDigits = 2

func numberTailMatches(cards []scryfall.Card, number string) []int {
	if len(number) < numberTailMatchMinDigits || !isDigits(number) {
		return nil
	}
	var out []int
	for i, c := range cards {
		base := scryfall.BaseNumber(c.CollectorNumber)

		if len(base) <= len(number) || !isDigits(base) {
			continue
		}
		if strings.HasSuffix(base, number) {
			out = append(out, i)
		}
	}
	return out
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func langSaysSo(c scryfall.Card, lang string) bool {
	return lang != "" && c.Lang != "" && strings.EqualFold(c.Lang, lang)
}

func rankByScanStrength(cards []scryfall.Card, set, number string, year int,
	border, finish, lang, frame string, priorSets []string,
) ([]scryfall.Card, scanMatch) {
	if len(cards) == 0 {
		return cards, scanMatchNone
	}
	if number == "" {
		return rankWithoutNumber(cards, year, border, finish, frame, priorSets, true)
	}
	matchIdxs := numberMatches(cards, set, number, lang)
	best, exactSet, langAgrees := -1, false, false
	for _, i := range matchIdxs {
		c := cards[i]
		setMatches := set != "" && strings.EqualFold(c.Set, set)

		if setMatches && langSaysSo(c, lang) {
			best, exactSet, langAgrees = i, true, true
			break
		}
		if setMatches && !exactSet {
			best, exactSet = i, true
			continue
		}
		if best < 0 {
			best = i
		}
	}
	if best < 0 {

		if tails := numberTailMatches(cards, number); len(tails) == 1 {
			if c := cards[tails[0]]; year <= 0 ||
				strings.HasPrefix(c.ReleasedAt, fmt.Sprintf("%d", year)) {
				return moveToFront(cards, tails[0]), scanMatchNumberTail
			}
		}

		return rankWithoutNumber(cards, year, border, finish, frame, priorSets, false)
	}
	return rankMatchedNumber(cards, matchIdxs, best, exactSet, langAgrees, year, border, finish)
}

func rankWithoutNumber(cards []scryfall.Card, year int, border, finish, frame string,
	priorSets []string, solePrintCounts bool,
) ([]scryfall.Card, scanMatch) {
	if solePrintCounts {
		if len(cards) == 1 {
			return cards, scanMatchSinglePrint
		}
		if ranked, ok := collapseVariants(cards); ok {
			return ranked, scanMatchSinglePrint
		}
	}

	if year > 0 {
		if i := soleIndexInYear(cards, allIndexes(len(cards)), year); i >= 0 {
			return moveToFront(cards, i), scanMatchYearOnly
		}

		if border != "" || finish != "" {
			kept := make([]int, 0, len(cards))
			for i, c := range cards {
				if borderRulesOut(c, border) || finishRulesOut(c, finish) {
					continue
				}
				kept = append(kept, i)
			}

			if len(kept) > 0 && len(kept) < len(cards) {
				if i := soleIndexInYear(cards, kept, year); i >= 0 &&
					markingsAgree(cards[i], border, finish) {
					return moveToFront(cards, i), scanMatchYearAndMarks
				}
			}
		}
	}

	if frame != "" {
		ruledOut := 0
		var agreed []int
		prefix := fmt.Sprintf("%d", year)
		for i, c := range cards {
			if frameRulesOut(c, frame) {
				ruledOut++
				continue
			}
			if frameAgrees(c, frame) &&
				(year <= 0 || strings.HasPrefix(c.ReleasedAt, prefix)) {
				agreed = append(agreed, i)
			}
		}

		if ruledOut > 0 && ruledOut < len(cards) {
			if len(agreed) == 1 {
				return moveToFront(cards, agreed[0]), scanMatchYearAndFrame
			}

			if len(agreed) > 1 && len(priorSets) > 0 {
				var inPrior []int
				for _, i := range agreed {
					if slices.ContainsFunc(priorSets, func(s string) bool {
						return strings.EqualFold(s, cards[i].Set)
					}) {
						inPrior = append(inPrior, i)
					}
				}
				if len(inPrior) == 1 {
					return moveToFront(cards, inPrior[0]), scanMatchYearAndFrame
				}
			}
		}
	}
	return cards, scanMatchNone
}

func frameRulesOut(c scryfall.Card, frame string) bool {
	if frame != "retro" {
		return false
	}
	switch c.Frame {
	case "2003", "2015", "future":
		return true
	}
	return false
}

func frameAgrees(c scryfall.Card, frame string) bool {
	return frame == "retro" && (c.Frame == "1993" || c.Frame == "1997")
}

func recentSets(recent []recentCommit) []string {
	var out []string
	for _, rc := range recent {
		if rc.set == "" || slices.ContainsFunc(out, func(s string) bool {
			return strings.EqualFold(s, rc.set)
		}) {
			continue
		}
		out = append(out, rc.set)
	}
	return out
}

func rankMatchedNumber(cards []scryfall.Card, matchIdxs []int, best int,
	exactSet, langAgrees bool, year int, border, finish string,
) ([]scryfall.Card, scanMatch) {
	yearPinned := false
	if !exactSet && len(matchIdxs) > 1 && year > 0 {
		if inYear := soleIndexInYear(cards, matchIdxs, year); inYear >= 0 {
			best, yearPinned = inYear, true
		}
	}

	markPinned := false
	if !exactSet && !yearPinned && len(matchIdxs) > 1 && (border != "" || finish != "") {
		kept := make([]int, 0, len(matchIdxs))
		for _, i := range matchIdxs {
			if borderRulesOut(cards[i], border) || finishRulesOut(cards[i], finish) {
				continue
			}
			kept = append(kept, i)
		}

		if len(kept) == 1 && markingsAgree(cards[kept[0]], border, finish) {
			best, markPinned = kept[0], true
		}
	}

	yearAgrees := year > 0 && strings.HasPrefix(cards[best].ReleasedAt, fmt.Sprintf("%d", year))
	ranked := moveToFront(cards, best)
	switch {
	case exactSet && langAgrees:
		return ranked, scanMatchSetNumberAndLang
	case exactSet:
		return ranked, scanMatchSetAndNumber
	case (len(matchIdxs) == 1 || yearPinned || markPinned) && yearAgrees:
		return ranked, scanMatchNumberAndYear
	case len(matchIdxs) == 1 || yearPinned || markPinned:
		return ranked, scanMatchNumberOnly
	default:
		return ranked, scanMatchNumberAmbiguous
	}
}

const (
	dupWindow = 10 * time.Second
	dupKeep   = 10
)

const sameCardFloor = 3 * time.Second

type pendingDup struct {
	it     queueItem
	finish finish.Finish
	at     time.Time

	offered bool
}

const pendingDupWindow = 30 * time.Second

func clearUnofferedPending(p *pendingDup) *pendingDup {
	if p == nil || !p.offered {
		return nil
	}
	return p
}

type recentCommit struct {
	scryfallID string
	finish     finish.Finish
	captureSeq int
	at         time.Time

	name            string
	set             string
	collectorNumber string
	releaseYear     int

	finishGuessed bool

	printingGuessed bool
}

func dupCapture(recent []recentCommit, id string, now time.Time) (prior recentCommit, since time.Duration, dup bool) {
	for i := len(recent) - 1; i >= 0; i-- {
		rc := recent[i]
		if rc.scryfallID == id && now.Sub(rc.at) <= dupWindow {
			return rc, now.Sub(rc.at), true
		}
	}
	return recentCommit{}, 0, false
}

func dupCaptureByName(recent []recentCommit, name string, now time.Time) (prior recentCommit, since time.Duration, dup bool) {
	for i := len(recent) - 1; i >= 0; i-- {
		rc := recent[i]
		if rc.name == name && now.Sub(rc.at) <= dupWindow {
			return rc, now.Sub(rc.at), true
		}
	}
	return recentCommit{}, 0, false
}

func recordCommit(recent []recentCommit, card scryfall.Card, fin finish.Finish, captureSeq int,
	now time.Time, finishGuessed, printingGuessed bool) []recentCommit {
	year := 0
	if len(card.ReleasedAt) >= 4 {
		year, _ = strconv.Atoi(card.ReleasedAt[:4])
	}
	recent = append(recent, recentCommit{scryfallID: card.ID, finish: fin,
		captureSeq: captureSeq, at: now, finishGuessed: finishGuessed,
		printingGuessed: printingGuessed,
		name:            card.Name, set: card.Set,
		collectorNumber: card.CollectorNumber, releaseYear: year})
	if len(recent) > dupKeep {
		recent = recent[len(recent)-dupKeep:]
	}
	return recent
}

func footerEcho(recent []recentCommit, c scan.Card, now time.Time) (recentCommit, bool) {
	type block struct {
		set, number string
		year        int
	}
	blocks := []block{{c.SetCode, c.CollectorNumber, c.CopyrightYear}}
	for _, a := range c.CollectorAlts {
		blocks = append(blocks, block{a.Set, a.Number, a.Year})
	}
	for i := len(recent) - 1; i >= 0; i-- {
		rc := recent[i]
		if now.Sub(rc.at) >= sameCardFloor || rc.collectorNumber == "" {
			continue
		}
		for _, b := range blocks {
			if b.number == "" {

				if b.year != 0 && b.year == rc.releaseYear &&
					(b.set == "" || strings.EqualFold(b.set, rc.set)) {
					return rc, true
				}
				continue
			}
			if scryfall.BaseNumber(b.number) != scryfall.BaseNumber(rc.collectorNumber) {
				continue
			}
			if b.set != "" && !strings.EqualFold(b.set, rc.set) {
				continue
			}
			if b.year != 0 && rc.releaseYear != 0 && b.year != rc.releaseYear {
				continue
			}
			return rc, true
		}
	}
	return recentCommit{}, false
}

func touchCommit(recent []recentCommit, id string, now time.Time) []recentCommit {
	for i := len(recent) - 1; i >= 0; i-- {
		if recent[i].scryfallID == id {
			recent[i].at = now
			return recent
		}
	}
	return recent
}

func rekeyCommit(recent []recentCommit, id string, from, to finish.Finish, now time.Time) []recentCommit {
	for i := len(recent) - 1; i >= 0; i-- {
		if recent[i].scryfallID == id && recent[i].finish == from {
			recent[i].finish = to
			recent[i].finishGuessed = false
			recent[i].at = now
			return recent
		}
	}
	return recent
}

type recentName struct {
	name string
	at   time.Time
}

func recordName(recent []recentName, name string, now time.Time) []recentName {
	recent = append(recent, recentName{name: name, at: now})
	if len(recent) > dupKeep {
		recent = recent[len(recent)-dupKeep:]
	}
	return recent
}

func seenRecently(recent []recentName, name string, now time.Time) bool {
	_, ok := seenWithin(recent, name, now)
	return ok
}

func seenWithin(recent []recentName, name string, now time.Time) (time.Duration, bool) {
	for i := len(recent) - 1; i >= 0; i-- {
		rn := recent[i]
		if now.Sub(rn.at) <= dupWindow && strings.EqualFold(rn.name, name) {
			return now.Sub(rn.at), true
		}
	}
	return 0, false
}

const frameNameFloor = 0.92

func hoardBuildVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	rev, dirty, when := "", "", ""
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 9 {
				rev = s.Value[:9]
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		case "vcs.time":
			when = s.Value
		}
	}
	if rev == "" {
		return "devel"
	}
	return rev + dirty + " " + when
}

func mangledEcho(recent []recentName, line string, now time.Time) (string, bool) {
	words := mangleWords(line)
	if len(words) == 0 {
		return "", false
	}
	for _, rn := range recent {
		if now.Sub(rn.at) > dupWindow {
			continue
		}
		nameWords := mangleWords(rn.name)
		matched := true
		for _, w := range words {
			if !slices.ContainsFunc(nameWords, func(nw string) bool {
				return cardname.EditDistance(w, nw) <= 1 || strings.HasPrefix(nw, w)
			}) {
				matched = false
				break
			}
		}
		if matched {
			return rn.name, true
		}
	}
	return "", false
}

func mangleWords(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		w := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) {
				return unicode.ToLower(r)
			}
			return -1
		}, f)
		if len(w) >= 4 {
			out = append(out, w)
		}
	}
	return out
}

func similarRecent(recent []recentName, text string, now time.Time) (string, bool) {
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	for _, rn := range recent {
		if now.Sub(rn.at) <= dupWindow && cardname.Plausible(text, rn.name) {
			return rn.name, true
		}
	}
	return "", false
}

type nudgeMsg struct{ gen int }

type flashDeadlineMsg struct{ name string }

const decisionCeiling = 1000 * time.Millisecond

const nudgeEchoWindow = 4 * time.Second

const nudgeBackoffSteps = 3

func nudgeBackoff(drops int) time.Duration {
	if drops < 0 {
		drops = 0
	}
	return nudgeDelay << min(drops, nudgeBackoffSteps)
}

func (m *model) scheduleNudge() tea.Cmd {
	m.nudgeGen++
	gen := m.nudgeGen
	return delayedMsg(m.ctx, nudgeBackoff(m.nudgeDrops), nudgeMsg{gen: gen})
}

func delayedMsg(ctx context.Context, d time.Duration, msg tea.Msg) tea.Cmd {
	return func() tea.Msg {
		if ctx == nil {
			ctx = context.Background()
		}
		select {
		case <-time.After(d):
			return msg
		case <-ctx.Done():
			return nil
		}
	}
}

func (m model) wantsSecondLook(it queueItem) bool {
	if it.canonical == "" || m.secondLookFor == it.canonical {
		return false
	}
	short, _ := printingUnverified(it)
	return short
}

func (m model) onNudge(msg nudgeMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.nudgeGen || m.session == nil || !m.autoCapable {
		return m, nil
	}
	if m.flushDeferredFlash() {
		m.note("outcome %q: the second look never came, queued after all",
			m.secondLookFor)
	}
	_ = m.session.Rearm()
	m.nudgeSentAt = m.now()
	return m, nil
}

type reviewItem struct{ it queueItem }

func (r reviewItem) Title() string {
	if r.it.canonical != "" {
		return r.it.canonical
	}
	if r.it.ocrLine != "" {
		return fmt.Sprintf("%q", r.it.ocrLine)
	}
	return "(unreadable)"
}
func (r reviewItem) Description() string { return r.it.note }
func (r reviewItem) FilterValue() string { return r.Title() + " " + r.it.note }
