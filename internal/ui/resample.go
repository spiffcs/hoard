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

	stamps := make([]time.Time, len(points))
	parsed := make([]bool, len(points))
	for i, p := range points {
		t, err := time.Parse(time.RFC3339, p.AsOf)
		stamps[i], parsed[i] = t, err == nil
	}
	if !parsed[0] || !parsed[len(points)-1] {
		return nil
	}

	first, last := stamps[0], stamps[len(points)-1]
	span := last.Sub(first)
	if span <= 0 {
		return []float64{points[len(points)-1].Value}
	}

	out := make([]float64, 0, buckets)
	at := 0
	for i := range buckets {
		edge := first.Add(time.Duration(float64(span) * float64(i+1) / float64(buckets)))
		for at+1 < len(points) {
			if !parsed[at+1] || stamps[at+1].After(edge) {
				break
			}
			at++
		}
		out = append(out, points[at].Value)
	}
	return out
}
