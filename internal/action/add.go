package action

// The add capabilities: a pasted list into a binder, one card by its
// Scryfall URL, and a whole deck. They share the resolver pipeline, the
// binder targeting, and the price-what-Scryfall-couldn't follow-up.

import (
	"bytes"
	"context"
	"fmt"

	"github.com/spiffcs/hoard/internal/decksource"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

// binderTarget resolves a --binder reference to a container, defaulting to
// the default binder.
func binderTarget(st *store.Store, ref string) (int64, string, error) {
	if ref != "" {
		b, err := st.BinderByRef(ref)
		if err != nil {
			return 0, "", err
		}
		return b.ID, b.Name, nil
	}
	binders, err := st.ListBinders()
	if err != nil {
		return 0, "", err
	}
	return binders[0].ID, binders[0].Name, nil
}

// AddListOptions is one pasted or piped card list.
type AddListOptions struct {
	Data      []byte
	Display   string // a path, or "stdin"
	BinderRef string
	Again     bool
}

// AddListResult is what the paste added and what it could not read.
type AddListResult struct {
	Copies   int
	Resolved int
	Binder   string
	// Skipped are the unreadable lines, reported with their numbers; the
	// paste succeeds around them.
	Skipped    []string
	Refinished int
	Unresolved []string
	Gaps       GapReport
}

// AddList reads a pasted or exported card list — decklist-style lines — and
// adds everything to one binder. This is the non-interactive add: the same
// lines the deck importer reads, minus boards, plus the import ledger's
// protection against pasting the same list twice.
func AddList(ctx context.Context, d Deps, p progress.Fn, o AddListOptions) (AddListResult, error) {
	var res AddListResult
	hash := ContentHash(o.Data)
	if err := RefuseReimport(d.Store, hash, o.Again); err != nil {
		return res, err
	}
	target, targetName, err := binderTarget(d.Store, o.BinderRef)
	if err != nil {
		return res, err
	}
	res.Binder = targetName

	entries, skipped, err := decksource.ParseLoose(bytes.NewReader(o.Data))
	if err != nil {
		return res, err
	}
	res.Skipped = skipped
	if len(entries) == 0 {
		if len(skipped) > 0 {
			return res, fmt.Errorf("no card lines found in %s; %d lines could not be read (e.g. %s)",
				o.Display, len(skipped), skipped[0])
		}
		return res, fmt.Errorf("no card lines found in %s", o.Display)
	}

	rr, err := d.resolver(p).Resolve(ctx, resolve.Requests(entries))
	if err != nil {
		return res, err
	}
	res.Refinished, res.Unresolved = rr.Refinished, rr.Unresolved

	adds := make([]store.CardAdd, 0, len(entries))
	for i, e := range entries {
		m := rr.Matches[i]
		if !m.OK {
			continue
		}
		adds = append(adds, store.CardAdd{
			ContainerID: target, Card: m.Card, Finish: m.Finish, Quantity: e.Quantity,
		})
		res.Copies += e.Quantity
	}
	res.Resolved = len(adds)
	if len(adds) > 0 {
		receipt := &store.ImportReceipt{Hash: hash, File: o.Display, Cards: res.Copies}
		if _, err := d.Store.ApplyImport(receipt, nil, adds); err != nil {
			return res, err
		}
		// Price what Scryfall could not, so the paste is worth what it is
		// worth immediately.
		if res.Gaps, err = FillGaps(ctx, d, p); err != nil {
			return res, err
		}
	}
	if n := len(res.Skipped) + len(res.Unresolved); n > 0 {
		return res, fmt.Errorf("%d lines were skipped: %w", n, ErrPartial)
	}
	return res, nil
}

// AddByURLOptions is one card by its Scryfall page link.
type AddByURLOptions struct {
	URL       string
	Foil      bool
	Qty       int
	BinderRef string
}

// AddByURLResult is the card as stored, for the confirmation line.
type AddByURLResult struct {
	Card     scryfall.Card
	Finish   string
	Binder   string
	PriceUSD *float64
}

// AddByURL fetches one card by its Scryfall link and files it. The
// destination resolves before the network round-trip: a mistyped binder
// should fail in a millisecond, not after a fetch.
func AddByURL(ctx context.Context, d Deps, p progress.Fn, o AddByURLOptions) (AddByURLResult, error) {
	var res AddByURLResult
	set, number, err := scryfall.ParseCardURL(o.URL)
	if err != nil {
		return res, err
	}
	target, targetName, err := binderTarget(d.Store, o.BinderRef)
	if err != nil {
		return res, err
	}
	res.Binder = targetName

	p.Emit(progress.Event{Step: "fetching card"})
	card, err := scryfall.FetchCard(ctx, set, number)
	if err != nil {
		return res, err
	}
	// One mapping from the flag to a finish, made here, so the stored row
	// and the confirmation line cannot disagree.
	res.Card, res.Finish, res.PriceUSD = *card, "nonfoil", card.PriceUSD
	if o.Foil {
		res.Finish, res.PriceUSD = "foil", card.PriceUSDFoil
	}
	if err := d.Store.AddCardFinishTo(target, *card, res.Finish, o.Qty); err != nil {
		return res, err
	}
	return res, nil
}

// DeckAddResult is one deck import.
type DeckAddResult struct {
	ID         int64
	Name       string
	Source     string
	Resolved   int
	Refinished int
	Unresolved []string
	Gaps       GapReport
}

// DeckAdd resolves a parsed deck's entries and upserts the whole deck. The
// caller supplies the Deck — from a provider URL fetch or a text file —
// because acquiring it is frontend-shaped (file dialogs, pasted URLs); what
// happens after is not.
func DeckAdd(ctx context.Context, d Deps, p progress.Fn, deck *decksource.Deck) (DeckAddResult, error) {
	var res DeckAddResult
	res.Name, res.Source = deck.Name, deck.Source

	// Resolve every entry in bulk — the shared pipeline also retries misses
	// by name and corrects finishes the printing does not come in (a
	// decklist with no *F* marker parses as non-foil, but precon commanders
	// are frequently foil-only, and the claimed finish would price at $0
	// forever).
	rr, err := d.resolver(p).Resolve(ctx, resolve.Requests(deck.Entries))
	if err != nil {
		return res, err
	}
	res.Refinished, res.Unresolved = rr.Refinished, rr.Unresolved
	if err := d.Store.UpsertPrintings(rr.Found); err != nil {
		return res, err
	}

	var entries []store.Entry
	for i, e := range deck.Entries {
		m := rr.Matches[i]
		if !m.OK {
			continue
		}
		entries = append(entries, store.Entry{
			ScryfallID: m.Card.ID,
			Finish:     m.Finish,
			Board:      e.Board,
			Quantity:   e.Quantity,
		})
	}
	res.Resolved = len(entries)

	res.ID, err = d.Store.UpsertDeck(store.DeckMeta{
		Name:      deck.Name,
		Source:    deck.Source,
		SourceID:  deck.SourceID,
		SourceURL: deck.SourceURL,
		Format:    deck.Format,
	}, entries)
	if err != nil {
		return res, err
	}

	// Price what Scryfall could not, now rather than on some later
	// update-prices, so a freshly imported deck is worth what it is worth.
	// This only downloads when the import actually left a gap.
	if res.Gaps, err = FillGaps(ctx, d, p); err != nil {
		return res, err
	}
	if n := len(res.Unresolved); n > 0 {
		return res, fmt.Errorf("%d cards were skipped: %w", n, ErrPartial)
	}
	return res, nil
}
