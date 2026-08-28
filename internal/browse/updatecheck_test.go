package browse

import (
	"errors"
	"strings"
	"testing"
)

type openSpy struct{ urls []string }

func (o *openSpy) open(u string) error {
	o.urls = append(o.urls, u)
	return nil
}

func offerModel(t *testing.T, f *fakeStore) (Model, *openSpy) {
	t.Helper()
	spy := &openSpy{}
	m := atAllCards(t, newTestModel(t, f))
	m.openURL = spy.open
	m.updateCheck = func() (string, error) { return "v0.4.1", nil }
	return m, spy
}

func offer(t *testing.T, m Model, latest string, err error) Model {
	t.Helper()
	next, _ := m.Update(updateCheckMsg{latest: latest, err: err})
	return next.(Model)
}

func TestANewerReleaseIsOfferedAtStartup(t *testing.T) {
	m, _ := offerModel(t, testStore())

	m = offer(t, m, "v0.4.1", nil)

	if m.confirm == nil {
		t.Fatal("a newer release raised no prompt")
	}
	if !strings.Contains(m.confirm.prompt, "v0.4.1") {
		t.Errorf("prompt does not name the release: %q", m.confirm.prompt)
	}
}

func TestAcceptingTheOfferOpensTheDownloadPage(t *testing.T) {
	m, spy := offerModel(t, testStore())
	m = offer(t, m, "v0.4.1", nil)

	m = key(m, "y")

	if len(spy.urls) != 1 {
		t.Fatalf("opened %d URLs, want 1: %v", len(spy.urls), spy.urls)
	}
	if !strings.Contains(spy.urls[0], "v0.4.1") {
		t.Errorf("opened %q, want a URL naming the release", spy.urls[0])
	}
}

// The binary is never replaced, so accepting must leave the upgrade command
// somewhere a user without a browser can still read it.
func TestAcceptingTheOfferLeavesTheUpgradeCommandOnScreen(t *testing.T) {
	m, _ := offerModel(t, testStore())
	m = offer(t, m, "v0.4.1", nil)

	m = key(m, "y")

	if !strings.Contains(m.status, "v0.4.1") {
		t.Errorf("status after accepting = %q, want it to name the release", m.status)
	}
}

func TestDecliningRemembersTheVersion(t *testing.T) {
	f := testStore()
	m, _ := offerModel(t, f)
	m = offer(t, m, "v0.4.1", nil)

	m = key(m, "n")

	if got := f.settings[setSkipVersion]; got != "v0.4.1" {
		t.Errorf("%s = %q, want %q", setSkipVersion, got, "v0.4.1")
	}
}

func TestASkippedVersionIsNotOfferedAgain(t *testing.T) {
	f := testStore()
	f.settings = map[string]string{setSkipVersion: "v0.4.1"}
	m, _ := offerModel(t, f)

	m = offer(t, m, "v0.4.1", nil)

	if m.confirm != nil {
		t.Errorf("a declined release was offered again: %q", m.confirm.prompt)
	}
}

func TestANewerReleaseThanTheSkippedOneIsStillOffered(t *testing.T) {
	f := testStore()
	f.settings = map[string]string{setSkipVersion: "v0.4.1"}
	m, _ := offerModel(t, f)

	m = offer(t, m, "v0.5.0", nil)

	if m.confirm == nil {
		t.Error("skipping v0.4.1 also silenced v0.5.0")
	}
}

func TestNothingToOfferRaisesNoPrompt(t *testing.T) {
	m, _ := offerModel(t, testStore())

	m = offer(t, m, "", nil)

	if m.confirm != nil {
		t.Errorf("an empty tag raised a prompt: %q", m.confirm.prompt)
	}
}

func TestAFailedCheckIsSilent(t *testing.T) {
	m, _ := offerModel(t, testStore())

	m = offer(t, m, "", errors.New("no route to host"))

	if m.confirm != nil {
		t.Errorf("a failed check raised a prompt: %q", m.confirm.prompt)
	}
	if m.statusErr {
		t.Errorf("a failed check complained in the status line: %q", m.status)
	}
}

func TestTheCheckCanBeTurnedOff(t *testing.T) {
	f := testStore()
	f.settings = map[string]string{setUpdateCheck: "false"}
	m, _ := offerModel(t, f)

	m = pump(t, m, m.Init())

	if m.confirm != nil {
		t.Errorf("the check ran with %s=false: %q", setUpdateCheck, m.confirm.prompt)
	}
}

func TestStartupSchedulesTheCheckWithoutBlocking(t *testing.T) {
	f := testStore()
	var calls int
	m := atAllCards(t, newTestModel(t, f))
	m.updateCheck = func() (string, error) { calls++; return "v0.4.1", nil }

	m = pump(t, m, m.Init())

	if calls != 1 {
		t.Fatalf("startup ran the check %d times, want exactly 1", calls)
	}
	if m.confirm == nil {
		t.Error("the startup check produced no offer")
	}
}

// A prompt already on screen must not be clobbered by the update offer.
func TestTheOfferDoesNotStealAPromptAlreadyOnScreen(t *testing.T) {
	m, _ := offerModel(t, testStore())
	m.confirm = &pendingConfirm{prompt: "Remove Sol Ring?", help: "y remove"}

	m = offer(t, m, "v0.4.1", nil)

	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "Sol Ring") {
		t.Errorf("the update offer replaced the prompt already on screen: %#v", m.confirm)
	}
}

func TestTheUpdateCommandIsInThePalette(t *testing.T) {
	var found *command
	for i, c := range commands() {
		if c.title == "UpdateHoard" {
			found = &commands()[i]
			break
		}
	}
	if found == nil {
		t.Fatal("no UpdateHoard command in the palette")
	}
	if found.run == nil {
		t.Error("UpdateHoard has no action")
	}
}
