package store

import "testing"

func TestASecondLookbackIsNeverServedFromStaleStats(t *testing.T) {
	s := trendMixStore(t)

	wide := trendOpts()
	wide.Since = "2026-05-01T00:00:00Z"

	if _, _, err := s.Trends(trendOpts()); err != nil {
		t.Fatalf("warming the default window: %v", err)
	}
	_, momentum, err := s.Trends(wide)
	if err != nil {
		t.Fatalf("warming the wide window: %v", err)
	}
	if _, ok := rowNamed(momentum, "Steady Climber"); !ok {
		t.Fatalf("Steady Climber should start out climbing in the wide window, got %v",
			namesInOrder(momentum))
	}

	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}

	if _, _, err := s.Trends(trendOpts()); err != nil {
		t.Fatalf("rereading the default window: %v", err)
	}

	_, momentum, err = s.Trends(wide)
	if err != nil {
		t.Fatalf("rereading the wide window: %v", err)
	}
	if _, ok := rowNamed(momentum, "Steady Climber"); ok {
		t.Errorf("wide-window momentum = %v, want Steady Climber gone — it took a step down, "+
			"and rebuilding one lookback must not leave another serving pre-change numbers",
			namesInOrder(momentum))
	}
}
