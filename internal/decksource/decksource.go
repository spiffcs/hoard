// Package decksource imports decks from external deck-list sources into a
// provider-agnostic form. Each provider turns a URL (or pasted text) into a
// normalized Deck of Scryfall identifiers; the caller resolves those to catalog
// cards via the scryfall package.
package decksource

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
)

// Board names used across providers.
const (
	BoardMain      = "main"
	BoardCommander = "commander"
	BoardSide      = "side"
	BoardMaybe     = "maybe"
)

// Entry is one card line in an imported deck, addressed by a Scryfall
// identifier (by id, set+number, or name) with quantity, finish, and board.
// Name rides along even when the identifier is an id or set+number, so a
// lookup miss can fall back to a name search instead of dropping the card.
type Entry struct {
	Ident    scryfall.Identifier
	Name     string
	Quantity int
	Finish   string // nonfoil|foil|etched
	Board    string // main|commander|side|maybe
}

// Request states this entry as the resolve pipeline's input.
func (e Entry) Request() resolve.Request {
	return resolve.Request{Ident: e.Ident, Name: e.Name, Finish: e.Finish}
}

// Deck is a normalized, provider-agnostic deck import.
type Deck struct {
	Name      string
	Source    string // provider slug: "archidekt", "text", ...
	SourceID  string // external id (deck id, or a stable id for text imports)
	SourceURL string
	Format    string
	Entries   []Entry
	// Skipped are the lines a text import could not read, with their line
	// numbers, so the caller can say what was dropped instead of failing the
	// whole file over one odd line. Provider imports leave it empty.
	Skipped []string
}

// Provider imports decks from one kind of URL.
type Provider interface {
	// Matches reports whether this provider handles the given URL.
	Matches(u *url.URL) bool
	// Fetch retrieves and normalizes the deck at u.
	Fetch(ctx context.Context, u *url.URL) (*Deck, error)
}

// providers is the ordered registry of URL-based providers. A Moxfield provider
// is intentionally absent — its API is Cloudflare-gated (HTTP 403) — but the
// registry stays open so one can be slotted in if access becomes available.
var providers = []Provider{archidektProvider{}}

// Fetch selects the first provider that matches rawURL and imports the deck.
func Fetch(ctx context.Context, rawURL string) (*Deck, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	for _, p := range providers {
		if p.Matches(u) {
			return p.Fetch(ctx, u)
		}
	}
	return nil, fmt.Errorf("no importer for host %q; for sites without an open API "+
		"(e.g. moxfield.com), export the deck to text and use --file", u.Host)
}
