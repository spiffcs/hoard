package command

import (
	"context"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
)

func deckCountedFor(t *testing.T, st *store.Store, name string) bool {
	t.Helper()
	decks, err := st.ListDecks()
	if err != nil {
		t.Fatalf("ListDecks: %v", err)
	}
	for _, d := range decks {
		if d.Name == name {
			return d.Counted
		}
	}
	t.Fatalf("no deck named %q", name)
	return false
}

func TestDeckExcludeAndIncludeFromTheCLI(t *testing.T) {
	ctx := context.Background()
	st := folderCmdStore(t)

	if !deckCountedFor(t, st, "Atraxa") {
		t.Fatal("an imported deck should start counted")
	}

	if _, err := execCmd(ctx, st, []string{"deck", "exclude", "Atraxa"}, false); err != nil {
		t.Fatalf("deck exclude: %v", err)
	}
	if deckCountedFor(t, st, "Atraxa") {
		t.Error("deck exclude did not stop the deck counting")
	}

	if _, err := execCmd(ctx, st, []string{"deck", "include", "Atraxa"}, false); err != nil {
		t.Fatalf("deck include: %v", err)
	}
	if !deckCountedFor(t, st, "Atraxa") {
		t.Error("deck include did not restore it")
	}
}

func TestSummaryMarksAnUncountedDeck(t *testing.T) {
	ctx := context.Background()
	st := folderCmdStore(t)
	if _, err := execCmd(ctx, st, []string{"deck", "exclude", "Atraxa"}, false); err != nil {
		t.Fatalf("deck exclude: %v", err)
	}

	out, err := execCmd(ctx, st, []string{"report"}, false)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !strings.Contains(out, "Atraxa") {
		t.Fatalf("report lost the deck:\n%s", out)
	}
	marked := ""
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "Atraxa") {
			marked = line
		}
	}
	if !strings.Contains(marked, "*") {
		t.Errorf("uncounted deck carries no marker:\n%s", out)
	}
}
