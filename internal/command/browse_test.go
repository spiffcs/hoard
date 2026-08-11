package command

import (
	"context"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/decksource"
	"github.com/spiffcs/hoard/internal/pricing"
)

// browseDeck runs a decklist through the real text parser, so the Skipped
// lines these tests assert on are the ones the parser actually produces
// rather than a hand-built fixture. This is the same list `deck add --file`
// would read; the browser reaches the same parser through importTextDeck.
func browseDeck(t *testing.T, name, body string) *decksource.Deck {
	t.Helper()
	d, err := decksource.ParseText(name, "", "", "text", strings.NewReader(body))
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	return d
}

// browseDeps builds the Deps cmdBrowse builds, minus the catalog the browser
// only uses for its own pickers. The fixtures are fully priced, so FillGaps
// finds no gap and never reaches the network.
func browseDeps(t *testing.T) action.Deps {
	t.Helper()
	return action.Deps{
		Store: importStore(t), CacheDir: pricing.DefaultCacheDir(), Resolver: cardResolver,
	}
}

// A decklist whose lines could not all be read was not fully imported, and
// the browser's report has to say so. `deck add` raises ErrPartial and exits
// 2 for exactly this; the browser is the same operation through a different
// door, and an exit code is not a thing it has. So the report is where the
// news lands — the reader is looking at it either way.
func TestBrowseDeckAddReportsUnreadableLines(t *testing.T) {
	stubFetch(t, importFixtures()...)
	deck := browseDeck(t, "Mixed", "1 Sol Ring (c21) 125\n~~~ garbage ~~~\nalso not a card line\n")
	if len(deck.Skipped) != 2 {
		t.Fatalf("fixture parsed %d skipped lines, want 2: %v", len(deck.Skipped), deck.Skipped)
	}

	r, err := browseDeckAdd(context.Background(), browseDeps(t), nil, deck)
	if err != nil {
		t.Fatalf("browseDeckAdd: %v", err)
	}
	// Partial is "done, mostly": the readable line still landed, so the
	// headline still reads as an import.
	if !strings.Contains(r.Summary, "1 cards resolved") {
		t.Errorf("summary = %q, want the one readable line reported as imported", r.Summary)
	}
	if !strings.Contains(r.Summary, "2 unreadable") {
		t.Errorf("summary = %q, want the 2 unreadable lines counted", r.Summary)
	}
	body := strings.Join(r.Report, "\n")
	for _, sk := range deck.Skipped {
		if !strings.Contains(body, sk) {
			t.Errorf("report does not name the skipped line %q:\n%s", sk, body)
		}
	}
	// A garbage line was never resolved against anything, so it must not
	// inherit the unresolved-cards sentence.
	if strings.Contains(body, "could not be resolved") {
		t.Errorf("unreadable lines reported as unresolved cards:\n%s", body)
	}
}

// Both losses at once: a line that parsed but named nothing, and lines that
// did not parse. They are different failures and the report must keep them
// apart — the CLI already prints two separate sentences for them.
func TestBrowseDeckAddSeparatesUnresolvedFromUnreadable(t *testing.T) {
	stubFetch(t, importFixtures()...)
	deck := browseDeck(t, "Mixed",
		"1 Sol Ring (c21) 125\n1 Blrgh Nonsense\n~~~ garbage ~~~\n")
	if len(deck.Skipped) != 1 {
		t.Fatalf("fixture parsed %d skipped lines, want 1: %v", len(deck.Skipped), deck.Skipped)
	}

	r, err := browseDeckAdd(context.Background(), browseDeps(t), nil, deck)
	if err != nil {
		t.Fatalf("browseDeckAdd: %v", err)
	}
	if !strings.Contains(r.Summary, "1 unresolved") || !strings.Contains(r.Summary, "1 unreadable") {
		t.Errorf("summary = %q, want both losses counted separately", r.Summary)
	}
	body := strings.Join(r.Report, "\n")
	if !strings.Contains(body, "1 cards could not be resolved and were skipped:") {
		t.Errorf("report lost the unresolved-cards sentence:\n%s", body)
	}
	if !strings.Contains(body, "1 lines could not be read and were skipped:") {
		t.Errorf("report lost the unreadable-lines sentence:\n%s", body)
	}
	if !strings.Contains(body, "Blrgh Nonsense") {
		t.Errorf("report does not name the unresolved card:\n%s", body)
	}
	if !strings.Contains(body, deck.Skipped[0]) {
		t.Errorf("report does not name the skipped line:\n%s", body)
	}
}

// The control that must pass before and after: a decklist that reads
// entirely produces the report it always produced, to the byte. A fix that
// changes the clean case has changed what every successful import looks
// like, which is not what was asked for.
func TestBrowseDeckAddCleanDeckReportUnchanged(t *testing.T) {
	stubFetch(t, importFixtures()...)
	deck := browseDeck(t, "Fish Tank", "2 Sol Ring (c21) 125\n1 Mystic Remora\n")

	r, err := browseDeckAdd(context.Background(), browseDeps(t), nil, deck)
	if err != nil {
		t.Fatalf("browseDeckAdd: %v", err)
	}
	const want = `imported deck "Fish Tank" (text) · 2 cards resolved`
	if r.Summary != want {
		t.Errorf("summary = %q, want %q", r.Summary, want)
	}
	if r.Report != nil {
		t.Errorf("report = %q, want nothing to say", r.Report)
	}
}
