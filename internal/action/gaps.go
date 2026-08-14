package action

import (
	"context"
	"fmt"
	"strings"

	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/progress"
)

type GapReport = pricing.GapReport

func FillGaps(ctx context.Context, d Deps, p progress.Fn) (GapReport, error) {
	f := d.pricer().WithProgress(func(msg string) {
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
