package tui

// The hands-free scan flow: every capture resolves in the background while the
// camera stays interactive. Confident resolutions write themselves to the
// collection (quantity 1, session destination); everything else joins a review
// queue the user walks through the normal cascade — mid-session via tab, or
// when the camera closes. This file holds the pure machinery: the queue item,
// the resolution pipeline, the auto-commit verdict, and the duplicate window.
// The model wiring lives in model.go.

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
)

// scanMatch is how strongly a capture's collector info pinned a printing.
type scanMatch int

const (
	scanMatchNone            scanMatch = iota
	scanMatchNumberAmbiguous           // number matched, but several printings share it
	scanMatchNumberOnly                // number matched exactly one printing
	scanMatchSetAndNumber              // set and number both matched
	scanMatchSinglePrint               // no number read, but only one printing exists
)

// queueItem is one scanned card and everything its background resolution
// learned: enough to re-enter the interactive cascade at the right depth
// without refetching, and enough for verdict to decide auto-commit.
type queueItem struct {
	id        int
	raw       scan.Card
	siblings  int    // how many cards the same capture yielded
	fromNudge bool   // the capture was fired by our rearm nudge, not the scene
	canonical string // "" when the name never resolved
	ocrLine   string // the line that matched, or the best guess on a miss
	lineIdx   int    // which OCR line matched; 0 is the helper's title guess
	match     cardname.Match
	prints    []scryfall.Card // ranked by rankByScanStrength; nil if never fetched
	rank      scanMatch
	// finishHint is the printed foil marker of whichever collector block won
	// verification: "foil", "nonfoil", or "" for frames without one.
	finishHint string
	// captureSeq identifies which capture produced this card, so a duplicate
	// inside one frame (a fanned playset) can be told from a card lingering
	// across frames (an un-swapped pile).
	captureSeq int
	dup        bool   // flagged as a possible duplicate of a recent commit
	note       string // human-readable reason the card queued
	errText    string // resolve error, if any
}

// resolveDoneMsg carries one finished background resolution. gen ties it to
// the resolve generation it was issued under, so a discarded session's
// stragglers land dead.
type resolveDoneMsg struct {
	gen  int
	item queueItem
	// nameDur and printsDur time the two lookup halves, for the telemetry
	// log — the Go side's slice of the per-card latency breakdown.
	nameDur   time.Duration
	printsDur time.Duration
}

// typeLineWords marks card-type vocabulary: a fallback OCR line carrying one
// is the card's type line or rules text, not a title, and resolving it ghosts
// real-but-unscanned cards into the queue ("creature." became Creature Guy,
// observed live). Only the primary line may carry them — that keeps the
// actual cards named with these words scannable by their own title line.
var typeLineWords = map[string]bool{
	"legendary": true, "creature": true, "enchantment": true,
	"planeswalker": true, "sorcery": true, "instant": true, "artifact": true,
	"battle": true, "tribal": true, "snow": true, "basic": true, "token": true,
}

// fallbackLineSuspect reports whether a non-primary OCR line should be skipped
// outright rather than tried against the searcher.
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

// collectorish matches the P/T box and collector-number shapes ("2/5",
// "123/264") that OCR routinely offers as candidate lines.
var collectorish = regexp.MustCompile(`\d+\s*/\s*\d+`)

