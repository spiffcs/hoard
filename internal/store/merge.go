package store

// The batch write behind `hoard merge`: everything one other database
// contributes, committed as one transaction.
//
// One transaction for the same reason ApplyImport is one — holdings accumulate,
// so a half-applied merge cannot be told apart from cards actually owned and
// cannot be safely re-run — and for one more: a merge writes catalog, binders,
// decks and watches, and a deck that landed without the printings it refers to
// would leave the database inconsistent rather than merely incomplete.

import (
	"fmt"
)

// MergePlan is one merge fully decided: which catalog rows to write, which
// binders to create, which holdings land where, and which decks and watches
// come across.
//
// Every conflict has already been resolved by the time a plan exists. What is
// absent from a plan is as deliberate as what is in it — a deck the receiving
// hoard already has is simply not here.
type MergePlan struct {
	Printings []SourcePrinting
	// NewBinders are binder names to create; Adds referring to them carry the
	// name in Binder with ContainerID 0, exactly as an import does.
	NewBinders []string
	Adds       []CardAdd
	Decks      []DeckMerge
	Watches    []WatchInput
}

// DeckMerge is one deck to write whole: its identity and every card in it.
type DeckMerge struct {
	Meta    DeckMeta
	Entries []Entry
}

// MergeResult is what the merge wrote, for the receipt and the report.
type MergeResult struct {
	// Printings is catalog rows offered, not rows changed: a printing the
	// receiving hoard already had at a newer timestamp is counted here and
	// deliberately left alone in the database.
	//
	// The per-binder breakdown is not here: which binder a name lands in is
	// the plan's decision, so the plan's author is what reports it.
	Printings      int
	Copies         int
	CreatedBinders []string
	Decks          int
	DeckCards      int
	Watches        int
}

// ApplyMerge writes one planned merge.
//
// Order inside the transaction is a foreign-key order, not a preference:
// card_entries and watches both reference cards, so the catalog goes first and
// a deck's own entries go with it.
func (s *Store) ApplyMerge(receipt *ImportReceipt, p MergePlan) (MergeResult, error) {
	res := MergeResult{Printings: len(p.Printings)}

	// Vet everything vetable before the transaction opens, as ApplyImport
	// does: a bad name or finish should refuse the merge rather than abort it
	// half-planned.
	type binderPlan struct{ name, sourceID string }
	plans := make([]binderPlan, 0, len(p.NewBinders))
	for _, name := range p.NewBinders {
		trimmed, sid, err := s.validateNewBinderName(name)
		if err != nil {
			return res, err
		}
		plans = append(plans, binderPlan{trimmed, sid})
	}
	for _, a := range p.Adds {
		if err := validFinish(a.Finish); err != nil {
			return res, err
		}
	}
	for _, d := range p.Decks {
		for _, e := range d.Entries {
			if err := validFinish(e.Finish); err != nil {
				return res, err
			}
		}
	}
	for i := range p.Watches {
		p.Watches[i].normalize()
		if err := validateWatch(p.Watches[i]); err != nil {
			return res, err
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	if err := mergePrintingsTx(tx, p.Printings); err != nil {
		return res, err
	}

	created := make(map[string]int64, len(plans))
	for _, bp := range plans {
		out, err := tx.Exec(insertBinderSQL, KindCollection, bp.name, bp.sourceID, now(), now())
		if err != nil {
			return res, fmt.Errorf("creating binder %q: %w", bp.name, err)
		}
		id, err := out.LastInsertId()
		if err != nil {
			return res, err
		}
		created[bp.name] = id
		res.CreatedBinders = append(res.CreatedBinders, bp.name)
	}

	stmt, err := tx.Prepare(entryAccumulateSQL)
	if err != nil {
		return res, err
	}
	defer stmt.Close()
	for _, a := range p.Adds {
		cid := a.ContainerID
		if cid == 0 {
			var ok bool
			if cid, ok = created[a.Binder]; !ok {
				return res, fmt.Errorf("add for %q names binder %q, which this merge does not create",
					a.Card.Name, a.Binder)
			}
		}
		// Binder holdings are 'main' for the reason ApplyImport gives: boards
		// are a deck's structure. A deck's own boards ride along with it below.
		if _, err := stmt.Exec(cid, a.Card.ID, a.Finish, orUnknown(a.Condition),
			"main", a.Quantity); err != nil {
			return res, fmt.Errorf("merging %s: %w", a.Card.Name, err)
		}
		res.Copies += a.Quantity
	}

	for _, d := range p.Decks {
		if _, err := upsertDeckTx(tx, d.Meta, d.Entries); err != nil {
			return res, err
		}
		res.Decks++
		for _, e := range d.Entries {
			res.DeckCards += e.Quantity
		}
	}

	for _, w := range p.Watches {
		// last_fired_at is deliberately not carried: the document does not
		// hold it, and a merged percent watch anchors from the receiving
		// hoard's own history, which is the only history it can honestly
		// speak about.
		if _, err := tx.Exec(watchUpsertSQL, w.ScryfallID, w.Display, w.Finish, w.Op,
			w.Threshold, w.Pct, w.MinMove, w.WindowDays, now()); err != nil {
			return res, fmt.Errorf("merging watch on %s: %w", w.Display, err)
		}
		res.Watches++
	}

	if receipt != nil {
		if _, err := tx.Exec(`
INSERT INTO import_ledger (hash, file, cards, imported_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(hash) DO UPDATE SET file=excluded.file, cards=excluded.cards,
    imported_at=excluded.imported_at`,
			receipt.Hash, receipt.File, receipt.Cards, now()); err != nil {
			return res, fmt.Errorf("recording merge receipt: %w", err)
		}
	}
	return res, tx.Commit()
}
