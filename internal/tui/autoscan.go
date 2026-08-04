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

// The order is the strength order: resolveCardCmd keeps the highest-ranking
// candidate, so set+number sits at the top. It is self-consistent evidence —
// a name fuzzy-resolved to the wrong card could not have its number match
// that card's printings — and must outrank the no-number sentinel, which is
// only ever a floor.
const (
	scanMatchNone            scanMatch = iota
	scanMatchNumberAmbiguous           // number matched, but several printings share it
	scanMatchYearOnly                  // no number, but the copyright year names one printing
	scanMatchNumberOnly                // number matched exactly one printing
	scanMatchSinglePrint               // no number read, but only one printing exists
	scanMatchNumberAndYear             // number named a printing and its release year agrees
	scanMatchSetAndNumber              // set and number both matched
)

// numberVerified reports whether a collector number actually matched a
// printing of the resolved card. That is the corroboration the weaker gates
// defer to: a wrong name could not have produced a number that agrees with it.
// The no-number ranks are excluded — single-print and year-only never had a
// number to check.
func numberVerified(r scanMatch) bool {
	return r == scanMatchSetAndNumber || r == scanMatchNumberAndYear ||
		r == scanMatchNumberOnly
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
	return r == scanMatchSetAndNumber || r == scanMatchNumberAndYear
}

// String names the match for the telemetry log.
func (m scanMatch) String() string {
	switch m {
	case scanMatchNumberAmbiguous:
		return "number-ambiguous"
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
	// numberOverridden records that a collector number was read but no
	// candidate carrying one verified, so the card rests on its name and a
	// single printing instead. Rare and worth auditing: it is the one path
	// that commits in spite of collector evidence rather than because of it.
	numberOverridden bool
	// viaBlock records that the card was identified from its collector block
	// because no line of text resolved — the title never read at all.
	viaBlock bool
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

// blockSearcher is implemented by searchers that can resolve a printing from
// its collector block alone.
type blockSearcher interface {
	PrintBySetNumber(ctx context.Context, set, number string) (*scryfall.Card, error)
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
		{Number: c.CollectorNumber, Set: c.SetCode, Finish: c.FinishHint, Source: c.NumberSource}},
		c.CollectorAlts...)
	for _, b := range blocks {
		if b.Set == "" || b.Number == "" || b.Source == "copyright" {
			continue
		}
		card, err := byBlock.PrintBySetNumber(ctx, b.Set, b.Number)
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

// remoteNameTimeout bounds a Scryfall lookup. The catalog has already answered
// by this point; the network call only catches a card printed since the last
// catalog build, which is a bonus and must never be a stall. Past this the card
// queues, which is a far better outcome than a session that freezes.
const remoteNameTimeout = 500 * time.Millisecond

// searchLine resolves one OCR line, choosing how far off-machine it may go.
// Fallback lines stay catalog-only (verdict refuses to auto-commit them
// anyway); line 0 escalates only when it reads like a title.
func searchLine(ctx context.Context, s Searcher, local localOnlySearcher,
	hasLocal bool, line string, i int) (*scryfall.Card, cardname.Match, error) {
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
	rctx, cancel := context.WithTimeout(ctx, remoteNameTimeout)
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
				// A number was printed on the wire and none of the candidates
				// carrying one verified: whatever committed rests on the name
				// and a lone printing.
				it.numberOverridden = it.raw.CollectorNumber == "" &&
					slices.ContainsFunc(cands, func(cd scan.CollectorAlt) bool {
						return cd.Number != ""
					})
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
	if len(it.prints) == 0 {
		return false, "", "no printings found"
	}
	switch it.rank {
	case scanMatchSetAndNumber, scanMatchNumberAndYear, scanMatchNumberOnly,
		scanMatchSinglePrint, scanMatchYearOnly:
	default:
		return false, "", fmt.Sprintf("printing unverified: %d printings", len(it.prints))
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
		if inYear := soleIndexInYear(cards, matchIdxs, year); inYear >= 0 {
			best, yearPinned = inYear, true
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
	case exactSet:
		return ranked, scanMatchSetAndNumber
	case (len(matchIdxs) == 1 || yearPinned) && yearAgrees:
		return ranked, scanMatchNumberAndYear
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
	// finishGuessed records that nothing on the card chose this finish — it
	// is the nonfoil default. A later look that *does* carry a marker is
	// better evidence than this row, and finishConflict finds that case.
	finishGuessed bool
}

// finishConflict reports when this look carries a printed finish marker that
// contradicts a finish we recorded moments ago *by default*. It is the case
// where the echo swallow would otherwise throw away the better evidence: the
// first look at a card saw no marker and committed the nonfoil default, and
// the second look — the one the recheck nudge fired — actually read one.
// Observed live on a foil Inspired Fire, recorded nonfoil.
//
// Deliberately not a silent correction. Two copies of a card, one foil and one
// not, scanned back to back look exactly like this, and rewriting the first row
// would be as wrong as dropping the second. The caller queues it instead, which
// is the only outcome that survives both readings.
func finishConflict(recent []recentCommit, it queueItem, now time.Time) (was string, ok bool) {
	if it.finishHint == "" || len(it.prints) == 0 {
		return "", false
	}
	card := it.prints[0]
	if !slices.Contains(finishOptions(card), it.finishHint) {
		return "", false
	}
	for i := len(recent) - 1; i >= 0; i-- {
		r := recent[i]
		if r.scryfallID != card.ID || now.Sub(r.at) > dupWindow {
			continue
		}
		// Only the most recent commit of this printing matters; an older one
		// has already been superseded by it.
		if r.finishGuessed && r.finish != it.finishHint {
			return r.finish, true
		}
		return "", false
	}
	return "", false
}

// correctRecentFinish restates the finish of the latest commit of a printing,
// and marks it evidenced so the correction is not itself reconsidered by the
// next look — the card has now told us, and a later capture that fails to read
// the marker is silence, not contradiction.
func correctRecentFinish(recent []recentCommit, id, to string) []recentCommit {
	for i := len(recent) - 1; i >= 0; i-- {
		if recent[i].scryfallID == id {
			recent[i].finish = to
			recent[i].finishGuessed = false
			return recent
		}
	}
	return recent
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
func recordCommit(recent []recentCommit, id, finish string, captureSeq int, now time.Time,
	finishGuessed bool) []recentCommit {
	recent = append(recent, recentCommit{scryfallID: id, finish: finish,
		captureSeq: captureSeq, at: now, finishGuessed: finishGuessed})
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
