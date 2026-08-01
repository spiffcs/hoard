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
	"slices"
	"strings"
	"time"

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
		if i > 0 && fallbackLineSuspect(line) {
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
	return func() tea.Msg {
		it := queueItem{id: id, raw: c, siblings: siblings, fromNudge: fromNudge}
		canonical, ocr, idx, match, err := resolveName(ctx, s, c.Lines())
		it.canonical, it.ocrLine, it.lineIdx, it.match = canonical, ocr, idx, match
		if err != nil {
			it.errText = err.Error()
		}
		if canonical != "" {
			prints, perr := s.SearchPrints(ctx, canonical)
			if perr != nil {
				it.errText = perr.Error()
			} else {
				// The band can hold several collector blocks — a stacked
				// card's sliver parses as well as the target's — so every
				// candidate is tried and the strongest verification wins: a
				// neighbour's number can't match this card's printings, the
				// real one can.
				cands := append([]scan.CollectorAlt{
					{Number: c.CollectorNumber, Set: c.SetCode, Finish: c.FinishHint}},
					c.CollectorAlts...)
				it.prints, it.rank, it.finishHint = prints, scanMatchNone, c.FinishHint
				for _, cd := range cands {
					ranked, r := rankByScanStrength(prints, cd.Set, cd.Number)
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
		return resolveDoneMsg{gen: gen, item: it}
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
		return false, "", "matched a fallback OCR line — check it's the right card"
	}
	if len(it.prints) == 0 {
		return false, "", "no printings found"
	}
	switch it.rank {
	case scanMatchSetAndNumber, scanMatchNumberOnly, scanMatchSinglePrint:
	default:
		return false, "", fmt.Sprintf("printing unverified — %d printings", len(it.prints))
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

// rankByScanStrength is rankByScan with the match strength kept: the picker
// only needs "promote and mark", but the auto-commit bar has to distinguish a
// set-verified match from a number that several printings share.
func rankByScanStrength(cards []scryfall.Card, set, number string) ([]scryfall.Card, scanMatch) {
	if len(cards) == 0 {
		return cards, scanMatchNone
	}
	if number == "" {
		if len(cards) == 1 {
			return cards, scanMatchSinglePrint
		}
		return cards, scanMatchNone
	}
	best, matches, exactSet := -1, 0, false
	for i, c := range cards {
		if !strings.EqualFold(c.CollectorNumber, number) {
			continue
		}
		matches++
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
	ranked := make([]scryfall.Card, 0, len(cards))
	ranked = append(ranked, cards[best])
	ranked = append(ranked, cards[:best]...)
	ranked = append(ranked, cards[best+1:]...)
	switch {
	case exactSet:
		return ranked, scanMatchSetAndNumber
	case matches == 1:
		return ranked, scanMatchNumberOnly
	default:
		return ranked, scanMatchNumberAmbiguous
	}
}

// --- duplicate window ---

// A re-fire on a card that hasn't moved (auto-exposure jitter, an incremental
// spread re-emitting every visible card) must not double-count. The window is
// deliberately a queue-not-drop: four physical copies is a legitimate playset,
// and the review confirm is exactly the "yes, really" that case needs. Time
// alone bounds it — the helper's own scene-change gating covers slow re-fires,
// and a genuine second copy scanned later deserves to just land.
const (
	dupWindow = 10 * time.Second
	dupKeep   = 10
)

type recentCommit struct {
	scryfallID string
	finish     string
	at         time.Time
}

// isRecentDup reports whether the same printing-and-finish was auto-committed
// within the time window.
func isRecentDup(recent []recentCommit, id, finish string, now time.Time) bool {
	for _, rc := range recent {
		if rc.scryfallID == id && rc.finish == finish && now.Sub(rc.at) <= dupWindow {
			return true
		}
	}
	return false
}

// recordCommit appends to the window, pruning it to a fixed size.
func recordCommit(recent []recentCommit, id, finish string, now time.Time) []recentCommit {
	recent = append(recent, recentCommit{scryfallID: id, finish: finish, at: now})
	if len(recent) > dupKeep {
		recent = recent[len(recent)-dupKeep:]
	}
	return recent
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
