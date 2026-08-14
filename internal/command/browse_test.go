package command

import (
	"context"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/decksource"
	"github.com/spiffcs/hoard/internal/pricing"
)

func browseDeck(t *testing.T, name, body string) *decksource.Deck {
	t.Helper()
	d, err := decksource.ParseText(name, "", "", "text", strings.NewReader(body))
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	return d
}

func browseDeps(t *testing.T) action.Deps {
	t.Helper()
	return action.Deps{
		Store: importStore(t), CacheDir: pricing.DefaultCacheDir(), Resolver: cardResolver,
	}
}

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

	if strings.Contains(body, "could not be resolved") {
		t.Errorf("unreadable lines reported as unresolved cards:\n%s", body)
	}
}

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

func TestBrowseUpdatePricesReportsNotFound(t *testing.T) {
	got := browseUpdatePricesSummary(action.UpdatePricesResult{
		Total: 120, Found: 117, NotFound: 3,
	})
	if !strings.Contains(got, "3 could not be re-fetched") {
		t.Errorf("summary = %q, want the 3 cards Scryfall no longer answers for", got)
	}
}

func TestBrowseUpdatePricesKeepsNotFoundDistinctFromUnpriced(t *testing.T) {
	res := action.UpdatePricesResult{Total: 120, Found: 117, NotFound: 3}
	res.Gaps.Remaining = 8
	got := browseUpdatePricesSummary(res)
	const want = "prices updated · 117 printings · 3 could not be re-fetched · 8 still unpriced"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestBrowseUpdatePricesCleanSummaryUnchanged(t *testing.T) {
	got := browseUpdatePricesSummary(action.UpdatePricesResult{Total: 120, Found: 120})
	const want = "prices updated · 120 printings"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}

	if got := browseUpdatePricesSummary(action.UpdatePricesResult{}); got != "no cards yet; nothing to update" {
		t.Errorf("empty-hoard summary = %q", got)
	}
}

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

	if strings.Count(got, "printings") != 1 {
		t.Errorf("summary = %q, want %q used once, for the headline", got, "printings")
	}

	if strings.Count(got, "skipped") != 1 {
		t.Errorf("summary = %q, want %q claimed only of the unmapped half", got, "skipped")
	}
}

func TestBrowseBackfillCleanSummaryUnchanged(t *testing.T) {
	got := browseBackfillSummary(action.BackfillResult{
		Printings: 400, Inserted: 9000, Cards: 340, BidInserted: 1200,
	})
	const want = "backfilled 9,000 observations across 340 printings · 1,200 buylist bids"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}

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
