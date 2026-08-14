package ui

import "time"

type TimePoint struct {
	AsOf  string
	Value float64
}

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
