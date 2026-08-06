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

func TestCardListFallsBackToFlatFields(t *testing.T) {
	// A helper too old to report a card list still describes exactly one card
	// in its flat fields; CardList is the one place that compatibility lives.
	ev, err := parseEvent([]byte(
		`{"event":"scan","name":"Sol Ring","candidates":["Sol Ring","Artifact"],` +
			`"collectorNumber":"125","setCode":"C21"}`))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	cards := ev.CardList()
	if len(cards) != 1 {
		t.Fatalf("cards = %d, want the flat fields as one card", len(cards))
	}
	c := cards[0]
	if c.Name != "Sol Ring" || c.SetCode != "C21" || c.CollectorNumber != "125" || len(c.Candidates) != 2 {
		t.Errorf("card = %+v, want the flat fields carried over", c)
	}
	// And a frame with nothing readable yields no cards, not one empty card.
	if got := (Event{Kind: EventScan}).CardList(); got != nil {
		t.Errorf("empty event CardList = %+v, want nil", got)
	}
}

func TestParseEventConfidenceFields(t *testing.T) {
	ev, err := parseEvent([]byte(
		`{"event":"scan","name":"Sol Ring","confidence":0.94,"bandAnchored":true,"auto":true,` +
			`"cards":[{"name":"Sol Ring","confidence":0.94,"source":"crop"}]}`))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if ev.Confidence != 0.94 || !ev.BandAnchored || !ev.Auto {
		t.Errorf("event signals = %v/%v/%v, want 0.94/true/true",
			ev.Confidence, ev.BandAnchored, ev.Auto)
	}
	if c := ev.Cards[0]; c.Confidence != 0.94 || c.Source != "crop" {
		t.Errorf("card signals = %v/%q, want 0.94/crop", c.Confidence, c.Source)
	}

	// An old helper omits every new field; zero values must read as unknown,
	// not as an error.
	ev, err = parseEvent([]byte(`{"event":"scan","name":"Sol Ring"}`))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if ev.Confidence != 0 || ev.BandAnchored || ev.Auto {
		t.Errorf("old-helper event should carry zero signals, got %+v", ev)
	}

	// The ready event's feature list and the auto event's state ride the same
	// struct.
	ev, err = parseEvent([]byte(`{"event":"ready","device":"iPhone","features":["auto"]}`))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if len(ev.Features) != 1 || ev.Features[0] != "auto" {
		t.Errorf("features = %v, want [auto]", ev.Features)
	}
	ev, err = parseEvent([]byte(`{"event":"auto","state":"held"}`))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if ev.Kind != EventAuto || ev.State != "held" {
		t.Errorf("auto event = %+v, want state held", ev)
	}
}

func TestParseEventCopyrightProvenance(t *testing.T) {
	// An old-frame read: the number came off the copyright line, with the
	// range's end year alongside, and an alternative block carries the same
	// provenance vocabulary.
	ev, err := parseEvent([]byte(
		`{"event":"scan","name":"Remove Soul",` +
			`"cards":[{"name":"Remove Soul","collectorNumber":"95",` +
			`"numberSource":"copyright","copyrightYear":2003,` +
			`"collectorAlts":[{"number":"93","source":"copyright","year":2001}]}]}`))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	c := ev.CardList()[0]
	if c.NumberSource != "copyright" || c.CopyrightYear != 2003 {
		t.Errorf("provenance = %q/%d, want copyright/2003", c.NumberSource, c.CopyrightYear)
	}
	if a := c.CollectorAlts[0]; a.Source != "copyright" || a.Year != 2001 {
		t.Errorf("alt provenance = %q/%d, want copyright/2001", a.Source, a.Year)
	}

	// A helper that predates provenance omits the fields: zero values mean a
	// trusted band read, which is what every number from such a helper is.
	ev, err = parseEvent([]byte(
		`{"event":"scan","name":"Sol Ring","cards":[{"name":"Sol Ring","collectorNumber":"125",` +
			`"collectorAlts":[{"number":"126"}]}]}`))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	c = ev.CardList()[0]
	if c.NumberSource != "" || c.CopyrightYear != 0 {
		t.Errorf("old-helper card should carry zero provenance, got %+v", c)
	}
	if a := c.CollectorAlts[0]; a.Source != "" || a.Year != 0 {
		t.Errorf("old-helper alt should carry zero provenance, got %+v", a)
	}
}

func TestCardListCarriesAnchoringIntoFallback(t *testing.T) {
	// The synthesized flat-fields card borrows Source's vocabulary: its
	// collector read is card-anchored exactly when the band was.
	ev, _ := parseEvent([]byte(
		`{"event":"scan","name":"Sol Ring","collectorNumber":"125","setCode":"C21",` +
			`"confidence":0.9,"bandAnchored":true}`))
	if c := ev.CardList()[0]; c.Source != "crop" || c.Confidence != 0.9 {
		t.Errorf("anchored fallback card = %+v, want source crop", c)
	}
	ev, _ = parseEvent([]byte(`{"event":"scan","name":"Sol Ring"}`))
	if c := ev.CardList()[0]; c.Source != "frame" {
		t.Errorf("unanchored fallback card = %+v, want source frame", c)
	}
}

