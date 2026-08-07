package tui

// The hands-free scan flow: every capture resolves in the background while the
// camera stays interactive. Confident resolutions write themselves to the
// collection (quantity 1, session destination); everything else joins a review
// queue the user walks through the normal cascade — mid-session via tab, or
// when the camera closes. This file holds the pure machinery: the queue item,
// the resolution pipeline, the auto-commit verdict, and the duplicate window.
// The model wiring lives in model.go.

import (
	"cmp"
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

// The order is the strength order: resolveCardCmd keeps the highest-ranking
// candidate, so set+number sits at the top. It is self-consistent evidence —
// a name fuzzy-resolved to the wrong card could not have its number match
// that card's printings — and must outrank the no-number sentinel, which is
// only ever a floor.
const (
	scanMatchNone             scanMatch = iota
	scanMatchNumberAmbiguous            // number matched, but several printings share it
	scanMatchNumberTail                 // number matched nothing, but is the tail of exactly one printing's
	scanMatchYearOnly                   // no number, but the copyright year names one printing
	scanMatchYearAndMarks               // year narrowed to a few, the card's printed markings picked one
	scanMatchNumberOnly                 // number matched exactly one printing
	scanMatchSinglePrint                // no number read, but only one printing exists
	scanMatchNumberAndYear              // number named a printing and its release year agrees
	scanMatchSetAndNumber               // set and number both matched
	scanMatchSetNumberAndLang           // set, number and the printed language all matched
)

// numberVerified reports whether a collector number actually matched a
// printing of the resolved card. That is the corroboration the weaker gates
// defer to: a wrong name could not have produced a number that agrees with it.
// The no-number ranks are excluded — single-print and year-only never had a
// number to check.
func numberVerified(r scanMatch) bool {
	return r == scanMatchSetNumberAndLang || r == scanMatchSetAndNumber ||
		r == scanMatchNumberAndYear || r == scanMatchNumberOnly
}

// corroboratedPrinting reports whether *two* independent pieces of printing
// evidence agree, which is the bar for waiving the name gate entirely. A
// mangled title cannot fake that: the name chose which card's printings to
// search, and two of that card's own fields then had to agree with the band.
//
// A lone number is not enough. Collector number 12 is common enough that a
// fuzzy match onto the wrong card could collide with it by luck — pairing it
// with the set code or the release year is what removes the luck.
func corroboratedPrinting(r scanMatch) bool {
	return r == scanMatchSetNumberAndLang || r == scanMatchSetAndNumber ||
		r == scanMatchNumberAndYear
}

// printingPinned reports whether the rank already chose one specific printing
// by its number — the ranks whose head row is a claim about *which* row, not
// merely a preferred ordering. Border evidence must leave that head alone.
func printingPinned(r scanMatch) bool {
	return numberVerified(r) || r == scanMatchNumberTail
}

// String names the match for the telemetry log.
func (m scanMatch) String() string {
	switch m {
	case scanMatchNumberAmbiguous:
		return "number-ambiguous"
	case scanMatchNumberTail:
		return "number-tail"
	case scanMatchYearAndMarks:
		return "year+marks"
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

// The telemetry formatters. Every scan decision the log records goes through
// these, so a session log reads the same way whichever branch produced it.

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// matchDesc renders how the name resolved: an exact hit, or the similarity
// the auto-commit gate will compare against.
func matchDesc(m cardname.Match) string {
	if m.Exact {
		return "exact"
	}
	return fmt.Sprintf("%d%%", int(m.Similarity*100))
}

// numberSourceSuffix marks a number read off the copyright line, since that
// one is upgrade-only evidence and reads differently from a band number.
func numberSourceSuffix(src string) string {
	if src == "" {
		return ""
	}
	return "(" + src + ")"
}

// borderSuffix records what the border reader said, and whether it mattered.
// It is on the resolve line rather than hidden behind a debug flag because a
// session's worth of these is the only real-world precision data this decision
// will ever have: every card where the user then picks a different printing is
// a recorded miss, and that is what has to be read before border is ever
// allowed to do more than sort a list.
func borderSuffix(it queueItem) string {
	if it.raw.BorderColor == "" {
		return ""
	}
	s := " border=" + it.raw.BorderColor
	if it.raw.BorderSource != "" {
		s += "(" + it.raw.BorderSource + ")"
	}
	if !it.borderFiltered {
		// Read but idle: every printing agreed, or none did. Worth telling
		// apart from a read that reordered, since only the latter can be wrong
		// in a way the user would ever see.
		s += " unused"
	}
	return s
}

// finishSuffix records what the card said about its finish, and — when the
// sparkle reader was the one asked — the number behind the answer.
//
// The provenance was put on the wire so a session log could show which signal
// was carrying the finish, and then never printed, which left the log exactly
// as silent as before. The score comes with it because a verdict alone cannot
// be debugged: the four foils that read nonfoil live scored 0.020, 0.339, 0.425
// and 0.496 against a bar of 0.52, and only the last of those is a threshold
// problem. The offset says which — a match found at the edge of the search
// window means the marker is outside it.
func finishSuffix(it queueItem) string {
	c := it.raw
	if it.finishHint == "" && c.FinishHint == "" && c.SparkleScore == nil {
		return ""
	}
	// The hint the verdict will actually use, which the candidate loop may
	// have adopted from a winning collector block. The raw hint used to be
	// printed here, so the log could show `nonfoil(separator)` for a commit
	// the alt block decided differently — a log that disagrees with the write
	// it is narrating.
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
		// The luma patch spread — the number that tells a sub-threshold miss
		// (real structure, faint) from an abstention scored as 0.000 (nothing
		// there at all). It crossed the wire from the start and was the one
		// field never printed, which made those two failures look identical
		// in every session log this far.
		if c.SparkleContrast != nil {
			s += fmt.Sprintf("/%.4f", *c.SparkleContrast)
		}
	}
	// The colour channel, logged beside the one that decided. A bare number
	// rather than a verdict, because it has no vote — the session log is
	// currently the only place its live behaviour can be observed at all, and
	// the question it has to answer is how often it would have been right.
	if c.SparkleChromaScore != nil {
		s += fmt.Sprintf(" chroma=%.3f", *c.SparkleChromaScore)
		if c.SparkleChromaContrast != nil {
			s += fmt.Sprintf("/%.4f", *c.SparkleChromaContrast)
		}
	}
	return s
}

// siblingSuffix records the context a card was seen in, plus the one flag
// worth auditing after the fact. The first two are inputs to the phantom,
// duplicate, and echo rules; number-overridden marks a card that committed
// despite its collector read rather than because of it.
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
	// The measurement behind moved/replaced, on the line that records the
	// outcome it decided. The phone traces it too, but on a different line and
	// only to stderr — and this is the number placementFaceFloor has to be
	// re-fitted from, so it belongs next to the verdict it produced rather
	// than one grep away from it.
	if it.faceDelta != nil {
		s += fmt.Sprintf(" face=%.1f", *it.faceDelta)
	}
	if it.numberOverridden {
		s += " number-overridden"
	}
	return s
}

// queueItem is one scanned card and everything its background resolution
// learned: enough to re-enter the interactive cascade at the right depth
// without refetching, and enough for verdict to decide auto-commit.
type queueItem struct {
	id        int
	raw       scan.Card
	siblings  int  // how many cards the same capture yielded
	fromNudge bool // the capture was fired by our rearm nudge, not the scene
	// fromMoved marks a capture the source fired because a card *moved*: a box
	// held the watched spot and still looked like the card already read. It is
	// the source declining to claim a placement it cannot see, and the
	// duplicate rules take it at its word.
	fromMoved bool
	// fromReplaced marks a capture the source fired because a box held the
	// watched spot wearing a face it did not recognize: its positive claim
	// that this is a different physical card. faceDelta is the measurement
	// behind that claim, nil when the source never made one.
	//
	// Together they are the only evidence allowed to outrank sameCardFloor —
	// see placedDecisively.
	fromReplaced bool
	faceDelta    *float64
	canonical    string // "" when the name never resolved
	ocrLine      string // the line that matched, or the best guess on a miss
	lineIdx      int    // which OCR line matched; 0 is the helper's title guess
	match        cardname.Match
	prints       []scryfall.Card // ranked by rankByScanStrength; nil if never fetched
	rank         scanMatch
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
	// numberOverridden records that a collector number was read but no
	// candidate carrying one verified, so the card rests on its name and a
	// single printing instead. Rare and worth auditing: it is the one path
	// that commits in spite of collector evidence rather than because of it.
	numberOverridden bool
	// viaBlock records that the card was identified from its collector block
	// because no line of text resolved — the title never read at all.
	viaBlock bool
	// borderFiltered records that the border read off the card reordered the
	// printings. It changes no verdict — border is ordering only, see
	// applyBorderEvidence — but every read belongs on the resolve line, since
	// a session's worth of them is the evidence for ever trusting it further.
	borderFiltered bool
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

// hasCollectorBlock reports whether a capture carried a set and number good
// enough to name a printing on their own — the signal that an unidentified
// entry is a real card whose title would not read, not a phantom. Copyright
// numbers do not count; they are too misread-prone to identify a card.
func hasCollectorBlock(c scan.Card) bool {
	if c.SetCode != "" && c.CollectorNumber != "" && c.NumberSource != "copyright" {
		return true
	}
	return slices.ContainsFunc(c.CollectorAlts, func(a scan.CollectorAlt) bool {
		return a.Set != "" && a.Number != "" && a.Source != "copyright"
	})
}

// hasPrintingEvidence reports whether the capture read anything off a card's
// footer at all — a set code, a collector number, a copyright year, or one of
// those on an alternative block.
//
// Deliberately weaker than hasCollectorBlock, and asking a different question.
// hasCollectorBlock asks "can this block *name* a card", which is why it
// refuses a copyright-sourced number: those misread digits often enough that
// resolving from one would invent cards. This asks only "was a card in frame",
// and for that a suspect number is still evidence — a printing was read, the
// digits are merely not trustworthy enough to act on alone.
//
// The distinction is what keeps the junk filter from eating a real card. A
// capture of ability text with an empty footer identified nothing and is noise;
// a capture whose title read as rules copy but whose footer says MSH/412 is a
// card, and belongs in review where a person can finish the job.
func hasPrintingEvidence(c scan.Card) bool {
	if c.SetCode != "" || c.CollectorNumber != "" || c.CopyrightYear != 0 {
		return true
	}
	return slices.ContainsFunc(c.CollectorAlts, func(a scan.CollectorAlt) bool {
		return a.Set != "" || a.Number != "" || a.Year != 0
	})
}

// blockSearcher is implemented by searchers that can resolve a printing from
// its collector block alone.
type blockSearcher interface {
	PrintBySetNumberLang(ctx context.Context, set, number, lang string) (*scryfall.Card, error)
}

// resolveByBlock identifies a card from a collector block when no line of text
// resolved. Every block the capture offered is tried, primary first, and the
// first that names exactly one printing wins.
//
// A copyright-sourced number is not allowed to do this. Everywhere else it is
// upgrade-only evidence because that glyph size misreads digits, and a misread
// number here would not merely rank a card wrongly — it would invent one out of
// a card that was never identified.
func resolveByBlock(ctx context.Context, s Searcher, c scan.Card) (*scryfall.Card, scan.CollectorAlt) {
	byBlock, ok := s.(blockSearcher)
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

// localOnlySearcher is implemented by searchers with a local catalog layer,
// resolving without the network fallthrough.
type localOnlySearcher interface {
	NamedFuzzyLocal(ctx context.Context, text string) (*scryfall.Card, cardname.Match, error)
}

// nudgeDelay is the quiet period after a processed scan before the phone is
// nudged to re-arm.
//
// There used to be a second, shorter value here for the local Continuity pipe,
// picked by source kind. The phone is the only source now, and its number is
// the one that was actually fitted against a session: measured over 61
// captures, the gap from a result to the next capture was *never* under 2500ms
// — the fastest was 3856ms, the median 4896ms. The old local delay would have
// fired on every single card, during the operator's swap rather than after it,
// and 43 of 61 resolves came back tagged as nudged. That is not free: each one
// re-arms mid-swap and buys an extra capture, which is most of the gap between
// that session's 1.27 captures per commit and the ledger's 1.1.
//
// 5500ms sits above the observed median swap and below the p75 of 7047ms, so
// it fires when a card is genuinely parked and stays quiet when someone is
// simply working at their own pace. See the remote row in
// docs/scanner-tuning.md.
const nudgeDelay = 5500 * time.Millisecond

// nameTimeout bounds the Scryfall escalation for a line the local catalog could
// not place.
//
// The catalog has already answered by this point; the network call only catches
// a card printed since the last catalog build, which is a bonus and must never
// be a stall. Past this the card queues, which is a far better outcome than a
// session that freezes.
//
// 250ms rather than the 500ms the local pipe used, because this loop has less
// headroom to give away, not more. The budget is 700ms shutter-to-result; the
// phone's own half of that measured 447ms median and 472ms at p90, leaving
// roughly 230ms before the rhythm the sounds depend on starts to slip. A 500ms
// escalation would blow it outright.
const nameTimeout = 250 * time.Millisecond

// searchLine resolves one OCR line, choosing how far off-machine it may go.
// Fallback lines stay catalog-only (verdict refuses to auto-commit them
// anyway); line 0 escalates only when it reads like a title.
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
	// A timeout is this policy working, not a failure to report. Surfacing it
	// put `lookup failed: … context deadline exceeded` and a raw Scryfall URL
	// in the review queue; the honest outcome is the one we already had — the
	// catalog did not know this card.
	if rerr != nil && rctx.Err() != nil {
		return card, m, nil
	}
	return rc, rm, rerr
}

// resolveName tries each OCR line in order until one fuzzy-matches, returning
// which line it was — the auto-commit bar treats a fallback-line match as
// suspect, since only line 0 is the helper's actual title guess.
//
// Only line 0 is allowed off-machine. verdict refuses to auto-commit any
// fallback-line match, so the network fallthrough that keeps newly-released
// cards scannable can buy a fallback line nothing but latency and a queue
// ghost — and it charged a lot: one live session spent 19s across 15 failed
// resolutions, the worst single line taking 4.8s, against 0-15ms for every
// catalog hit.
func resolveName(ctx context.Context, s Searcher, lines []string) (canonical, ocr string, lineIdx int, match cardname.Match, err error) {
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
		// Line 0 always gets its catalog try, unfiltered — that is what keeps
		// a card genuinely named with a keyword or a type word scannable, and
		// the catalog is free. What is not free is escalating to Scryfall, and
		// on an unreadable frame that escalation asks the network about a
		// string that was never a name: six such lookups in one session
		// returned nothing after ~600ms each, the worst loop costing 3.9s.
		// So the two layers are split here, and only a line that looks like a
		// title is allowed off the machine.
		card, m, ferr := searchLine(ctx, s, local, hasLocal, line, i)
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
	fromMoved := m.lastScanMoved
	fromReplaced := m.lastScanReplaced
	faceDelta := m.lastScanFaceDelta
	captureSeq := m.captureSeq
	return func() tea.Msg {
		it := queueItem{id: id, raw: c, siblings: siblings, fromNudge: fromNudge,
			fromMoved: fromMoved, fromReplaced: fromReplaced, faceDelta: faceDelta,
			captureSeq: captureSeq}
		tName := time.Now()
		canonical, ocr, idx, match, err := resolveName(ctx, s, c.Lines())
		nameDur := time.Since(tName)
		var printsDur time.Duration
		it.canonical, it.ocrLine, it.lineIdx, it.match = canonical, ocr, idx, match
		if err != nil {
			it.errText = err.Error()
		}
		// A title that would not read does not make the card unidentifiable:
		// the collector block names it just as precisely. Only reached when
		// every line failed, so this costs nothing on the normal path.
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
				//
				// The alts carry no Source of their own and so read as band
				// numbers here. That holds because the helper fills a
				// copyright number only when the band gave nothing at all,
				// and the alts are the band parse's own tail — so a
				// copyright-sourced primary always arrives with an empty alt
				// list and an empty set code. Keep those facts together if
				// either side changes: an alt able to carry a copyright
				// number would silently defeat this gate.
				trusted := slices.ContainsFunc(cands, func(cd scan.CollectorAlt) bool {
					return cd.Number != "" && cd.Source != "copyright"
				})
				// An exact name earns the same floor. The veto below exists
				// because a number matching nothing suggests the *name* landed
				// on the wrong card — but that reasoning is about a fuzzy
				// match, and it inverts when the name is exact: there the
				// mismatch is the digits, and this glyph size misreads digits
				// constantly (Lethal Vapors' 68 arrived as 8, live, on a card
				// with one printing and a perfect name read).
				//
				// The floor only pays out when the printings collapse to one,
				// since rankByScanStrength gives an empty number nothing to
				// work with otherwise — so a card with nine printings and a
				// bad number still queues. The effect is simply that a garbage
				// number can no longer be worse than no number at all, which
				// is the outcome an exact name already commits on.
				//
				// The risk it accepts: scanning off a stack, an exact read of
				// a *neighbour's* title whose card has one printing will now
				// commit that neighbour instead of queueing. Auditable — the
				// resolve line marks every rescue `number-overridden`.
				// The year rides along: without it the sentinel re-derives the
				// no-number outcome blind, and soleIndexInYear cannot fire in
				// the one situation it exists for — an old frame whose only
				// number came off the copyright line and matched nothing.
				if !trusted || match.Exact {
					cands = append(cands, scan.CollectorAlt{
						Finish: c.FinishHint, Year: c.CopyrightYear})
				}
				it.prints, it.rank, it.finishHint = prints, scanMatchNone, c.FinishHint
				for _, cd := range cands {
					ranked, r := rankByScanStrength(
						prints, cd.Set, cd.Number, cd.Year, c.BorderColor, c.FinishHint,
						cmp.Or(cd.Language, c.Language))
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
				// A number was printed on the wire and none of the candidates
				// carrying one verified: whatever committed rests on the name
				// and a lone printing.
				it.numberOverridden = it.raw.CollectorNumber == "" &&
					slices.ContainsFunc(cands, func(cd scan.CollectorAlt) bool {
						return cd.Number != ""
					})
			}
			// Border last, and only over the ordering the ranking settled on.
			// It never revisits the rank: an old frame that verified nothing
			// still queues, it just queues with the right printing on top.
			//
			// And only when the rank did not already name a printing. A
			// number match put one specific row at the head, and the head is
			// what commits — a border+year shuffle after that can replace a
			// set+number+lang exact with a sibling printing that happens to
			// share the read border, which is how an M15 Ornithopter read as
			// M15/223 nonfoil committed as SLD/604 foil on 2026-08-06. Border
			// evidence is for orderings the number never settled.
			if !printingPinned(it.rank) {
				it.prints, it.borderFiltered = applyBorderEvidence(
					it.prints, c.BorderColor, c.CopyrightYear)
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
// printingUnverified reports whether the collector evidence failed to pin a
// printing, and the queue note to show when it did.
//
// Split out of verdict rather than inlined there because two callers need the
// same answer: verdict, to refuse the commit, and the queue path in model.go,
// to decide whether the card is worth one more look. The alternative was
// matching on the prose of the note, which would make this file's wording
// load-bearing in a way it should never be.
//
// Requires the caller to have already established a name and some printings —
// verdict checks both above, and "no printings found" is a different answer
// from "several, and nothing chose between them".
func printingUnverified(it queueItem) (short bool, note string) {
	if len(it.prints) == 0 {
		return false, ""
	}
	switch it.rank {
	case scanMatchSetNumberAndLang, scanMatchSetAndNumber, scanMatchNumberAndYear,
		scanMatchNumberOnly, scanMatchSinglePrint,
		// A tail match is a repaired number, not a verified one, so it commits
		// on the same terms as the year strata: numberVerified() still says no,
		// which keeps verdict's fallback-OCR-line veto and confidence floor
		// standing over it.
		scanMatchNumberTail:
		return false, ""
	case scanMatchYearOnly, scanMatchYearAndMarks:
		// The same contradiction check the ambiguous-number case carries, and
		// these ranks need it more: the year is their *whole* evidence, so a
		// head row from a different year means something after the ranking —
		// the border reorder — displaced the row the rank was about. Observed
		// live, 2026-08-07: Ornithopter ranked year-only on a 2014 read, a
		// misread white border then exiled every black row, and the head that
		// committed was a 2022 borderless printing whose foil-only finish
		// became "evidence" — a wrong printing *and* a wrong finish off one
		// commit the rank's own year refutes.
		if y := it.raw.CopyrightYear; y > 0 && len(it.prints) > 0 &&
			!strings.HasPrefix(it.prints[0].ReleasedAt, fmt.Sprintf("%d", y)) {
			return true, fmt.Sprintf(
				"printing unverified: %d printings, and the front one is not from %d",
				len(it.prints), y)
		}
		return false, ""
	case scanMatchNumberAmbiguous:
		// A number that matched several printings commits the one the ranking
		// put in front, rather than queuing.
		//
		// This is a deliberate trade, made on the operator's preference for
		// false positives over false negatives: a wrong printing is one row to
		// correct later, while a queued card is a stop in a session whose whole
		// point is not stopping. The ranking already believed enough to reorder
		// the list; acting on that belief and refusing to write it down were
		// hard to justify together.
		//
		// The ordering is not arbitrary. Printings come back newest-first with
		// ties broken by set code, and a promo set is its regular set's code
		// with a `p` in front — `dom` before `pdom` — so on the collision this
		// case is really about, the regular printing leads its own promos.
		//
		// Never against evidence, though. Absence of a year is why this pick is
		// uncertain; a year that *disagrees* is a different thing entirely, and
		// committing a 2024 printing when the band read 2018 would be ignoring
		// the card rather than guessing without it.
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
	if len(it.prints) == 0 {
		return false, "", "no printings found"
	}
	if short, note := printingUnverified(it); short {
		return false, "", note
	}
	// A fallback line is a weak place to find a name — it is not the helper's
	// own title guess — so it queues unless the printing evidence stands on its
	// own. A number that matches a printing of the card the line resolved to is
	// exactly that: the line could not have picked the wrong card and then had
	// its number agree. Ordered after the rank switch for that reason; as an
	// unconditional veto it queued a Forest holding an exact name and a full
	// MSH/286 match, and a Glowrider whose number named one printing (live).
	if it.lineIdx != 0 && !numberVerified(it.rank) {
		return false, "", "matched a fallback OCR line; check it's the right card"
	}
	// The name gate weighs in only when the printing evidence is short of two
	// agreeing signals. That pairing is self-consistent by construction — a
	// name fuzzy-resolved to the wrong card could not have both the number and
	// the set (or the release year) of that card agree — so glare that
	// truncates a name (0.79, live) or garbles two letters of it ("Stemal
	// Dragon", 0.76, live) must not queue a card the band already pinned.
	if !corroboratedPrinting(it.rank) {
		if !it.match.Exact && it.match.Similarity < cardname.AutoCommitSimilarity {
			return false, "", fmt.Sprintf("uncertain name match (%d%%)", int(it.match.Similarity*100))
		}
	}
	// Vision's confidence is a statement about the *glyphs*, and a matched
	// number answers it: the digits and the name agree on a real printing, so a
	// soft-looking title is soft, not wrong. Eternal Dragon (name 92%, number
	// naming one printing) and Hobgoblin (96%, same) both queued on a 0.5
	// reading, live. Below a verified number the floor still stands.
	if !numberVerified(it.rank) {
		if c := it.raw.Confidence; !it.match.Exact && c > 0 && c < autoCommitOCRConfidence {
			return false, "", fmt.Sprintf("low OCR confidence (%d%%)", int(c*100))
		}
	}
	// An unread finish takes the nonfoil default and commits anyway. The
	// `evidenced` flag is dropped here on purpose, and it is the one place in
	// this file that deliberately discards a piece of evidence.
	//
	// Queuing on it was built and reversed the same day. The gate is right in
	// the abstract — Glowrider, Trap Digger and Hard Evidence all committed
	// `nonfoil` off silence on 2026-08-06 and all three were foil — but it
	// treats the wrong thing as the defect. The defect is that the sparkle
	// reader missed them. A stop in the session does not fix that; it moves the
	// cost of the miss onto the operator, on every retro card in every pile,
	// forever. The standing preference holds here as everywhere else in this
	// function: a wrong row is one row to correct, a queued card is a stop in a
	// session whose whole point is not stopping.
	//
	// So this is a deliberate bet on the foil reader, and it is why the number
	// that matters about that reader is its false *negative* rate — every miss
	// is now a silently underpriced row rather than a question. See
	// docs/scanner-foil-registration.md.
	finish, _ = finishFromEvidence(it.prints[0], it.finishHint)
	return true, finish, ""
}

// finishFromEvidence picks the finish to record and says whether the card
// actually told us. A single-finish printing is not a choice at all, and a
// printed marker the printing recognizes is evidence; anything else is the
// nonfoil default, which is a guess and has to be remembered as one.
//
// Old frames make that distinction matter. They carry no set/language line, so
// no marker ever reaches here and every old foil records as nonfoil — silently,
// and foil is worth a multiple. Callers that keep the guess flag can at least
// notice when a later look disagrees.
func finishFromEvidence(card scryfall.Card, hint string) (finish string, evidenced bool) {
	finishes := finishOptions(card)
	if len(finishes) == 1 {
		return finishes[0], true
	}
	if hint != "" && slices.Contains(finishes, hint) {
		return hint, true
	}
	return "nonfoil", false
}

// collapseVariants reports whether every printing is the same logical printing
// — one set, one base collector number, the rows differing only by a variation
// marker — returning the unmarked row first when so. Without this, a card
// whose only reprint is its own theme-deck alternate (`ody 72` beside
// `ody 72†`, observed live) never clears the single-print bar and queues on
// every scan.
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

// soleIndexInYear returns the one index among idxs whose printing was released
// in the given year, or -1 when none or several were. "Several" is a failure
// on purpose: two printings sharing a year means the year settles nothing.
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

// borderRulesOut reports whether a read border excludes a printing.
//
// One definition, shared by the ordering and the ranking, because they must
// never disagree about what a border means — a printing promoted to the top of
// the review list and a printing chosen for an unattended commit have to be the
// same printing.
//
// The reader answers only "white" or "black", but those two answers do not
// carry the same weight, and treating them as if they did is what produced a
// wrong commit. Gold and silver borders are *light*:
//
//   - A **black** read excludes them. Black sits at the dark end of the card's
//     own tone range, measured at -0.11 to -0.23 across live captures, and no
//     gold or silver border lands there. So reading black on Mana Leak's 1998
//     line rules out `wc98/rb36` and confirms `sth/36`.
//   - A **white** read excludes neither, because white, gold and silver are all
//     bright and the reader was built to tell dark from light. This is not
//     caution for its own sake: a white read on that same card once eliminated
//     the black printing, left the gold one standing alone, and committed a
//     World Championship card by pure elimination.
//
// Borderless is never excluded by either. Its "border" is whatever the art does
// at the edge, which can be any tone at all.
func borderRulesOut(c scryfall.Card, border string) bool {
	if border == "" {
		return false
	}
	// Keyed on what was *read*, not on what the printing is. Keyed the other
	// way, a read this build does not produce — "gold", or a value from a
	// newer helper — would start excluding printings on the strength of not
	// matching, which is the opposite of failing closed.
	switch strings.ToLower(border) {
	case "black":
		return isLightBorder(c.BorderColor)
	case "white":
		return strings.EqualFold(c.BorderColor, "black")
	default:
		return false
	}
}

// isLightBorder reports whether a printing's border is one of the bright ones.
//
// White, gold and silver all sit at the light end and are what a black read
// excludes. Borderless is deliberately absent: its edge is whatever the art
// does there, so it can be any tone and is never excluded by either answer.
func isLightBorder(color string) bool {
	switch strings.ToLower(color) {
	case "white", "gold", "silver":
		return true
	}
	return false
}

// finishRulesOut reports whether a read foil marker excludes a printing.
//
// The set promo is the case this exists for. A prerelease or set promo is
// printed *foil only* — `psoi/59s` beside `soi/59`, `paer/29s` beside `aer/29`,
// same card, same set, same release date — so the year cannot separate them and
// the card queues holding two choices that differ in exactly one visible way.
//
// The card says which it is. A bullet between the set code and the language is
// the nonfoil marker, and a printing that only ever existed as a foil cannot be
// the card that printed a bullet. The reverse holds too: a star cannot be a
// printing that never came in foil.
//
// Silence excludes nothing. Frames before ~2003 print no marker at all, and a
// glyph too small to read comes back empty rather than as "nonfoil" — treating
// either as evidence would rule out real printings on the strength of a marker
// that was never there to read.
func finishRulesOut(c scryfall.Card, finish string) bool {
	if finish == "" {
		return false
	}
	return !slices.Contains(finishOptions(c), finish)
}

// markingsAgree reports whether a printing positively matches every marking the
// capture actually read.
//
// The counterpart to the narrowing above, and the lesson from a wrong commit:
// surviving elimination is not agreement. A printing left standing only because
// nothing could exclude it has no evidence behind it, so each signal that was
// read has to point *at* the winner rather than merely fail to point away.
func markingsAgree(c scryfall.Card, border, finish string) bool {
	if border != "" && !strings.EqualFold(c.BorderColor, border) {
		return false
	}
	if finish != "" && !slices.Contains(finishOptions(c), finish) {
		return false
	}
	return true
}

// applyBorderEvidence promotes the printings whose border matches the one read
// off the card, reporting whether it changed anything. It is ordering only: no
// printing is ever removed, no rank is ever raised, no commit is ever blocked.
//
// That restraint is the whole design. Before 1998 nothing but the copyright
// year is printed, and the year alone pins under a quarter of those cards;
// border colour is the era's other discriminator and takes it to nearly half,
// with Fourth Edition going from nothing to almost everything, because 4ED
// (white) and 4BB (black) share their year, art and artist. So it is worth
// real evidence — but it cannot be weighed like the rest of them.
//
// Every other signal here fails closed. A misread year matches no printing and
// evaporates; a misread collector number matches nothing and the empty
// sentinel re-derives the no-number outcome as the floor. A border is one bit,
// and a wrong bit always matches *something*, because there is always a
// black-bordered candidate. It cannot fail closed on its own, so it does not
// get to decide anything on its own — here it sorts the review queue, and
// nothing more.
//
// A border matching no printing at all is ignored rather than treated as a
// contradiction. That is what makes a wrong read on a gold, silver or
// foreign-language card cost nothing: the reader already abstains on those,
// and if it ever stops abstaining the mistake still buys nothing.
//
// The year comes in because border alone lands on the wrong row. Control
// Magic has twenty printings and plenty are white, so "white" first promotes
// whichever white one is newest — Battle Royale, observed on the fixture.
// Neither field pins it alone: 1995 is shared by 4ED and 4BB, white is shared
// by 4ED and half the reprints. Together they name exactly one. So the order
// is the pairing first, then the border, then everything else, which makes the
// top row here the same row a rank that trusted the pairing would commit —
// and that is deliberate, because it means a session's top rows measure the
// next step's accuracy before the next step is allowed to write anything.
func applyBorderEvidence(prints []scryfall.Card, border string, year int) ([]scryfall.Card, bool) {
	if border == "" || len(prints) < 2 {
		return prints, false
	}
	// The reader tells white from black and nothing else. Gold and silver read
	// as white often enough to matter — eight of them across the corpus — and
	// no photometry fixes it, because a silver border simply is light grey.
	//
	// So a printing in a colour the reader cannot recognise is never *ruled
	// out*, whatever it said. It is not promoted either: it sits where the
	// newest-first order left it, ahead of the printings this read genuinely
	// excludes. Suppressing the border entirely for such cards was the first
	// attempt and it was far too blunt — 22% of pre-1998 multi-printing cards
	// have a gold or silver sibling somewhere, and Control Magic, one of the
	// cards this whole feature exists for, lost its border to two Pro Tour
	// Collector Set rows it was never going to be confused with.
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
	// Nothing matched the border, or everything did and the year adds no
	// split: either way the order this would impose is the order already held.
	if len(rest) == len(prints) || (len(rest) == 0 && len(both) == 0) {
		return prints, false
	}
	ranked := make([]scryfall.Card, 0, len(prints))
	ranked = append(ranked, both...)
	ranked = append(ranked, borderOnly...)
	return append(ranked, rest...), true
}

// allIndexes is every position in a slice of n, for a search over all cards.
func allIndexes(n int) []int {
	idxs := make([]int, n)
	for i := range idxs {
		idxs[i] = i
	}
	return idxs
}

// moveToFront promotes one printing to the head, leaving the rest in order —
// the shape the picker reads as "this is the scanned one".
func moveToFront(cards []scryfall.Card, i int) []scryfall.Card {
	ranked := make([]scryfall.Card, 0, len(cards))
	ranked = append(ranked, cards[i])
	ranked = append(ranked, cards[:i]...)
	return append(ranked, cards[i+1:]...)
}

// rankByScanStrength is rankByScan with the match strength kept: the picker
// only needs "promote and mark", but the auto-commit bar has to distinguish a
// set-verified match from a number that several printings share. year, when
// non-zero, is the copyright range's end year — it equals the printing's
// release year, so it can break a number tie ("95" is both 7th and 8th
// Edition; only one was printed in 2003). A misread year simply matches no
// printing and leaves the tie ambiguous, exactly as if it were never read.
// numberMatches are the printings that answer to the number read off the card.
//
// Exact is the ordinary case. A marker-trimmed sibling also answers — `war/97★`
// beside `war/97`, where Scryfall keeps two rows apart under one number using a
// glyph the card does not print, so OCR reads the bare number and can only ever
// reach the unmarked row — but only when the card's own set row vouches for it
// with *both* its set code and its language.
//
// Requiring the set code is what makes an unreliable language safe to act on.
// Measured over scan/corpus the language read answers on a fifth of cards and
// is right four in five, and its errors are systematic: a line of rules text
// parses as a set row and donates a language, so "…put it into your hand"
// yields Italian on a plainly English card (docs/scanner-tuning.md, "Prose
// fabricates set codes"). But that same fabrication invents the set code beside
// it, and an invented set code matches no printing — so the bogus language
// arrives attached to a set that rules it out, and the sibling never enters the
// running. A language is only ever trusted in the company of a set code that
// checks out.
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

// numberTailMatchMinDigits is how short a read number may be and still be
// offered to numberTailMatches.
//
// Two, because one digit against a three-digit number is barely a claim: every
// printing numbered 1-9, 11, 21, 31 … answers to "1", so the "exactly one
// match" guard would be carrying the entire argument. Two digits against the
// live misreads (14 for 414, 18 for 418) already leaves the guard something to
// stand on. A one-digit number that matches *exactly* is untouched by this and
// still commits — Abiding Grace reads a bare 1 off H2R and always has.
const numberTailMatchMinDigits = 2

// numberTailMatches are the printings whose collector number ends with the
// digits read off the card.
//
// This repairs exactly one failure — a *lost leading digit* — and it is worth
// being precise about how narrow that is, because the neighbouring failures look
// identical in the log and none of them is fixable this way. Checked against the
// catalog, on the four numbers two live sessions of one pile read wrongly:
//
//	Meltdown         18  → mh3/418   a digit lost      · repaired here
//	Dress Down       14  → h2r/4     a digit *gained*  · no tail, still queues
//	Unstable Amulet  431 → mh3/421   3 for 2           · no tail, still queues
//	Lion Umbra       420 → mh3/426   0 for 6           · no tail, still queues
//
// So three of the four are substitutions or insertions, and the tempting
// generalisation — match within one edit — would have to guess *which* digit
// lied. Against Lion Umbra's two printings, `420` sits one edit from `426` and
// nothing else, and it would commit on that; against a card with a dozen
// printings it would collide constantly. A tail is the one repair where the
// digits that were read are all still true, which is why it is the only one
// here.
//
// It is deliberately not part of numberMatches. A tail is not a match; it is a
// guess about which digits went missing, and it is only safe as a last resort
// after the honest comparison has already come back empty — see the call site.
//
// Digits only on both sides. Scryfall numbers a promo `123★` and a variant
// `123a`, and "the tail of" is a question about the printed run of digits, not
// about a suffix the card does not print in that position at all.
func numberTailMatches(cards []scryfall.Card, number string) []int {
	if len(number) < numberTailMatchMinDigits || !isDigits(number) {
		return nil
	}
	var out []int
	for i, c := range cards {
		base := scryfall.BaseNumber(c.CollectorNumber)
		// Strictly longer: an equal-length "tail" is the exact match, which by
		// construction has already been tried and failed.
		if len(base) <= len(number) || !isDigits(base) {
			continue
		}
		if strings.HasSuffix(base, number) {
			out = append(out, i)
		}
	}
	return out
}

// isDigits reports whether every byte is an ASCII digit. Empty is not.
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

// langSaysSo reports whether the card's printed language agrees with this
// printing's. Unknown on either side is not agreement: a catalog built before
// the language column stores none, and silence must not read as a match.
func langSaysSo(c scryfall.Card, lang string) bool {
	return lang != "" && c.Lang != "" && strings.EqualFold(c.Lang, lang)
}

func rankByScanStrength(cards []scryfall.Card, set, number string, year int,
	border, finish, lang string,
) ([]scryfall.Card, scanMatch) {
	if len(cards) == 0 {
		return cards, scanMatchNone
	}
	if number == "" {
		return rankWithoutNumber(cards, year, border, finish, true)
	}
	matchIdxs := numberMatches(cards, set, number, lang)
	best, exactSet, langAgrees := -1, false, false
	for _, i := range matchIdxs {
		c := cards[i]
		setMatches := set != "" && strings.EqualFold(c.Set, set)
		// Among rows that all answer to the number, the language breaks the tie:
		// `war/97` and `war/97★` share a set and a number, and the marked one is
		// the Japanese alternate art at fourteen times the price.
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
		// A number was read but matches nothing. That is a reason to distrust
		// the number — the name match may have landed on the wrong card, or the
		// digits are a misread — but it is not a reason to throw away the rest
		// of the capture, which is what returning here used to do. The year on
		// the copyright line and the markings printed on the card are still
		// true, and they are exactly the evidence the no-number stratum below
		// was built to weigh. Live: Lion Umbra read a clean 2024 and a foil
		// sparkle against two printings and queued as "printing unverified"
		// holding both, because a collector number that agreed with neither
		// silenced them.
		//
		// Before falling through, one last reading of the digits themselves: a
		// number that matched nothing may simply be missing its leading digit.
		// Live, Meltdown's mh3/418 read as `18` and queued as "printing
		// unverified" against four printings, of which only 418 ends in 18.
		//
		// Only when it names exactly one printing, and only when the year has
		// nothing to say against it. Ambiguity and contradiction both fall
		// through to the strata below, which is where they were already going.
		if tails := numberTailMatches(cards, number); len(tails) == 1 {
			if c := cards[tails[0]]; year <= 0 ||
				strings.HasPrefix(c.ReleasedAt, fmt.Sprintf("%d", year)) {
				return moveToFront(cards, tails[0]), scanMatchNumberTail
			}
		}
		// Everything except the sole-printing floor. That one is pure absence
		// of evidence — "no number was read, and there is only one candidate" —
		// and a number that matched nothing is a positive reason for suspicion
		// standing against it. The strata that rest on something the card
		// actually said still apply.
		return rankWithoutNumber(cards, year, border, finish, false)
	}
	return rankMatchedNumber(cards, matchIdxs, best, exactSet, langAgrees, year, border, finish)
}

// rankWithoutNumber weighs a capture that has no usable collector number: the
// copyright year, and the markings the card prints, against the candidate
// printings.
//
// solePrintCounts is false when a number *was* read and matched nothing. The
// difference is not cosmetic — see the call site.
func rankWithoutNumber(cards []scryfall.Card, year int, border, finish string,
	solePrintCounts bool,
) ([]scryfall.Card, scanMatch) {
	if solePrintCounts {
		if len(cards) == 1 {
			return cards, scanMatchSinglePrint
		}
		if ranked, ok := collapseVariants(cards); ok {
			return ranked, scanMatchSinglePrint
		}
	}
	// Old frames print no collector number the band can reach, so most of
	// what queues here queues for want of *any* printing evidence — and
	// the copyright line has been carrying some all along. Its range end
	// is the printing's release year, which on a card reprinted years
	// apart names exactly one printing on its own.
	//
	// Weaker than a number, and deliberately ranked below one: the year is
	// four small italic digits, the same glyphs that turn "30" into "80",
	// so it only ever picks between printings rather than confirming a
	// card. Ambiguity fails closed — zero or several printings in that
	// year leave the card queued exactly as before.
	if year > 0 {
		if i := soleIndexInYear(cards, allIndexes(len(cards)), year); i >= 0 {
			return moveToFront(cards, i), scanMatchYearOnly
		}
		// The year found several, so ask the border which of those it is.
		//
		// This is the case the whole stratum turns on. A 1995 copyright
		// line narrows Prodigal Sorcerer's 23 printings to `4ed/94` and
		// `4bb/94` — same set, same collector number, same year, differing
		// only in that one is white-bordered and the other black. Nothing
		// else printed on the card can separate them, so before this the
		// year did all the work it could and the card still queued.
		//
		// Deliberately reached only *after* the year has already narrowed
		// the field. The border is one bit and would settle almost nothing
		// on its own; it is decisive here precisely because the year has
		// left a handful of candidates for it to choose between.
		//
		// And the survivor must *match* the read, not merely survive it.
		// That distinction was missing and it committed a wrong card live:
		// Mana Leak's 1998 line is `sth/36` black and `wc98/rb36` gold, the
		// reader said white, black was ruled out, and gold — which the
		// reader cannot read and so never excludes — was left standing
		// alone and committed as a World Championship printing. Survival is
		// not agreement. A card picked purely by eliminating its siblings
		// has no positive evidence behind it, and the one printing that can
		// never be eliminated is exactly the one to distrust.
		if border != "" || finish != "" {
			kept := make([]int, 0, len(cards))
			for i, c := range cards {
				if borderRulesOut(c, border) || finishRulesOut(c, finish) {
					continue
				}
				kept = append(kept, i)
			}
			// Fails closed twice over: if the markings ruled nothing out
			// they added no information, and if they ruled everything out
			// they disagree with every printing the catalog has, which is a
			// reason to distrust the read rather than to pick from nothing.
			if len(kept) > 0 && len(kept) < len(cards) {
				if i := soleIndexInYear(cards, kept, year); i >= 0 &&
					markingsAgree(cards[i], border, finish) {
					return moveToFront(cards, i), scanMatchYearAndMarks
				}
			}
		}
	}
	return cards, scanMatchNone
}

// rankMatchedNumber weighs a capture whose collector number matched at least
// one printing: which of them it is, and how much the agreement is worth.
func rankMatchedNumber(cards []scryfall.Card, matchIdxs []int, best int,
	exactSet, langAgrees bool, year int, border, finish string,
) ([]scryfall.Card, scanMatch) {
	yearPinned := false
	if !exactSet && len(matchIdxs) > 1 && year > 0 {
		if inYear := soleIndexInYear(cards, matchIdxs, year); inYear >= 0 {
			best, yearPinned = inYear, true
		}
	}
	// The markings break a number tie the same way the year does.
	//
	// A collector number is not unique across printings — `dom/76` sits beside
	// `pdom/76` and `pdom/76s`, the two promos, and a card that read a clean
	// 076/269 still queued as "number matched, but several printings share it".
	// Both promos are foil-only, so the bullet on the card ruled them out and
	// nothing was asking it to.
	//
	// Narrowing only ran when there was *no* number at all, which is backwards:
	// a number that matched several printings is exactly the situation where a
	// second signal is worth having.
	markPinned := false
	if !exactSet && !yearPinned && len(matchIdxs) > 1 && (border != "" || finish != "") {
		kept := make([]int, 0, len(matchIdxs))
		for _, i := range matchIdxs {
			if borderRulesOut(cards[i], border) || finishRulesOut(cards[i], finish) {
				continue
			}
			kept = append(kept, i)
		}
		// Exactly one survivor, and it has to agree rather than merely survive.
		if len(kept) == 1 && markingsAgree(cards[kept[0]], border, finish) {
			best, markPinned = kept[0], true
		}
	}
	// The year is checked against the winner even when it had no tie to break.
	// A number that named one printing is one piece of evidence; the same
	// printing's release year agreeing with the copyright line is a second,
	// and two independent agreements are what let a mangled title through.
	// Live: "Stemal Dragon" resolved to Eternal Dragon at 76% similarity and
	// queued, while the band read a clean 12/143 and the copyright said 2003 —
	// Scourge 12, released 2003, agreeing twice over.
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

// sameCardFloor is how soon after seeing a printing another sighting of it can
// possibly be a second physical card.
//
// Under it, a repeat is the card that is already there — jostled, or re-read
// after the trigger mistook motion for a placement — and committing it writes a
// copy nobody owns.
//
// The number is measured, not chosen. Over 60 recorded result-to-capture gaps
// (the session `nudgeDelay` was fitted from), a human swapping a card was
// *never* faster than 3856ms. The session that prompted this floor committed
// eight duplicates whose same-printing gaps were 0.91, 0.93, 0.93, 0.96, 1.60
// and 2.60s — every one of them below the fastest swap a person has been
// observed making, and one card committed five times in six seconds while
// sitting still on the desk.
//
// 3s sits above every false repeat and below the fastest real one, so a
// deliberate playset scanned at human speed still commits every copy. Two
// copies visible in a *single* frame are a different case entirely and are
// never subject to this — see the captureSeq branch in onResolveDone.
const sameCardFloor = 3 * time.Second

// placementFaceFloor is how far a card's face must sit from the one already
// read before the source's claim that this is a different card outranks
// sameCardFloor.
//
// The floor above is a fallback standing in for evidence the source could not
// give. It no longer cannot. The phone measures the nearest in-frame card
// against the one it shot and calls the result `moved` below its own
// movedFaceMax (20.0) or `replaced` above it — the exact question the clock
// was approximating. Where that measurement is decisive it wins, because it
// looked at the cards and the clock only counted.
//
// Set at movedFaceMax plus a 25% margin rather than at it. Two reasons: the
// phone's threshold is interpolated rather than measured, and exactly one
// `moved` sample exists to fit against. A marginal `replaced` is therefore
// still handed to the clock, which is the conservative answer — the cost is a
// keystroke on the recovery affordance, not a lost card.
//
// Observed over 19 fires in one session: the sole `moved` reading was 15.8;
// real placements ran 20.1 through 44.1, eight of nine at 26.4 or above. The
// drop this exists to stop read 32.5. Re-fit it from live `face=` traces
// before trusting it further; see docs/scanner-tuning.md.
const placementFaceFloor = 25.0

// placedDecisively reports whether the source watched a different card arrive
// and measured the difference convincingly.
//
// Only `replaced` qualifies. `removed` says the captured card left the watched
// rect, which is equally what picking a card up to look at it and setting it
// back down does — it is evidence of motion, not of identity. And on
// `removed` there is by construction no face to have measured, since the
// comparison only runs when a box is sitting on the spot.
func (it queueItem) placedDecisively() bool {
	return it.fromReplaced && it.faceDelta != nil && *it.faceDelta >= placementFaceFloor
}

// pendingDup is the last repeat sighting the duplicate rules suppressed, held
// so the operator can overrule them with one key.
//
// Every rule above is a judgement about a physical act nobody in this process
// witnessed, and each is calibrated on a handful of live sessions —
// placementFaceFloor on a single negative sample. They will be wrong
// sometimes, and a wrong drop today costs a card: no write, no sound, no
// review entry, nothing to act on. That asymmetry is the problem, not the
// error rate. Holding the item turns being wrong into a keystroke.
//
// One slot rather than a queue, deliberately. The question it answers is "was
// that a second copy", which is only ever about the sighting just suppressed;
// a backlog of them would be a review queue with worse ergonomics, and the
// review queue already exists.
type pendingDup struct {
	it     queueItem
	finish string
	at     time.Time
}

// pendingDupWindow is how long `+` can still promote a suppressed sighting.
//
// A backstop, not the mechanism: the slot is normally cleared by the next card
// to commit or queue, because by then the operator has moved on and "that was
// a second copy" no longer refers to anything they can see. This bounds the
// other case — a session left sitting — and is set to comfortably cover
// reading the status line and deciding, since the line itself is the prompt.
const pendingDupWindow = 30 * time.Second

type recentCommit struct {
	scryfallID string
	finish     string
	captureSeq int
	at         time.Time
	// finishGuessed records that nothing on the card chose this finish — it
	// is the nonfoil default. A later look that *does* carry a marker is
	// better evidence than this row, and finishConflict finds that case.
	finishGuessed bool
}

// dupCapture reports whether the same printing was seen within the time
// window, by which capture, and how long ago — the discriminators between a
// fanned playset, a lingering neighbour, and a card that only moved.
//
// It answers with the *most recent* match. It used to answer with the first one
// in the slice, which is the oldest: with five sightings of one card banked, the
// age it reported was the age of the first, so any question about "how long
// since I last saw this" got an answer several seconds too large. That is a bug
// on its own and became load-bearing the moment sameCardFloor existed.
//
// The printing alone, not printing-and-finish. Keyed on the finish too, a
// re-read whose finish verdict merely *changed* skipped every duplicate rule —
// including the source's own "it only moved" — and committed a second row.
// Observed live twice in one session: Brainsurge committed nonfoil, then foil
// 800ms later off a capture the source flagged as moved. Whether the finish
// difference means a correction or a second copy is a judgement for the
// caller, made *after* the physical-identity rules have run, which is why the
// whole sighting is returned rather than its capture number.
func dupCapture(recent []recentCommit, id string, now time.Time) (prior recentCommit, since time.Duration, dup bool) {
	for i := len(recent) - 1; i >= 0; i-- {
		rc := recent[i]
		if rc.scryfallID == id && now.Sub(rc.at) <= dupWindow {
			return rc, now.Sub(rc.at), true
		}
	}
	return recentCommit{}, 0, false
}

// recordCommit appends to the window, pruning it to a fixed size.
func recordCommit(recent []recentCommit, id, finish string, captureSeq int, now time.Time,
	finishGuessed bool) []recentCommit {
	recent = append(recent, recentCommit{scryfallID: id, finish: finish,
		captureSeq: captureSeq, at: now, finishGuessed: finishGuessed})
	if len(recent) > dupKeep {
		recent = recent[len(recent)-dupKeep:]
	}
	return recent
}

// touchCommit re-stamps the most recent sighting of a printing without banking
// a new one, so sameCardFloor measures from the last time the card was *seen*
// rather than the last time it was written.
//
// The difference is the whole guard. A card re-read every second anchors each
// repeat on the original commit otherwise, so the third repeat lands outside a
// three-second floor and commits — which is exactly the burst this exists to
// stop (five commits of one card at 0.93s, 1.60s, 0.93s and 2.60s apart: two
// suppressed, two through). Rolling the anchor forward suppresses all of them
// for as long as the card keeps announcing itself.
func touchCommit(recent []recentCommit, id string, now time.Time) []recentCommit {
	for i := len(recent) - 1; i >= 0; i-- {
		if recent[i].scryfallID == id {
			recent[i].at = now
			return recent
		}
	}
	return recent
}

// rekeyCommit restates a banked sighting under the finish a later look proved,
// so the duplicate window keeps agreeing with the row the adder now holds.
func rekeyCommit(recent []recentCommit, id, from, to string, now time.Time) []recentCommit {
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
	_, ok := seenWithin(recent, name, now)
	return ok
}

// seenWithin is seenRecently plus how long ago, taken from the *most recent*
// sighting — the same newest-wins rule dupCapture follows, and for the same
// reason: a card seen five times running would otherwise report the age of the
// first sighting and read as older than it is.
func seenWithin(recent []recentName, name string, now time.Time) (time.Duration, bool) {
	for i := len(recent) - 1; i >= 0; i-- {
		rn := recent[i]
		if now.Sub(rn.at) <= dupWindow && strings.EqualFold(rn.name, name) {
			return now.Sub(rn.at), true
		}
	}
	return 0, false
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
// a new card commits normally.
//
// Only the nudge's own echoes are swallowed here. An identical read fired by
// real disruption goes to the duplicate rules instead, which commit it when
// the source can show a placement happened — so a deliberately stacked
// playset copy is never lost to this.

// nudgeMsg fires when the post-processing quiet period elapses. gen ties it
// to the scheduling generation; any newer scan or schedule voids it.
type nudgeMsg struct{ gen int }

// flashDeadlineMsg is the decision ceiling lapsing on a held review flash.
// Scoped by the card's name rather than a generation: the deadline is void
// exactly when the flash it guards is no longer held for that card, and
// clearDeferredFlash already encodes every way that happens.
type flashDeadlineMsg struct{ name string }

// decisionCeiling is how long a queued card may hold its review flash while a
// second look is out, before the operator is told regardless.
//
// The nudge timer already flushes the held flash, but it answers a different
// question — "has the scene gone quiet" — at a swap-cadence 5.5s that echo
// backoff can double. As a *decision* deadline that is the wrong clock:
// measured across four live sessions, every second look that ever rescued a
// card landed within 0.9s of the queue (0.70-0.90s), and no retry that missed
// that window ever answered — the late reads that did arrive came mangled off
// a nudge and were dropped. 1.3s keeps every observed rescue with ~40%
// margin; the nudge stays armed behind it as the capture backstop it always
// was.
//
// 1300 starts tight by choice — the operator's call, against the measured
// 0.9s rescue cap. The number to watch in the session log is a rescue
// ("re-read ... beats the queued ..." or a second-look commit) landing
// *after* its card's "no better read within" line: that is the ceiling
// cutting into real rescues, and the answer is to raise this a step
// (1300 → 1800 → 2500), not to doubt the rescue. The four sessions behind
// the 0.9s figure share one operator and one rig.
const decisionCeiling = 1300 * time.Millisecond

// nudgeEchoWindow is how long after a sent nudge a scan counts as possibly
// nudge-originated. A window rather than a consumed flag, because a real scan
// can race the nudge onto the wire; a new card inside the window is never
// dropped (its name differs from the last processed card), so the width only
// bounds how stale an echo can be.
const nudgeEchoWindow = 4 * time.Second

// nudgeBackoffSteps caps how far consecutive echoes push the quiet period out,
// doubling each time: 5.5s, 11s, 22s, 44s, and no further.
//
// The cap exists because the ceiling is the only cost. A card that arrives
// while the timer is parked does not wait for it — the phone fires on
// disruption and the capture voids the pending generation — so the back-off
// only ever governs the case geometry cannot see, a card stacked squarely on
// the pile. Four doublings is enough that a genuinely abandoned session stops
// asking, and short enough that the one blind case is not left for a minute.
const nudgeBackoffSteps = 3

// nudgeBackoff is the quiet period after `drops` consecutive echoes.
//
// Split out from scheduleNudge because the command it builds closes over a
// sleep, so the delay is unobservable from a test once it is in there — and
// this is the arithmetic that decides whether a parked card polls forever.
func nudgeBackoff(drops int) time.Duration {
	if drops < 0 {
		drops = 0
	}
	return nudgeDelay << min(drops, nudgeBackoffSteps)
}

// scheduleNudge arms the quiet-period timer for the current generation, backing
// off once per consecutive echo.
//
// The recheck used to be armed once per processed scan and never re-armed by
// its own echo, so a parked card got exactly one look. That was not what the
// code did: the echo path returned no command at all, which ended the loop
// rather than bounding it, and a suppressed card then waited on the phone's own
// re-arm — 73.5s in one observed session.
//
// Rescheduling unconditionally is the fix and the back-off is what makes it
// affordable. nudgeDrops counts consecutive echoes and is zeroed by any card
// that commits or queues, so the delay widens only while the scanner keeps
// being shown something it has already handled.
func (m *model) scheduleNudge() tea.Cmd {
	m.nudgeGen++
	gen := m.nudgeGen
	delay := nudgeBackoff(m.nudgeDrops)
	return func() tea.Msg {
		time.Sleep(delay)
		return nudgeMsg{gen: gen}
	}
}

// wantsSecondLook reports whether this queue-bound card should be photographed
// again promptly rather than waiting out the operator's swap window.
//
// Only for an unverified printing: that is the failure a second photograph
// actually fixes, because the collector number lives in four small glyphs at the
// bottom edge and whether they survive is a property of the capture rather than
// of the card. The other queue reasons — an unreadable name, a failed lookup —
// are not improved by looking again at the same rate, and asking would only
// spend captures.
//
// Bounded by name, to one look. A card that will never read must not hold the
// session open re-photographing itself, and the retry that already happened is
// exactly what m.secondLookFor remembers.
func (m model) wantsSecondLook(it queueItem) bool {
	if it.canonical == "" || m.secondLookFor == it.canonical {
		return false
	}
	short, _ := printingUnverified(it)
	return short
}

// onNudge sends the rearm if the session is still quiet and capable.
//
// It is also where a held review signal runs out of patience. Reaching here at
// all means the quiet period elapsed with no capture in it — any real one bumps
// nudgeGen and voids this timer — so a retry that was going to answer has not,
// and the card in the queue is owed the flash it did not get. Every other exit
// from a held flash is an event; this is the one that is a timeout.
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
