package decksource

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/safetext"
	"github.com/spiffcs/hoard/internal/scryfall"
)

const (
	BoardMain      = "main"
	BoardCommander = "commander"
	BoardSide      = "side"
	BoardMaybe     = "maybe"
)

type Entry struct {
	Ident    scryfall.Identifier
	Name     string
	Quantity int
	Finish   finish.Finish
	Board    string
}

func (e Entry) Request() resolve.Request {
	return resolve.Request{Ident: e.Ident, Name: e.Name, Finish: e.Finish}
}

type Deck struct {
	Name      string
	Source    string
	SourceID  string
	SourceURL string
	Format    string
	Entries   []Entry

	Skipped []string
}

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

func cleanEntries(es []Entry) {
	for i := range es {
		es[i].Name = safetext.Clean(es[i].Name)
		es[i].Ident.Name = safetext.Clean(es[i].Ident.Name)
		es[i].Ident.Set = safetext.Clean(es[i].Ident.Set)
		es[i].Ident.CollectorNumber = safetext.Clean(es[i].Ident.CollectorNumber)
	}
}

type Provider interface {
	Matches(u *url.URL) bool

	Fetch(ctx context.Context, u *url.URL) (*Deck, error)
}

var providers = []Provider{archidektProvider{}}

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

			return d.clean(), nil
		}
	}
	return nil, fmt.Errorf("no importer for host %q; for sites without an open API "+
		"(e.g. moxfield.com), export the deck to text and use --file", u.Host)
}
