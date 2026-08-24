package action

import (
	"bytes"
	"context"
	"fmt"

	"github.com/spiffcs/hoard/internal/decksource"
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

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

	if len(binders) == 0 {
		return 0, "", fmt.Errorf("no default binder exists to add into; the database is missing its collection container")
	}
	return binders[0].ID, binders[0].Name, nil
}

type AddListOptions struct {
	Data      []byte
	Display   string
	BinderRef string
	Again     bool
}

type AddListResult struct {
	Copies   int
	Resolved int
	Binder   string

	Skipped    []string
	Refinished int
	Unresolved []string
	Gaps       GapReport
}

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
			return res, fmt.Errorf("no card lines found in %s; %s could not be read (e.g. %s)",
				o.Display, ui.Plural(len(skipped), "line", "lines"), skipped[0])
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

		if res.Gaps, err = FillGaps(ctx, d, p); err != nil {
			return res, err
		}
	}
	if n := len(res.Skipped) + len(res.Unresolved); n > 0 {
		return res, fmt.Errorf("%s skipped: %w", ui.Plural(n, "line was", "lines were"), ErrPartial)
	}
	return res, nil
}

type AddCardByURLOptions struct {
	URL       string
	Foil      bool
	Qty       int
	BinderRef string
}

type AddCardByURLResult struct {
	Card     scryfall.Card
	Finish   finish.Finish
	Binder   string
	PriceUSD *float64
}

func AddCardByURL(ctx context.Context, d Deps, p progress.Fn, o AddCardByURLOptions) (AddCardByURLResult, error) {
	var res AddCardByURLResult
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

	res.Card, res.Finish, res.PriceUSD = *card, finish.Nonfoil, card.PriceUSD
	if o.Foil {
		res.Finish, res.PriceUSD = finish.Foil, card.PriceUSDFoil
	}
	if err := d.Store.AddCardFinishTo(target, *card, res.Finish, o.Qty); err != nil {
		return res, err
	}
	return res, nil
}

type DeckAddOptions struct {
	DryRun bool
}

type DeckAddResult struct {
	ID         int64
	Name       string
	Source     string
	Resolved   int
	Refinished int
	Unresolved []string
	Gaps       GapReport

	Replaces string
}

func DeckAdd(ctx context.Context, d Deps, p progress.Fn, deck *decksource.Deck, o DeckAddOptions) (DeckAddResult, error) {
	var res DeckAddResult
	res.Name, res.Source = deck.Name, deck.Source

	_, name, exists, err := d.Store.DeckBySource(deck.Source, deck.SourceID)
	if err != nil {
		return res, err
	}
	switch {

	case exists && o.DryRun:
		res.Replaces = name
	case exists:
		q := fmt.Sprintf("Deck %q is already imported. Replace its cards (manual edits to them are lost)?", name)
		if !d.confirm(q) {
			return res, fmt.Errorf("deck %q is already imported; re-importing replaces its cards and discards manual edits (re-run with --refresh to do it)", name)
		}
	}

	rr, err := d.resolver(p).Resolve(ctx, resolve.Requests(deck.Entries))
	if err != nil {
		return res, err
	}
	res.Refinished, res.Unresolved = rr.Refinished, rr.Unresolved

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

	if o.DryRun {
		if n := len(deck.Skipped) + len(res.Unresolved); n > 0 {
			return res, fmt.Errorf("%s would not resolve: %w",
				ui.Plural(n, "line", "lines"), ErrPartial)
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

	if res.Gaps, err = FillGaps(ctx, d, p); err != nil {
		return res, err
	}

	if n := len(deck.Skipped) + len(res.Unresolved); n > 0 {
		return res, fmt.Errorf("%s skipped: %w", ui.Plural(n, "line was", "lines were"), ErrPartial)
	}
	return res, nil
}
