package tui

// The art-identification channel: when a card queues unverified, the Mac
// hashes the picture off the still it already received and matches it against
// the perceptual-hash index of every printing's Scryfall image. OCR never
// touches the art, so the glare that eats the copyright line — the scanner's
// dominant live failure — cannot touch this evidence, and same-name printings
// differ in art or frame, which is exactly what the text ranks cannot
// separate. The result re-enters resolution as a synthetic re-read at
// scanMatchArt, so upgradeQueued and the commit path treat it like any other
// better look.

import (
	"context"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/artindex"
	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/scryfall"
)

// Fail-closed acceptance gates.
//
// UNFITTED AT THE CURRENT FOOTPRINT — these are the values that were fitted
// against the 64-bit hash, and the hash is now 256 bits (2026-08-08). Every
// distance they are compared against is drawn from a range four times wider,
// so as written they reject nearly everything. That is the safe direction to
// be wrong in — a rejected match leaves the card queued, exactly as if the
// channel did not exist, while an accepted wrong one commits a wrong printing
// — and it is why they were not rescaled by arithmetic here. Guessing a gate
// that ACCEPTS is the one move this channel must never make.
//
// docs/sprint-artmatch-v2.md Stage C refits them from the measured
// distributions: HOARD_MINI_EVAL=1 first for the go/no-go, then
// HOARD_ARTMATCH_EVAL=1 across all ~107k printings, with zero wrong-printing
// matches as the bar. Until then this channel stays inert, which it already
// was for an unrelated reason (the audit's B7: stills only land in
// HOARD_SCAN_DEBUG_DIR).
//
// For scale when reading the eval output: at 256 bits the same synthetic image
// survives scale and brightness shifts within 6 bits while distinct images sit
// 104+ bits apart (phash_test.go). The live numbers will be worse than both.
const (
	artAcceptDistance = 10
	artAcceptMargin   = 8
)

// artMatchTimeout bounds the whole side-channel: waiting for the still to
// land, the probe's flatten, the hash, the lookup. It must finish inside
// decisionCeiling (1000ms) with room for the resolve it triggers.
const artMatchTimeout = 700 * time.Millisecond

// cardsByID is the optional searcher capability the art channel needs: a
// match answers with a Scryfall id, not a name. The local catalog satisfies
// it; a searcher that cannot simply leaves the channel off, the same shape as
// blockSearcher.
type cardsByID interface {
	Cards(ids []string) (map[string]scryfall.Card, error)
}

// artMatcher is the loaded index plus everything the background command needs.
type artMatcher struct {
	index    *artindex.Index
	byID     cardsByID
	stillDir string
	probe    string
	// sem bounds concurrent probe processes. One command spawns per queued
	// card with no other limit, and a burst of bad reads (a pile session's
	// signature) launched a probe per card at once — each a full process
	// with an image decode behind it. Slots beyond two buy nothing: the
	// still-wait serializes most of the window anyway.
	sem chan struct{}
}

// newArtMatcher wires the channel when every piece is present: a built index,
// a by-id searcher, a stills directory, and the probe binary for the flatten.
// Any absence returns nil and the scanner behaves as before.
func newArtMatcher(s Searcher) *artMatcher {
	byID, ok := s.(cardsByID)
	if !ok {
		return nil
	}
	dir := os.Getenv("HOARD_SCAN_DEBUG_DIR")
	if dir == "" {
		return nil
	}
	probe := probePath()
	if probe == "" {
		return nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil
	}
	ix, err := artindex.Open(filepath.Join(cache, "hoard", "artindex"))
	if err != nil || ix.Count() == 0 {
		if ix != nil {
			ix.Close()
		}
		return nil
	}
	return &artMatcher{index: ix, byID: byID, stillDir: dir, probe: probe,
		sem: make(chan struct{}, 2)}
}

func (am *artMatcher) close() {
	if am != nil && am.index != nil {
		am.index.Close()
	}
}