// titleLikely is a generous title-likeness gate for fallback OCR lines — the
// shadow-card channel: a junk line is a guaranteed catalog miss, so each one
// bought a sequential Scryfall round trip and a chance to fuzzy-resolve into
// a real-but-unscanned card. A false reject only means the card queues as
// unidentified, which is the review queue's job, so the rules stay coarse: a
// title leads with a capital, is mostly letters, and isn't a P/T, collector,
// or trademark line. Only fallback lines are gated — the helper's own title
// guess (line 0) always gets its try.
func titleLikely(line string) bool {
	s := strings.TrimSpace(line)
	if s == "" {
		return false
	}
	if r := []rune(s)[0]; !unicode.IsUpper(r) {
		return false // rules fragments, quotes, and — attributions all lead low
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

// abilityWords marks the keyword abilities that print alone on their own line
// ("Haste", "Flying", "Double Strike"). No real card is named only of these,
// but Scryfall happily fuzzy-matches one to a real card ("Haste" → Haste
// Magic, a live phantom), so a fallback line made purely of them is frame
// text, never a title. Line 0 stays eligible — the card actually named with
// a keyword-bearing title resolves through its own title line.
var abilityWords = map[string]bool{
	"haste": true, "flying": true, "lifelink": true, "trample": true,
	"vigilance": true, "deathtouch": true, "menace": true, "reach": true,
	"defender": true, "hexproof": true, "indestructible": true, "flash": true,
	"rebound": true, "ward": true, "prowess": true, "first": true,
	"double": true, "strike": true,
}

// keywordLine reports whether a line is nothing but ability keywords.
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

// resolveName tries each OCR line in order until one fuzzy-matches, returning
// which line it was — the auto-commit bar treats a fallback-line match as
// suspect, since only line 0 is the helper's actual title guess.
func resolveName(ctx context.Context, s Searcher, lines []string) (canonical, ocr string, lineIdx int, match cardname.Match, err error) {
	var firstErr error
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
		card, m, ferr := s.NamedFuzzy(ctx, line)
		if ferr != nil {
			if firstErr == nil {
				firstErr = ferr
			}
			continue
		}
		if card != nil && cardname.Plausible(line, card.Name) {
			return card.Name, line, i, m, nil
		}
	}
	var top string
	if len(lines) > 0 {
		top = lines[0]
	}
	return "", top, 0, cardname.Match{}, firstErr
}

// resolveCardCmd runs one card's whole lookup — name, printings, collector
// ranking — off the UI state, so the capture step stays interactive while a
// stack of cards resolves behind it.
func (m model) resolveCardCmd(id int, c scan.Card, siblings int) tea.Cmd {
	gen := m.resolveGen
	ctx, s := m.ctx, m.searcher
	fromNudge := m.lastScanNudged
	captureSeq := m.captureSeq
	return func() tea.Msg {
		it := queueItem{id: id, raw: c, siblings: siblings, fromNudge: fromNudge,
			captureSeq: captureSeq}
		tName := time.Now()
		canonical, ocr, idx, match, err := resolveName(ctx, s, c.Lines())
		nameDur := time.Since(tName)
		var printsDur time.Duration
		it.canonical, it.ocrLine, it.lineIdx, it.match = canonical, ocr, idx, match
		if err != nil {
			it.errText = err.Error()
		}
		if canonical != "" {
			tPrints := time.Now()
			prints, perr := s.SearchPrints(ctx, canonical)
			printsDur = time.Since(tPrints)
			if perr != nil {
				it.errText = perr.Error()
			} else {
				// The band can hold several collector blocks — a stacked
				// card's sliver parses as well as the target's — so every
				// candidate is tried and the strongest verification wins: a
				// neighbour's number can't match this card's printings, the
				// real one can.
				cands := append([]scan.CollectorAlt{
					{Number: c.CollectorNumber, Set: c.SetCode, Finish: c.FinishHint,
						Source: c.NumberSource, Year: c.CopyrightYear}},
					c.CollectorAlts...)
				// A copyright-line number is upgrade-only evidence: the tiny
				// italic serif misreads digits (observed live: "30/145" read
				// as "80/145" on a card that must keep auto-committing), so
				// when no trusted band number was read, an empty sentinel
				// re-derives the no-number outcome as the floor — a matching
				// copyright number can only rank higher, never veto. A band
				// number that matches nothing keeps its veto: there the
				// number is trusted, so the mismatch means the name match
				// itself is suspect.
				trusted := slices.ContainsFunc(cands, func(cd scan.CollectorAlt) bool {
					return cd.Number != "" && cd.Source != "copyright"
				})
				if !trusted {
					cands = append(cands, scan.CollectorAlt{Finish: c.FinishHint})
				}
				it.prints, it.rank, it.finishHint = prints, scanMatchNone, c.FinishHint
				for _, cd := range cands {
					ranked, r := rankByScanStrength(prints, cd.Set, cd.Number, cd.Year)
					if r > it.rank {
						it.prints, it.rank = ranked, r
						// The winning candidate becomes the item's collector
						// context, so the picker's ranking, the ← scanned
						// marker, and the finish marker all agree with what
						// actually verified.
						it.raw.SetCode, it.raw.CollectorNumber = cd.Set, cd.Number
						it.finishHint = cd.Finish
					}
				}
			}
		}
		return resolveDoneMsg{gen: gen, item: it, nameDur: nameDur, printsDur: printsDur}
	}
}

// autoCommitOCRConfidence is the floor on the helper's reported OCR confidence
// for an unattended write. Zero (an old helper, or the field absent) is
// unknown, not failing — the name and printing gates decide alone.
const autoCommitOCRConfidence = 0.8

// verdict decides whether a resolved card may be written without a human, and
// with what finish. The returned note is the queue's display reason when it
// may not. Pure, so the whole G-matrix is table-testable.
//
// The bar, every gate required: the name matched exactly (or ≥
// cardname.AutoCommitSimilarity) on the helper's own title line; the printing
// is pinned by collector info or is the only one; OCR confidence, when
// reported, clears the floor. The headline never-rule: a name-only match with
// several printings and no collector verification never commits — the
// newest-first default would silently pick the wrong set.
func verdict(it queueItem) (auto bool, finish string, note string) {
	if it.errText != "" {
		return false, "", "lookup failed: " + it.errText
	}
	if it.canonical == "" {
		if it.ocrLine == "" {
			return false, "", "nothing readable"
		}
		return false, "", fmt.Sprintf("couldn't identify %q", it.ocrLine)
	}
	if it.lineIdx != 0 {
		return false, "", "matched a fallback OCR line; check it's the right card"
	}
	if len(it.prints) == 0 {
		return false, "", "no printings found"
	}
	switch it.rank {
	case scanMatchSetAndNumber, scanMatchNumberOnly, scanMatchSinglePrint:
	default:
		return false, "", fmt.Sprintf("printing unverified: %d printings", len(it.prints))
	}
	// The name gates weigh in only when the printing evidence is short of a
	// full set+number verification. That verification is self-consistent by
	// construction — a name fuzzy-resolved to the wrong card could not have
	// its number match that card's printings — so glare that truncates a name
	// (similarity 0.79, observed live) or drags Vision's line confidence to
	// 0.5 on an exactly-matching read must not queue a card the collector
	// block already pinned.
	if it.rank != scanMatchSetAndNumber {
		if !it.match.Exact && it.match.Similarity < cardname.AutoCommitSimilarity {
			return false, "", fmt.Sprintf("uncertain name match (%d%%)", int(it.match.Similarity*100))
		}
		if c := it.raw.Confidence; !it.match.Exact && c > 0 && c < autoCommitOCRConfidence {
			return false, "", fmt.Sprintf("low OCR confidence (%d%%)", int(c*100))
		}
	}
	// A single finish is that finish. Otherwise the printed marker decides —
	// modern frames star the collector line on foil printings and bullet it
	// on nonfoil ones — and only markerless frames fall back to nonfoil, with
	// the tally as the audit trail.
	finishes := finishOptions(it.prints[0])
	finish = "nonfoil"
	switch {
	case len(finishes) == 1:
		finish = finishes[0]
	case it.finishHint != "" && slices.Contains(finishes, it.finishHint):
		finish = it.finishHint
	}
	return true, finish, ""
}

// variationMarkers are the suffixes Scryfall appends to a collector number for
// a same-set variation — † (theme-deck alternates), ★ (foil-only rows),
// Φ (Phyrexian-text printings).
const variationMarkers = "†★Φ"

// collapseVariants reports whether every printing is the same logical printing
// — one set, one base collector number, the rows differing only by a variation
// marker — returning the unmarked row first when so. Without this, a card
// whose only reprint is its own theme-deck alternate (`ody 72` beside
// `ody 72†`, observed live) never clears the single-print bar and queues on
// every scan.
func collapseVariants(cards []scryfall.Card) ([]scryfall.Card, bool) {
	base := func(c scryfall.Card) string {
		return strings.ToLower(c.Set) + "/" + strings.TrimRight(c.CollectorNumber, variationMarkers)
	}
	for _, c := range cards[1:] {
		if base(c) != base(cards[0]) {
			return cards, false
		}
	}
	ranked := make([]scryfall.Card, 0, len(cards))
	for _, c := range cards {
		if c.CollectorNumber == strings.TrimRight(c.CollectorNumber, variationMarkers) {
			ranked = append(ranked, c)
		}
	}
	for _, c := range cards {
		if c.CollectorNumber != strings.TrimRight(c.CollectorNumber, variationMarkers) {
			ranked = append(ranked, c)
		}
	}
	return ranked, true
}

// rankByScanStrength is rankByScan with the match strength kept: the picker
// only needs "promote and mark", but the auto-commit bar has to distinguish a
// set-verified match from a number that several printings share. year, when
// non-zero, is the copyright range's end year — it equals the printing's
// release year, so it can break a number tie ("95" is both 7th and 8th
// Edition; only one was printed in 2003). A misread year simply matches no
// printing and leaves the tie ambiguous, exactly as if it were never read.
func rankByScanStrength(cards []scryfall.Card, set, number string, year int) ([]scryfall.Card, scanMatch) {
	if len(cards) == 0 {
		return cards, scanMatchNone
	}
	if number == "" {
		if len(cards) == 1 {
			return cards, scanMatchSinglePrint
		}
		if ranked, ok := collapseVariants(cards); ok {
			return ranked, scanMatchSinglePrint
		}
		return cards, scanMatchNone
	}
	best, matchIdxs, exactSet := -1, []int(nil), false
	for i, c := range cards {
		if !strings.EqualFold(c.CollectorNumber, number) {
			continue
		}
		matchIdxs = append(matchIdxs, i)
		if set != "" && strings.EqualFold(c.Set, set) && !exactSet {
			best, exactSet = i, true
			continue
		}
		if best < 0 {
			best = i
		}
	}
	if best < 0 {
		// A number was read but matches nothing — even a lone printing is
		// suspect then: the name match may have landed on the wrong card.
		return cards, scanMatchNone
	}
	yearPinned := false
	if !exactSet && len(matchIdxs) > 1 && year > 0 {
		prefix := fmt.Sprintf("%d", year)
		inYear := -1
		for _, i := range matchIdxs {
			if strings.HasPrefix(cards[i].ReleasedAt, prefix) {
				if inYear >= 0 {
					inYear = -1
					break
				}
				inYear = i
			}
		}
		if inYear >= 0 {
			best, yearPinned = inYear, true
		}
	}
	ranked := make([]scryfall.Card, 0, len(cards))
	ranked = append(ranked, cards[best])
	ranked = append(ranked, cards[:best]...)
	ranked = append(ranked, cards[best+1:]...)
	switch {
	case exactSet:
		return ranked, scanMatchSetAndNumber
	case len(matchIdxs) == 1 || yearPinned:
		return ranked, scanMatchNumberOnly
	default:
		return ranked, scanMatchNumberAmbiguous
	}
}

// --- duplicate window ---

// A re-fire on a card that hasn't moved (auto-exposure jitter, an incremental
// spread re-emitting every visible card) must not double-count. What happens
// to the re-read depends on where it came from:
//
//   - the same capture: two copies genuinely in one frame — a fanned playset.
//     Queue for the deliberate confirm; never drop.
//   - a later capture where the dup shares the frame with a new card, or a
//     nudge recheck: a card lingering on the pile beside the work. Dropped —
//     an un-swapped neighbour is not a playset signal (a real session queued
//     five re-sightings of one card this way).
//   - a later solo capture: a deliberate re-scan. Queue as a possible
//     duplicate — sequential playset scanning must never lose the copy.
//
// Time alone bounds the window — a genuine second copy scanned later
// deserves to just land.
const (
	dupWindow = 10 * time.Second
	dupKeep   = 10
)

type recentCommit struct {
	scryfallID string
	finish     string
	captureSeq int
	at         time.Time
}

// dupCapture reports whether the same printing-and-finish was auto-committed
// within the time window, and by which capture — the discriminator between a
// fanned playset and a lingering neighbour.
func dupCapture(recent []recentCommit, id, finish string, now time.Time) (captureSeq int, dup bool) {
	for _, rc := range recent {
		if rc.scryfallID == id && rc.finish == finish && now.Sub(rc.at) <= dupWindow {
			return rc.captureSeq, true
		}
	}
	return 0, false
}

// recordCommit appends to the window, pruning it to a fixed size.
func recordCommit(recent []recentCommit, id, finish string, captureSeq int, now time.Time) []recentCommit {
	recent = append(recent, recentCommit{scryfallID: id, finish: finish,
		captureSeq: captureSeq, at: now})
	if len(recent) > dupKeep {
		recent = recent[len(recent)-dupKeep:]
	}
	return recent
}

// --- recently-processed names ---

// The set of card names processed lately, refreshed on every sighting. It is
// what lets a recheck of a multi-card scene swallow *every* card of the
// previous capture (a single last-name memory let the rest dup-queue), and
// what catches an OCR-mangled re-read of a card still in frame before it
// queues as "uncertain".
type recentName struct {
	name string
	at   time.Time
}

// recordName refreshes a name in the window, pruning it to the dup window's
// size.
func recordName(recent []recentName, name string, now time.Time) []recentName {
	recent = append(recent, recentName{name: name, at: now})
	if len(recent) > dupKeep {
		recent = recent[len(recent)-dupKeep:]
	}
	return recent
}

// seenRecently reports whether this exact name was processed inside the
// window.
func seenRecently(recent []recentName, name string, now time.Time) bool {
	for _, rn := range recent {
		if now.Sub(rn.at) <= dupWindow && strings.EqualFold(rn.name, name) {
			return true
		}
	}
	return false
}

// similarRecent finds a recently processed name the text plausibly is — the
// same shape-tolerant match resolution itself uses, so "Doc Gal's Hanchmen"
// recognizes the "Doc Ock's Henchmen" added seconds ago.
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

// --- rearm nudge ---

// Geometry cannot tell a card stacked squarely on the pile from the card just
// shot, so after processing a scan the model waits a beat and, if no new
// capture arrived, nudges the helper to re-arm. A nudge-fired re-read of the
// very card just processed is dropped silently and the next nudge backs off;
// a new card commits normally. Disruption-fired identical reads still
// dup-queue — only the nudge's own echoes are swallowed, so a deliberately
// stacked playset copy is never lost to this.

// nudgeMsg fires when the post-processing quiet period elapses. gen ties it
// to the scheduling generation; any newer scan or schedule voids it.
type nudgeMsg struct{ gen int }

const nudgeBaseDelay = 2500 * time.Millisecond

// nudgeEchoWindow is how long after a sent nudge a scan counts as possibly
// nudge-originated. A window rather than a consumed flag, because a real scan
// can race the nudge onto the wire; a new card inside the window is never
// dropped (its name differs from the last processed card), so the width only
// bounds how stale an echo can be.
const nudgeEchoWindow = 4 * time.Second

// scheduleNudge arms the quiet-period timer for the current generation. It is
// armed once per processed scan and never re-armed by its own echo, so a
// parked card gets exactly one recheck.
func (m *model) scheduleNudge() tea.Cmd {
	m.nudgeGen++
	gen := m.nudgeGen
	return func() tea.Msg {
		time.Sleep(nudgeBaseDelay)
		return nudgeMsg{gen: gen}
	}
}

// onNudge sends the rearm if the session is still quiet and capable.
func (m model) onNudge(msg nudgeMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.nudgeGen || m.session == nil || !m.autoCapable {
		return m, nil
	}
	_ = m.session.Rearm()
	m.nudgeSentAt = m.now()
	return m, nil
}

// --- review queue list item ---

// reviewItem is one queued card in the review picker: its best-guess name and
// the reason it queued.
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
