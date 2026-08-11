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
	if !strings.Contains(r.Summary, "1 card resolved") {
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
	// Singular, because there is exactly one of each: these sentences land on
	// the error path, and "1 cards ... were skipped" reads as a bug in the
	// thing that just told you it lost something.
	if !strings.Contains(body, "1 card could not be resolved and was skipped:") {
		t.Errorf("report lost the unresolved-cards sentence:\n%s", body)
	}
	if !strings.Contains(body, "1 line could not be read and was skipped:") {
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

// A price refresh that could not re-fetch some cards has to say so. Scryfall
// dropping an identifier is permanent — those prices never move again — and
// the browser's line reported only what it found, so the number quietly
// shrank with nothing to explain it. The CLI has said this since it had a
// report to say it in.
func TestBrowseUpdatePricesReportsNotFound(t *testing.T) {
	got := browseUpdatePricesSummary(action.UpdatePricesResult{
		Total: 120, Found: 117, NotFound: 3,
	})
	if !strings.Contains(got, "3 could not be re-fetched") {
		t.Errorf("summary = %q, want the 3 cards Scryfall no longer answers for", got)
	}
}

// The two counters sit on one line and count different failures: a printing
// Scryfall stopped answering for, and a printing nothing could price. If the
// second wording were reused for the first, the line would read as one
// number split in two.
func TestBrowseUpdatePricesKeepsNotFoundDistinctFromUnpriced(t *testing.T) {
	res := action.UpdatePricesResult{Total: 120, Found: 117, NotFound: 3}
	res.Gaps.Remaining = 8
	got := browseUpdatePricesSummary(res)
	const want = "prices updated · 117 printings · 3 could not be re-fetched · 8 still unpriced"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// The control that must pass before and after: a refresh with nothing to
// confess produces the line it always produced, to the byte.
func TestBrowseUpdatePricesCleanSummaryUnchanged(t *testing.T) {
	got := browseUpdatePricesSummary(action.UpdatePricesResult{Total: 120, Found: 120})
	const want = "prices updated · 120 printings"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	// The empty hoard's line is the other branch and is equally untouched.
	if got := browseUpdatePricesSummary(action.UpdatePricesResult{}); got != "no cards yet; nothing to update" {
		t.Errorf("empty-hoard summary = %q", got)
	}
}

// A backfill that could not cover part of the hoard has to say so. The CLI
// raises both shortfalls as warnings on stderr precisely because they are
// the partial outcome; the browser has neither stderr nor a report slot on
// this seam, so silence meant the reader saw only the half that worked.
func TestBrowseBackfillReportsUnmappedAndUnquoted(t *testing.T) {
	got := browseBackfillSummary(action.BackfillResult{
		Printings: 400, Inserted: 9000, Cards: 340, Unmapped: 12, Unquoted: 48,
	})
	if !strings.Contains(got, "12 skipped (no MTGJSON id)") {
		t.Errorf("summary = %q, want the 12 printings with no MTGJSON id", got)
	}
	if !strings.Contains(got, "48 with no TCGplayer history") {
		t.Errorf("summary = %q, want the 48 printings with no price history", got)
	}
	// The headline already counts printings. A second count of them would
	// read as a share of the first rather than a separate shortfall, so
	// neither counter repeats the noun.
	if strings.Count(got, "printings") != 1 {
		t.Errorf("summary = %q, want %q used once, for the headline", got, "printings")
	}
	// Only the unmapped half was skipped: the unquoted printings were asked
	// about and the archive answered with nothing.
	if strings.Count(got, "skipped") != 1 {
		t.Errorf("summary = %q, want %q claimed only of the unmapped half", got, "skipped")
	}
}

// The control that must pass before and after: a backfill that covered
// everything produces the line it always produced, to the byte — including
// the buylist clause, which shares the counter run with the two new ones.
func TestBrowseBackfillCleanSummaryUnchanged(t *testing.T) {
	got := browseBackfillSummary(action.BackfillResult{
		Printings: 400, Inserted: 9000, Cards: 340, BidInserted: 1200,
	})
	const want = "backfilled 9,000 observations across 340 printings · 1,200 buylist bids"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	// The three early returns are untouched by the counters below them.
	for _, tc := range []struct {
		res  action.BackfillResult
		want string
	}{
		{action.BackfillResult{}, "nothing owned yet"},
		{action.BackfillResult{Printings: 10, AlreadyToday: "2026-08-10T09:00:00Z"}, "already backfilled today"},
		{action.BackfillResult{Printings: 10}, "nothing to backfill · history already recorded"},
	} {
		if got := browseBackfillSummary(tc.res); got != tc.want {
			t.Errorf("summary = %q, want %q", got, tc.want)
		}
	}
}