// probePath finds cardkit-probe the way the replay harness does: the env
// override first, then the repo-relative build product.
func probePath() string {
	if p := os.Getenv("HOARD_REPLAY_HELPER"); p != "" {
		return p
	}
	for _, p := range []string{"bin/cardkit-probe", "../../bin/cardkit-probe"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// artDecisive is the fail-closed acceptance: the winner must be close in
// absolute terms AND clearly ahead of the runner-up. A near-tie is exactly
// the case a wrong pick would come from — two printings sharing art, or a
// blurred capture equidistant from everything — and both leave the card
// queued, which costs nothing this channel did not already cost.
func artDecisive(best, second artindex.Match) bool {
	return best.Distance <= artAcceptDistance &&
		second.Distance-best.Distance >= artAcceptMargin
}

// artMatchMsg carries a decisive match back to the update loop as a synthetic
// resolution for the queued card.
type artMatchMsg struct {
	gen  int
	item queueItem
}

// artMatchCmd runs the whole side-channel off the update loop: wait briefly
// for the capture's still to land in the debug dir, borrow the probe's
// flatten, hash, match, and — only on a decisive winner — hand back a
// synthetic re-read. Every failure path returns nil silently: this channel
// must never cost the session anything but the goroutine.
func (m model) artMatchCmd(it queueItem, notBefore time.Time) tea.Cmd {
	am := m.art
	if am == nil {
		return nil
	}
	gen := m.resolveGen
	captureSeq := it.captureSeq
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), artMatchTimeout)
		defer cancel()

		// A slot before any work: the timeout keeps running while queued
		// here, so a probe that cannot start in time simply never starts.
		select {
		case am.sem <- struct{}{}:
			defer func() { <-am.sem }()
		case <-ctx.Done():
			return nil
		}

		still := waitForStill(ctx, am.stillDir, notBefore)
		if still == "" {
			return nil
		}
		// One crop file per capture, never shared: with a fixed name, two
		// cards queued in the same window raced the same path, and card B
		// could hash card A's crop — art corroboration then committed the
		// wrong printing (and A's deferred remove deleted B's crop mid-read).
		cropFile, err := os.CreateTemp("", fmt.Sprintf("hoard-artmatch-%d-*.png", captureSeq))
		if err != nil {
			return nil
		}
		crop := cropFile.Name()
		cropFile.Close()
		defer os.Remove(crop)
		// Exit 3 is "nothing readable", which does not matter here — the
		// crop is emitted before the reading is judged. Exit 4 is "no card
		// located", which does: no crop, nothing to hash.
		cmd := exec.CommandContext(ctx, am.probe, "--image", still, "--emit-card", crop)
		_ = cmd.Run()
		f, err := os.Open(crop)
		if err != nil {
			return nil
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			return nil
		}
		best, second := am.index.Best(artindex.FromImage(img))
		// A one-entry index cannot render a decisive verdict: the runner-up
		// is Best's sentinel, seeded above every reachable distance, so any
		// image inside the distance gate clears the margin unopposed. Two
		// entries is the least field a margin means anything against.
		if am.index.Count() < 2 || !artDecisive(best, second) {
			return nil
		}
		cards, err := am.byID.Cards([]string{best.ScryfallID})
		if err != nil {
			return nil
		}
		card, ok := cards[best.ScryfallID]
		if !ok {
			return nil
		}
		out := queueItem{
			id:         it.id,
			raw:        it.raw,
			siblings:   1,
			captureSeq: captureSeq,
			canonical:  card.Name,
			ocrLine:    it.ocrLine,
			match:      cardname.Match{Exact: true},
			prints:     []scryfall.Card{card},
			rank:       scanMatchArt,
			finishHint: it.finishHint,
		}
		// The chosen row is the match's own identity, so the log's
		// "(read …)" divergence marker stays honest.
		out.raw.SetCode, out.raw.CollectorNumber = card.Set, card.CollectorNumber
		return artMatchMsg{gen: gen, item: out}
	}
}

// waitForStill polls for a still newer than the capture that queued — they
// arrive right behind their scan event, but a 2MB frame takes real time to
// cross the link.
func waitForStill(ctx context.Context, dir string, notBefore time.Time) string {
	for {
		entries, _ := filepath.Glob(filepath.Join(dir, "remote-still-*.jpg"))
		var newest string
		var newestAt time.Time
		for _, p := range entries {
			fi, err := os.Stat(p)
			if err != nil || fi.ModTime().Before(notBefore) {
				continue
			}
			if fi.ModTime().After(newestAt) {
				newest, newestAt = p, fi.ModTime()
			}
		}
		if newest != "" {
			return newest
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(50 * time.Millisecond):
		}
	}
}
