package browse

// inputMode is who owns the keyboard. It is derived, not stored: the state
// fields (m.confirm, m.filtering, m.detail, …) remain the single source of
// truth, and this enum is the one place their precedence lives — input
// dispatch and rendering both switch on it, so they can never disagree
// about which surface the user is in.
type inputMode int

const (
	modeBrowse inputMode = iota
	// modeConfirm outranks everything: a staged destructive action must
	// never be preempted, so the prompt cannot be left hanging while the
	// cursor wanders off the row it refers to. (In practice confirm and the
	// filter bar are mutually exclusive — the confirm consumes every key,
	// including the one that would open the bar — so ranking confirm first
	// changes no observable behavior.)
	modeConfirm
	// modePrompt is an inline input mid-flight (usually opened by an
	// action); its text entry owns printable keys.
	modePrompt
	// modePalette is the command drawer; it precedes filter and detail so
	// ':' works from the overlay.
	modePalette
	// modeFilter: while the bar is open it takes every printable key, or
	// typing "q" in a card name would quit.
	modeFilter
	// modeDetail: the overlay covers the panes, so it takes the keys that
	// would otherwise move a cursor the reader cannot see.
	modeDetail
)

// mode reports who owns the keyboard right now.
func (m Model) mode() inputMode {
	switch {
	case m.confirm != nil:
		return modeConfirm
	case m.prompt != nil:
		return modePrompt
	case m.palette != nil:
		return modePalette
	case m.filtering:
		return modeFilter
	case m.detail != nil:
		return modeDetail
	}
	return modeBrowse
}