func TestCardListPassesThroughCards(t *testing.T) {
	ev, err := parseEvent([]byte(
		`{"event":"scan","name":"Ulamog, the Infinite Gyre",` +
			`"cards":[{"name":"Ulamog, the Infinite Gyre","candidates":["Ulamog, the Infinite Gyre"],"setCode":"UMA","collectorNumber":"7"},` +
			`{"name":"Emrakul, the World Anew","candidates":["Emrakul, the World Anew"]}]}`))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	cards := ev.CardList()
	if len(cards) != 2 {
		t.Fatalf("cards = %d, want the list as sent", len(cards))
	}
	if cards[0].SetCode != "UMA" || cards[0].CollectorNumber != "7" {
		t.Errorf("card 0 lost its printing: %+v", cards[0])
	}
	if cards[1].Name != "Emrakul, the World Anew" || cards[1].SetCode != "" {
		t.Errorf("card 1 = %+v, want name-only", cards[1])
	}
	if lines := cards[1].Lines(); len(lines) != 1 || lines[0] != "Emrakul, the World Anew" {
		t.Errorf("card Lines = %v", lines)
	}
}

// The trigger's measurements decode when sent, and stay nil when not.
//
// Nil is the load-bearing case. A helper too old to send these fields must
// leave the parent on its timing fallback, and the distinction between "the
// comparison never ran" and "the comparison returned zero" is the reason the
// fields are pointers: zero is the reading for an identical picture, which is
// the strongest same-card evidence there is. Decoding an absent field into it
// would turn a missing measurement into the most confident possible one.
func TestParseEventTriggerDeltas(t *testing.T) {
	ev, err := parseEvent([]byte(
		`{"event":"scan","name":"No-Dachi","fireReason":"replaced",` +
			`"holdDelta":34.3,"faceDelta":32.5}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.FireReason != FireReplaced {
		t.Errorf("FireReason = %q, want %q", ev.FireReason, FireReplaced)
	}
	if ev.HoldDelta == nil || *ev.HoldDelta != 34.3 {
		t.Errorf("HoldDelta = %v, want 34.3", ev.HoldDelta)
	}
	if ev.FaceDelta == nil || *ev.FaceDelta != 32.5 {
		t.Errorf("FaceDelta = %v, want 32.5", ev.FaceDelta)
	}

	// A helper that predates the fields.
	old, err := parseEvent([]byte(`{"event":"scan","name":"No-Dachi","fireReason":"replaced"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if old.HoldDelta != nil || old.FaceDelta != nil {
		t.Errorf("deltas = %v/%v, want nil from a helper that sends neither",
			old.HoldDelta, old.FaceDelta)
	}

	// And a genuine zero is not the same answer as an absent field.
	zero, err := parseEvent([]byte(`{"event":"scan","name":"No-Dachi","faceDelta":0}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if zero.FaceDelta == nil || *zero.FaceDelta != 0 {
		t.Errorf("FaceDelta = %v, want a decoded zero", zero.FaceDelta)
	}
}

func TestParseEventFinishProvenance(t *testing.T) {
	// Two ways a finish arrives, and the hint itself is identical either way —
	// the Go side treats a present finish as evidence whatever found it. Only
	// the provenance differs, and it exists so a session's log can show which
	// signal is carrying the answer.
	ev, err := parseEvent([]byte(
		`{"event":"scan","name":"Glowrider","cards":[{"name":"Glowrider",` +
			`"finishHint":"foil","finishSource":"sparkle","copyrightYear":2003}]}`))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	c := ev.CardList()[0]
	if c.FinishHint != "foil" || c.FinishSource != "sparkle" {
		t.Errorf("finish = %q/%q, want foil/sparkle", c.FinishHint, c.FinishSource)
	}

	ev, err = parseEvent([]byte(
		`{"event":"scan","name":"Deserted Temple","cards":[{"name":"Deserted Temple",` +
			`"finishHint":"foil","finishSource":"separator"}]}`))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if c := ev.CardList()[0]; c.FinishSource != "separator" {
		t.Errorf("finishSource = %q, want separator", c.FinishSource)
	}

	// A helper that predates the field omits it. That must read as "no
	// provenance recorded" and never as "no finish" — the hint is what decides,
	// and an old helper's foil is still a foil.
	ev, err = parseEvent([]byte(
		`{"event":"scan","name":"Sol Ring","cards":[{"name":"Sol Ring","finishHint":"foil"}]}`))
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	c = ev.CardList()[0]
	if c.FinishHint != "foil" {
		t.Errorf("old helper's finish dropped: %q", c.FinishHint)
	}
	if c.FinishSource != "" {
		t.Errorf("old helper should carry no provenance, got %q", c.FinishSource)
	}
}
