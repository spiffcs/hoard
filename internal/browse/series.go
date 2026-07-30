package browse

import (
	"time"

	"github.com/spiffcs/hoard/internal/store"
)

// resample turns price observations into evenly-spaced values for a sparkline.
//
// Only the days a price actually moved are stored, so the observations are
// irregularly spaced. Plotting them by index would give a day on which the
// price moved three times the same width as three quiet weeks, which inverts
// what a sparkline is for: the flat stretches are the information.
//
// Each bucket takes the last price observed at or before its end — the series
// is a step function, since a price stands until a later one replaces it, not a
// set of samples to interpolate between. Buckets before the first observation
// are dropped rather than back-filled, so a card first seen halfway through the
// window renders half a line instead of a fictional flat start.
func resample(points []store.PricePoint, buckets int) []float64 {
	if len(points) == 0 || buckets <= 0 {
		return nil
	}
	if len(points) == 1 {
		return []float64{points[0].Price}
	}

	first, err := time.Parse(time.RFC3339, points[0].AsOf)
	if err != nil {
		return nil
	}
	last, err := time.Parse(time.RFC3339, points[len(points)-1].AsOf)
	if err != nil {
		return nil
	}
	span := last.Sub(first)
	if span <= 0 {
		return []float64{points[len(points)-1].Price}
	}

	out := make([]float64, 0, buckets)
	at := 0
	for i := range buckets {
		// The right edge of bucket i, so the last bucket lands exactly on the
		// final observation and the sparkline ends at today's price.
		edge := first.Add(time.Duration(float64(span) * float64(i+1) / float64(buckets)))
		for at+1 < len(points) {
			t, err := time.Parse(time.RFC3339, points[at+1].AsOf)
			if err != nil || t.After(edge) {
				break
			}
			at++
		}
		out = append(out, points[at].Price)
	}
	return out
}
