package scan

import (
	"testing"
)

func TestParseEvent(t *testing.T) {
	ev, err := parseEvent([]byte(
		`{"event":"scan","name":"Sol Ring","candidates":["Sol Ring","Artifact"],"rotation":90}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Kind != EventScan || ev.Name != "Sol Ring" || ev.Rotation != 90 {
		t.Errorf("event wrong: %+v", ev)
	}
	if len(ev.Candidates) != 2 {
		t.Errorf("candidates = %v, want 2", ev.Candidates)
	}

	// Collector info read off the card's bottom border.
	ev, err = parseEvent([]byte(
		`{"event":"scan","name":"Sol Ring","collectorNumber":"123","setCode":"MH3",` +
			`"bottomLines":["0123/0281 R","MH3 • EN"]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.CollectorNumber != "123" || ev.SetCode != "MH3" {
		t.Errorf("collector info = %q/%q, want 123/MH3", ev.CollectorNumber, ev.SetCode)
	}
	if len(ev.BottomLines) != 2 {
		t.Errorf("bottomLines = %v, want 2", ev.BottomLines)
	}

	// A helper too old to report collector info is not an error: cards printed
	// before 1998 have no number to read either, so empty is the normal case.
	ev, err = parseEvent([]byte(`{"event":"scan","name":"Black Lotus"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.CollectorNumber != "" || ev.SetCode != "" {
		t.Errorf("want empty collector info, got %q/%q", ev.CollectorNumber, ev.SetCode)
	}

	// A line without a kind isn't one of ours; the reader skips it rather than
	// treating an unknown shape as a scan.
	if _, err := parseEvent([]byte(`{"name":"Sol Ring"}`)); err == nil {
		t.Error("expected an error for an event with no kind")
	}
	if _, err := parseEvent([]byte(`not json`)); err == nil {
		t.Error("expected an error for malformed JSON")
	}
}

func TestEventLines(t *testing.T) {
	// Candidates win: they include the best guess first and give the caller
	// fallbacks when the top-line guess is wrong.
	ev := Event{Kind: EventScan, Name: "Sol Ring", Candidates: []string{"Sol Ring", "Artifact"}}
	if got := ev.Lines(); len(got) != 2 || got[0] != "Sol Ring" {
		t.Errorf("Lines() = %v, want the candidate list", got)
	}

	// An older helper that only reports a name still yields one usable line.
	ev = Event{Kind: EventScan, Name: "Sol Ring"}
	if got := ev.Lines(); len(got) != 1 || got[0] != "Sol Ring" {
		t.Errorf("Lines() = %v, want [Sol Ring]", got)
	}

	if got := (Event{Kind: EventScan}).Lines(); got != nil {
		t.Errorf("Lines() = %v, want nil when nothing was read", got)
	}
}

func TestParseDevices(t *testing.T) {
	devs, err := parseDevices([]byte(
		`{"devices":[{"id":"a","name":"Chris's iPhone","kind":"iPhone"},` +
			`{"id":"b","name":"Old iPhone","kind":"iPhone"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(devs) != 2 {
		t.Fatalf("got %d devices, want 2", len(devs))
	}
	if devs[0].ID != "a" || devs[0].Name != "Chris's iPhone" || devs[0].Kind != "iPhone" {
		t.Errorf("first device wrong: %+v", devs[0])
	}

	// No cameras attached is an empty list, not an error — the caller decides
	// how to tell the user.
	if devs, err := parseDevices([]byte(`{"devices":[]}`)); err != nil || len(devs) != 0 {
		t.Errorf("empty list: got %v, %v", devs, err)
	}
	if _, err := parseDevices([]byte(`not json`)); err == nil {
		t.Error("expected error for malformed JSON")
	}
}
