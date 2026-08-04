package tui

// An end-to-end replay of a captured session: every saved frame goes through
// the real helper, the real resolution, and the real verdict against the real
// catalog, and reports what the scanner would have done with it.
//
// The fixture sweep (scan/fixtures/sweep.sh) proves what the *helper* reads.
// It cannot prove the thing a session is actually judged on — how many cards
// land in review — because that decision is all on this side of the wire. This
// closes that gap, so "the tool got better" can be measured against the very
// frames a session queued rather than argued from the code.
//
// Skipped unless pointed at a session, since it needs both a built helper and
// a populated catalog:
//
//	HOARD_REPLAY_FRAMES=/path/to/capture-frames \
//	go test ./internal/tui -run TestSessionReplay -v
//
// Optional: HOARD_REPLAY_CATALOG (defaults to the user cache location),
// HOARD_REPLAY_HELPER (defaults to bin/hoard-scan.app in the repo).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/scan"
)

func replayHelper(t *testing.T) string {
	t.Helper()
	if h := os.Getenv("HOARD_REPLAY_HELPER"); h != "" {
		return h
	}
	return filepath.Join("..", "..", "bin", "hoard-scan.app", "Contents", "MacOS", "hoard-scan")
}

func replayCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	dir := os.Getenv("HOARD_REPLAY_CATALOG")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("no home directory to find a catalog in: %v", err)
		}
		dir = filepath.Join(home, "Library", "Caches", "hoard", "catalog")
	}
	cat, err := catalog.Open(dir)
	if err != nil {
		t.Skipf("no catalog at %s: %v", dir, err)
	}
	if cat.CardCount() == 0 {
		t.Skipf("catalog at %s is empty", dir)
	}
	return cat
}

// frameOrder sorts capture-N by N so the report reads in capture order rather
// than lexically (capture-10 before capture-2).
func frameOrder(paths []string) {
	num := func(p string) int {
		base := filepath.Base(p)
		base = strings.TrimSuffix(strings.TrimPrefix(base, "capture-"), "-ocr.png")
		n, _ := strconv.Atoi(base)
		return n
	}
	sort.Slice(paths, func(i, j int) bool { return num(paths[i]) < num(paths[j]) })
}

func TestSessionReplay(t *testing.T) {
	dir := os.Getenv("HOARD_REPLAY_FRAMES")
	if dir == "" {
		t.Skip("set HOARD_REPLAY_FRAMES to a session's capture frames to replay it")
	}
	helper := replayHelper(t)
	if _, err := os.Stat(helper); err != nil {
		t.Skipf("helper not built at %s — run: make scan", helper)
	}
	cat := replayCatalog(t)
	defer cat.Close()

	frames, err := filepath.Glob(filepath.Join(dir, "capture-*-ocr.png"))
	if err != nil || len(frames) == 0 {
		t.Skipf("no capture-*-ocr.png frames in %s", dir)
	}
	frameOrder(frames)

	var committed, queued, killed int
	for _, frame := range frames {
		out, err := exec.Command(helper, "--image", frame, "--rotate", "0").Output()
		if err != nil {
			// Exit code 3 is the helper's "nothing readable", not a failure.
			if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 3 {
				t.Errorf("%s: helper failed: %v", filepath.Base(frame), err)
				continue
			}
		}
		var ev scan.Event
		if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &ev); err != nil {
			continue
		}
		cards := ev.CardList()
		label := strings.TrimSuffix(filepath.Base(frame), "-ocr.png")
		if len(cards) == 0 {
			t.Logf("%-12s (nothing readable)", label)
			continue
		}
		for _, c := range cards {
			m := newModel(context.Background(), cat, noopAdder, &fakeScanner{}, "", nil)
			msg := m.resolveCardCmd(1, c, len(cards))().(resolveDoneMsg)
			it := msg.item

			// The phantom rules live in onResolveDone rather than verdict, so
			// mirror the one that decides whether an entry is a card at all.
			if it.canonical == "" && it.errText == "" && len(cards) > 1 && !hasCollectorBlock(it.raw) {
				killed++
				t.Logf("%-12s KILLED   %q", label, it.ocrLine)
				continue
			}
			auto, finish, note := verdict(it)
			// The border the helper read, and — for a pre-1998 pile, where the
			// pile itself is the ground truth — the set now sitting on top of
			// the queue because of it. That top row is what the user accepts
			// with one keystroke, so it is the thing whose accuracy has to be
			// scored before border is ever allowed to commit on its own.
			border := ""
			if it.raw.BorderColor != "" {
				border = fmt.Sprintf("  border=%s", it.raw.BorderColor)
				if !it.borderFiltered {
					border += "(unused)"
				} else if len(it.prints) > 0 {
					border += fmt.Sprintf("→%s", strings.ToUpper(it.prints[0].Set))
				}
			}
			if auto {
				committed++
				t.Logf("%-12s COMMIT   %s (%s/%s) %s  [rank=%s]%s", label, it.prints[0].Name,
					strings.ToUpper(it.prints[0].Set), it.prints[0].CollectorNumber, finish,
					it.rank, border)
				continue
			}
			queued++
			name := it.canonical
			if name == "" {
				name = strconv.Quote(it.ocrLine)
			}
			t.Logf("%-12s QUEUE    %-30s %s  [rank=%s]%s", label, name, note, it.rank, border)
		}
	}
	t.Logf("--- %d committed, %d queued, %d killed ---", committed, queued, killed)
}
