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
	// ListBinders creates the default binder on demand today, but that
	// invariant lives three call-frames away; a future filter (hidden
	// binders, a corrupt row) returning an empty list must be an error here,
	// not an index panic on the add path.
	if len(binders) == 0 {
		return 0, "", fmt.Errorf("no default binder exists to add into; the database is missing its collection container")
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

// DeckAddOptions is one deck import as requested. The parsed Deck stays a
// separate parameter rather than a field here — acquiring it is the
// frontend's job, as DeckAdd's own comment explains, while this carries only
// what the caller decided about the import.
type DeckAddOptions struct {
	// DryRun resolves the list and reports what it would do, writing
	// nothing: not the deck, not the printings behind it, not the prices
	// that would be filled in after. The rehearsal `import --dry-run`
	// already offers, and the one an LLM-authored decklist most wants before
	// it lands in somebody's collection.
	DryRun bool
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

	// Replaces names the already-imported deck this import would overwrite,
	// empty when there is none. Only a dry run fills it: the live path asks
	// about the overwrite instead, and by the time it returns the overwrite
	// has happened, so there is nothing left to warn about.
	Replaces string
}

// DeckAdd resolves a parsed deck's entries and upserts the whole deck. The
// caller supplies the Deck — from a provider URL fetch or a text file —
// because acquiring it is frontend-shaped (file dialogs, pasted URLs); what
// happens after is not.
func DeckAdd(ctx context.Context, d Deps, p progress.Fn, deck *decksource.Deck, o DeckAddOptions) (DeckAddResult, error) {
	var res DeckAddResult
	res.Name, res.Source = deck.Name, deck.Source

	// Re-importing an existing deck replaces every entry, and with them any
	// conditions assessed or printings corrected by hand — an evening of
	// browse edits gone to one re-pasted URL. So an overwrite is asked, not
	// assumed. Asked before the resolve work, too: the answer changes
	// whether any of it should happen. The CLI wires Confirm to a terminal
	// prompt (or --refresh); the browser to its confirm surface; a script
	// with neither declines and gets told how to proceed.
	_, name, exists, err := d.Store.DeckBySource(deck.Source, deck.SourceID)
	if err != nil {
		return res, err
	}
	switch {
	// A dry run has nothing to ask permission for, and asking anyway would
	// hang the unattended rehearsal the flag exists to serve — or, at a
	// terminal, make the user answer for a replacement that is not going to
	// happen. So the fact is reported rather than prompted. This is the same
	// trade import's dry run makes when it skips the re-import ledger: a
	// rehearsal answers "what would this do", and refusing to rehearse
	// because the real thing would need a decision answers nothing.
	case exists && o.DryRun:
		res.Replaces = name
	case exists:
		q := fmt.Sprintf("Deck %q is already imported. Replace its cards (manual edits to them are lost)?", name)
		if !d.confirm(q) {
			return res, fmt.Errorf("deck %q is already imported; re-importing replaces its cards and discards manual edits (re-run with --refresh to do it)", name)
		}
	}

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
	// Even the printings are withheld on a rehearsal. They are only the
	// catalog rows the deck's entries point at, so keeping them would look
	// harmless — but they are rows, in tables `hoard` reports on, and a dry
	// run that leaves any behind cannot honestly print "nothing was written".
	if !o.DryRun {
		if err := d.Store.UpsertPrintings(rr.Found); err != nil {
			return res, err
		}
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

	// A dry run stops here, with everything it learned and nothing to show
	// for it in the database: res.ID stays zero because no deck was created,
	// and the caller must not print it as one. The skipped lines and the
	// unresolved cards are still the partial outcome they would be on the
	// real run — a rehearsal whose exit code said "clean" while the import
	// it rehearsed would exit 2 would be the one thing a rehearsal must
	// never do. So this guard counts what the real one below counts; see
	// there for why unreadable lines are in that count.
	if o.DryRun {
		if n := len(deck.Skipped) + len(res.Unresolved); n > 0 {
			return res, fmt.Errorf("%d lines would not resolve: %w", n, ErrPartial)
		}
		return res, nil
	}

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
	// Two ways a decklist line fails to become a card, and both of them are
	// this import not having imported the list: the line did not parse
	// (deck.Skipped, counted by the frontend that did the parsing) or it
	// parsed and nothing answered it (res.Unresolved). Only the second used
	// to reach the exit status, so a deck restore that read one line of a
	// 99-card file exited 0 with the other 98 named on stderr — a scripted
	// restore could not tell that from a clean one. AddList has always
	// summed both; this is the same condition, and it did not have a second
	// answer.
	//
	// Changing an exit status is a contract change, made deliberately here
	// on the precedent of c9d5b87, which made exactly this one for `import`
	// after a canonical export round trip dropped 1,879 of 2,235 copies and
	// exited green. The argument carries unchanged: exit 2 is hoard's word
	// for "done, mostly", the receipt still prints, and the lines that did
	// import are still written.
	if n := len(deck.Skipped) + len(res.Unresolved); n > 0 {
		return res, fmt.Errorf("%d lines were skipped: %w", n, ErrPartial)
	}
	return res, nil
}
