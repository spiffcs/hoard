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
	"github.com/spiffcs/hoard/internal/safetext"
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

// clean strips the characters a terminal acts on from every string in d that
// came from outside hoard — see internal/safetext for what and why.
//
// This is a method on Deck rather than a call at each field the providers
// assign, because the providers are the thing that grows: archidekt.go builds
// a Deck in one place, textlist.go in another, and a Moxfield provider would
// be a third. Cleaning at the exits from this package means a new provider is
// covered by construction, and cannot forget.
//
// EVERY EXPORTED FUNCTION THAT RETURNS A Deck OR AN Entry MUST CALL THIS.
// There are three today: Fetch, ParseText and ParseLoose.
//
// Skipped is included and is not an afterthought — it is the most direct sink
// of the three. Those lines are the ones the parser could NOT read, and they
// are quoted straight back to the terminal ("1 lines could not be read (e.g.
// line 1: ...)"), so a line crafted to be unparseable is a line guaranteed to
// be echoed.
//
// The identifier's fields go too. A set code or collector number that fails to
// resolve is repeated in the miss message, which makes them the same kind of
// sink as the name.
func (d *Deck) clean() *Deck {
	if d == nil {
		return nil
	}
	d.Name = safetext.Clean(d.Name)
	d.Format = safetext.Clean(d.Format)
	d.SourceID = safetext.Clean(d.SourceID)
	d.SourceURL = safetext.Clean(d.SourceURL)
	cleanEntries(d.Entries)
	for i, s := range d.Skipped {
		d.Skipped[i] = safetext.Clean(s)
	}
	return d
}

// cleanEntries is split out so ParseLoose, which returns entries with no Deck
// around them, cleans them the same way.
func cleanEntries(es []Entry) {
	for i := range es {
		es[i].Name = safetext.Clean(es[i].Name)
		es[i].Ident.Name = safetext.Clean(es[i].Ident.Name)
		es[i].Ident.Set = safetext.Clean(es[i].Ident.Set)
		es[i].Ident.CollectorNumber = safetext.Clean(es[i].Ident.CollectorNumber)
	}
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
			d, err := p.Fetch(ctx, u)
			if err != nil {
				return nil, err
			}
			// The remote boundary: every string below this line was chosen by
			// whoever built the deck being fetched.
			return d.clean(), nil
		}
	}
	return nil, fmt.Errorf("no importer for host %q; for sites without an open API "+
		"(e.g. moxfield.com), export the deck to text and use --file", u.Host)
}
