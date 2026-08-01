package action

import (
	"context"
	"fmt"
	"strings"

	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/progress"
)

// GapReport re-exports pricing's report so frontends need only this package
// for the result shapes they render.
type GapReport = pricing.GapReport

// FillGaps prices what Scryfall could not, narrating what happened: the
// fetcher's own messages and the outcome both flow as Notes, so the
// sequence reads the same on the CLI's stderr as in a TUI status line. The
// returned report carries the same facts for rendering after the fact —
// notes are droppable, results are not.
func FillGaps(ctx context.Context, d Deps, p progress.Fn) (GapReport, error) {
	f := pricing.New(d.Store, d.CacheDir).WithProgress(func(msg string) {
		p.Emit(progress.Event{Step: "filling price gaps", Note: msg})
	})
	rep, err := f.FillGaps(ctx)
	if err != nil || rep.Gaps == 0 {
		return rep, err
	}
	note := func(format string, args ...any) {
		p.Emit(progress.Event{Step: "filling price gaps", Note: fmt.Sprintf(format, args...)})
	}
	if rep.Skipped {
		note("%d cards have no price for a finish you own; MTGJSON had none when last asked.", rep.Gaps)
		return rep, nil
	}
	note("%d cards have no price for a finish you own; checking MTGJSON...", rep.Gaps)
	if rep.Filled <= 0 {
		note("no other source could price them either.")
		return rep, nil
	}
	note("filled %d from %s.", rep.Filled, strings.Join(rep.Sources, ", "))
	if rep.Remaining > 0 {
		note("%d still unpriced anywhere.", rep.Remaining)
	}
	return rep, nil
}
