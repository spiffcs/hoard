package action

import (
	"bytes"
	"errors"
	"fmt"
	"slices"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

var ErrNotConfirmed = errors.New("the move was not confirmed")

type MoveOptions struct {
	Document []byte
	To       string
	DryRun   bool
}

type MoveResult struct {
	Target       string
	Copies       int
	Printings    int
	Value        float64
	AlreadyThere int
	SkippedRows  int
	SkippedDecks []string
}

func MoveHoldings(d Deps, o MoveOptions) (MoveResult, error) {
	var res MoveResult

	held, err := hoardjson.ReadHoldings(bytes.NewReader(o.Document))
	if err != nil {
		return res, err
	}

	target, err := d.Store.BinderByRef(o.To)
	if err != nil {
		return res, err
	}
	res.Target = target.Name

	refs, err := movableRefs(held.Rows, target.ID, &res)
	if err != nil {
		return res, err
	}
	if len(refs) == 0 {
		return res, nothingToMove(res, target.Name)
	}
	if o.DryRun {
		return res, nil
	}

	if !d.confirm(fmt.Sprintf("Move %s of %s into %q?",
		ui.Plural(res.Copies, "copy", "copies"),
		ui.Plural(res.Printings, "printing", "printings"), target.Name)) {
		return res, ErrNotConfirmed
	}

	moved, err := d.Store.MoveEntries(refs, target.ID)
	if err != nil {
		return res, err
	}
	res.Copies = moved
	return res, nil
}

func movableRefs(rows []hoardjson.Holding, targetID int64, res *MoveResult) ([]store.EntryRef, error) {
	var refs []store.EntryRef
	for _, row := range rows {
		if row.ContainerKind != "binder" {
			res.SkippedRows++
			if !slices.Contains(res.SkippedDecks, row.Container) {
				res.SkippedDecks = append(res.SkippedDecks, row.Container)
			}
			continue
		}
		if row.ContainerID == targetID {
			res.AlreadyThere += row.Count
			continue
		}
		if row.ContainerID == 0 {
			return nil, fmt.Errorf(
				"holding %q names no containerId; it came from a hoard older than schema 1.1.4",
				row.Card.Name)
		}
		fin, err := finish.Parse(row.Card.Finish)
		if err != nil {
			return nil, fmt.Errorf("holding %q: %w", row.Card.Name, err)
		}
		refs = append(refs, store.EntryRef{
			ContainerID: row.ContainerID,
			ScryfallID:  row.Card.ScryfallID,
			Finish:      fin,
			Condition:   row.Card.Condition,
			Board:       row.Board,
		})
		res.Copies += row.Count
		res.Printings++
		if row.PriceUsd != nil {
			res.Value += float64(row.Count) * *row.PriceUsd
		}
	}
	slices.Sort(res.SkippedDecks)
	return refs, nil
}

func nothingToMove(res MoveResult, target string) error {
	switch {
	case res.AlreadyThere > 0 && res.SkippedRows > 0:
		return fmt.Errorf("nothing to move: %s already in %q, and every other row is a deck card",
			ui.Plural(res.AlreadyThere, "copy is", "copies are"), target)
	case res.AlreadyThere > 0:
		return fmt.Errorf("nothing to move: every holding is already in %q", target)
	case res.SkippedRows > 0:
		return errors.New(
			"nothing to move: every row is a deck card, and move only touches binders")
	default:
		return errors.New("nothing to move: the document carries no holdings")
	}
}
