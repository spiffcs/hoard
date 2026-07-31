package ui

import "time"

// TimePoint is one dated value, the input Resample buckets onto an even time
// base. AsOf is RFC 3339, as the store writes its history.
type TimePoint struct {
	AsOf  string
	Value float64
}

// Resample turns irregular observations into evenly-spaced values for Spark.
//
// Only the days a value moved are stored, so plotting by index would give a
// volatile day the same width as three quiet weeks — inverting what a
// sparkline is for, since the flat stretches are the information.
//
// Each bucket takes the last value at or before its end: the series is a step
// function, not samples to interpolate between.
func Resample(points []TimePoint, buckets int) []float64 {
	if len(points) == 0 || buckets <= 0 {
		return nil
	}
	if len(points) == 1 {
		return []float64{points[0].Value}
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
		return []float64{points[len(points)-1].Value}
	}

	out := make([]float64, 0, buckets)
	at := 0
	for i := range buckets {
		// The right edge of bucket i, so the last bucket lands exactly on the
		// final observation and the sparkline ends at the current value.
		edge := first.Add(time.Duration(float64(span) * float64(i+1) / float64(buckets)))
		for at+1 < len(points) {
			t, err := time.Parse(time.RFC3339, points[at+1].AsOf)
			if err != nil || t.After(edge) {
				break
			}
			at++
		}
		out = append(out, points[at].Value)
	}
	return out
}
