package action

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/spiffcs/hoard/internal/collsource"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

// ErrPartial marks an operation that finished its work but had to skip some
// of it — unresolved cards in an import, say. Frontends map it to exit code
// 2, so a pipeline can distinguish "done" from "done, mostly" without
// parsing output.
var ErrPartial = fmt.Errorf("some items were skipped")

// resolver returns the resolver an operation should fetch through: the
// injected one when its test seam is armed, else a per-call resolver whose
// fetches narrate as a "resolving cards" bar. Done accumulates across the
// pipeline's two passes and Total grows when the name-retry pass adds
// identifiers — consumers re-read Total on every event by contract.
func (d Deps) resolver(p progress.Fn) *resolve.Resolver {
	if d.Resolver != nil && d.Resolver.Fetch != nil {
		return d.Resolver
	}
	var done, total int
	return &resolve.Resolver{
		Fetch: func(ctx context.Context, ids []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error) {
			total += len(ids)
			start := done
			found, missing, err := scryfall.FetchCollectionProgress(ctx, ids,
				func(chunkDone, _ int, note string) {
					p.Emit(progress.Event{Step: "resolving cards",
						Done: int64(start + chunkDone), Total: int64(total),
						Unit: progress.UnitCards, Note: note})
				})
			done += len(ids)
			p.Emit(progress.Event{Step: "resolving cards",
				Done: int64(done), Total: int64(total), Unit: progress.UnitCards})
			return found, missing, err
		},
	}
}

// ImportOptions is one collection import as requested.
type ImportOptions struct {
	Data []byte
	// Display names the source in the receipt and in errors — a path, or
	// "stdin".
	Display   string
	Format    string // auto (sniff the header), manabox, moxfield, delver, hoard
	BinderRef string // destination binder; "" = the default binder
	Preserve  bool   // recreate the file's own binders instead
	DryRun    bool
	Again     bool
}

// ImportResult is everything the import did, skipped, and dropped — the
// frontend renders it verbatim.
type ImportResult struct {
	Format          string
	Copies          int
	Resolved        int
	PerBinder       map[string]int
	Created         []string
	SkippedDeckRows int
	Refinished      int
	Dropped         map[string]int
	Unresolved      []string
	Gaps            GapReport
}

// ImportCollection adds a collection CSV export — ManaBox, Moxfield, Delver
// Lens, or hoard's own — to the binder of your choice, as one transaction.
func ImportCollection(ctx context.Context, d Deps, p progress.Fn, o ImportOptions) (ImportResult, error) {
	var res ImportResult
	hash := ContentHash(o.Data)
	if !o.DryRun {
		if err := RefuseReimport(d.Store, hash, o.Again); err != nil {
			return res, err
		}
	}

	coll, err := collsource.Parse(bytes.NewReader(o.Data), o.Format)
	if err != nil {
		return res, fmt.Errorf("%s: %w", o.Display, err)
	}
	res.Format = coll.Format
	res.Dropped = coll.Dropped

	// Deck rows in a canonical export are the decks' own contents. Imported
	// as loose cards they would inflate the binder totals, and re-importing
	// the decks themselves would then count every card twice — so they are
	// skipped, and `deck add` remains the way decks come back.
	keptRows := coll.Rows[:0]
	for _, r := range coll.Rows {
		if r.Kind == "deck" {
			res.SkippedDeckRows++
			continue
		}
		keptRows = append(keptRows, r)
	}
	coll.Rows = keptRows

	// Resolve every row through the shared pipeline — bulk lookup, name
	// retry for set+number pairs Scryfall does not know, finish correction.
	rr, err := d.resolver(p).Resolve(ctx, resolve.Requests(coll.Rows))
	if err != nil {
		return res, err
	}
	res.Refinished, res.Unresolved = rr.Refinished, rr.Unresolved

	type addition struct {
		binder string // destination binder; "" = the --binder/default target
		card   scryfall.Card
		finish string
		qty    int
	}
	var adds []addition
	for i, r := range coll.Rows {
		m := rr.Matches[i]
		if !m.OK {
			continue
		}
		binder := ""
		if o.Preserve {
			binder = r.Binder
		}
		adds = append(adds, addition{binder: binder, card: m.Card, finish: m.Finish, qty: r.Quantity})
	}
	res.Resolved = len(adds)

	// Destinations. ListBinders puts the default binder first, and its
	// display names are what ManaBox-style binder columns are matched on.
	binders, err := d.Store.ListBinders()
	if err != nil {
		return res, err
	}
	targetID, targetName := binders[0].ID, binders[0].Name
	if o.BinderRef != "" {
		b, err := d.Store.BinderByRef(o.BinderRef)
		if err != nil {
			return res, err
		}
		targetID, targetName = b.ID, b.Name
	}
	binderIDs := make(map[string]int64, len(binders))
	binderNames := make(map[int64]string, len(binders))
	for _, b := range binders {
		binderIDs[strings.ToLower(b.Name)] = b.ID
		binderNames[b.ID] = b.Name
	}
	// The reserved aliases land in the default binder even after a rename, so
	// a pre-rename export whose Container column says "Binder" round-trips
	// into it instead of creating a second binder by that name. A legacy
	// binder that actually owns one of these names (possible before they were
	// reserved) keeps its claim via the map entry made above.
	for _, alias := range store.ReservedBinderNames {
		if _, taken := binderIDs[strings.ToLower(alias)]; !taken {
			binderIDs[strings.ToLower(alias)] = binders[0].ID
		}
	}

	// Plan every write first, then hand the whole batch to the store as one
	// transaction: an interrupted import must be nothing rather than half,
	// because added quantities cannot be told apart from cards actually
	// owned. New binder names are deduped case-insensitively on their first
	// spelling.
	spelling := make(map[string]string)
	res.PerBinder = make(map[string]int)
	cardAdds := make([]store.CardAdd, 0, len(adds))
	for _, a := range adds {
		dest, name := targetID, targetName
		newBinder := ""
		if a.binder != "" {
			key := strings.ToLower(a.binder)
			if id, ok := binderIDs[key]; ok {
				// The binder's own name, not the CSV's spelling: an aliased
				// "Binder" cell should be receipted under the default
				// binder's current name.
				dest, name = id, binderNames[id]
			} else {
				canonical, seen := spelling[key]
				if !seen {
					canonical = strings.TrimSpace(a.binder)
					spelling[key] = canonical
					res.Created = append(res.Created, canonical)
				}
				dest, name, newBinder = 0, canonical, canonical
			}
		}
		cardAdds = append(cardAdds, store.CardAdd{
			ContainerID: dest, Binder: newBinder,
			Card: a.card, Finish: a.finish, Quantity: a.qty,
		})
		res.Copies += a.qty
		res.PerBinder[name] += a.qty
	}
	if !o.DryRun && len(cardAdds) > 0 {
		receipt := &store.ImportReceipt{Hash: hash, File: o.Display, Cards: res.Copies}
		if _, err := d.Store.ApplyImport(receipt, res.Created, cardAdds); err != nil {
			return res, err
		}
	}
	if o.DryRun {
		if n := len(res.Unresolved); n > 0 {
			return res, fmt.Errorf("%d rows would not resolve: %w", n, ErrPartial)
		}
		return res, nil
	}

	// Price what Scryfall could not, as deck add does, so the import is
	// worth what it is worth immediately.
	if res.Gaps, err = FillGaps(ctx, d, p); err != nil {
		return res, err
	}
	if n := len(res.Unresolved); n > 0 {
		return res, fmt.Errorf("%d rows were skipped: %w", n, ErrPartial)
	}
	return res, nil
}
