package browse

import (
	"strconv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/release"
)

const (
	setUpdateCheck = "update.check"
	setSkipVersion = "update.skipVersion"
)

// UpdateCheckFunc reports the tag of a release newer than the running build.
// An empty tag means there is nothing to offer; the comparison against the
// running version happens in the caller, which knows the build info.
type UpdateCheckFunc func() (latest string, err error)

func WithUpdateCheck(f UpdateCheckFunc) Option {
	return func(m *Model) { m.updateCheck = f }
}

type updateCheckStartMsg struct{}

type updateCheckMsg struct {
	latest string
	err    error

	// asked marks a check the user ran from the palette, which reports back
	// even when there is nothing to say. The startup check stays quiet.
	asked bool
}

// checkForUpdate runs the startup check off the main loop. Startup never waits
// on it, and a failure is silent: an unreachable GitHub is not something to
// interrupt someone looking at their cards.
func (m *Model) checkForUpdate() tea.Cmd {
	check := m.updateCheck
	if check == nil || !m.updateCheckEnabled() {
		return nil
	}
	return func() tea.Msg {
		latest, err := check()
		return updateCheckMsg{latest: latest, err: err}
	}
}

// requestUpdateCheck is the palette's UpdateHoard: the user asked, so it runs
// whatever the setting says and reports the outcome either way.
func (m *Model) requestUpdateCheck() tea.Cmd {
	check := m.updateCheck
	if check == nil {
		m.status, m.statusErr = "update checks are not available here", true
		return nil
	}
	m.status, m.statusErr = "checking for a newer hoard...", false
	return func() tea.Msg {
		latest, err := check()
		return updateCheckMsg{latest: latest, err: err, asked: true}
	}
}

func (m Model) updateCheckEnabled() bool {
	s, err := m.store.Settings()
	if err != nil {
		return true
	}
	if on, err := strconv.ParseBool(s[setUpdateCheck]); err == nil {
		return on
	}
	return true
}

func (m Model) skippedRelease() string {
	s, err := m.store.Settings()
	if err != nil {
		return ""
	}
	return s[setSkipVersion]
}

func (m Model) onUpdateCheck(msg updateCheckMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if msg.asked {
			m.status, m.statusErr = "checking for a newer hoard: "+msg.err.Error(), true
		}
		return m, nil
	}
	if msg.latest == "" {
		if msg.asked {
			m.status, m.statusErr = "hoard is up to date", false
		}
		return m, nil
	}
	if !msg.asked && m.skippedRelease() == msg.latest {
		return m, nil
	}

	// A prompt already on screen was asked for; this one was not.
	if m.confirm != nil {
		return m, nil
	}

	latest := msg.latest
	m.confirm = &pendingConfirm{
		prompt: "hoard " + latest + " is available. Open the download page?",
		help:   "y open · any other key skips this release",
		onYes: func(m *Model) tea.Cmd {
			m.status, m.statusErr =
				"hoard "+latest+" · run 'hoard update' for the upgrade command", false
			if m.openURL != nil {
				if err := m.openURL(release.ReleaseURL(latest)); err != nil {
					m.status, m.statusErr = "opening the download page: "+err.Error(), true
				}
			}
			return nil
		},
		onNo: func(m *Model) { m.rememberSkippedRelease(latest) },
	}
	return m, nil
}

func (m *Model) rememberSkippedRelease(tag string) {
	if err := m.store.SaveSettings(map[string]string{setSkipVersion: tag}); err != nil {
		m.status, m.statusErr = "saving update setting: "+err.Error(), true
	}
}
