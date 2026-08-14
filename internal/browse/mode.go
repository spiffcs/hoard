package browse

type inputMode int

const (
	modeBrowse inputMode = iota

	modeConfirm

	modeAddChild

	modePrompt

	modePalette

	modeFilter

	modeDetail

	modeText
)

func (m Model) mode() inputMode {
	switch {
	case m.confirm != nil:
		return modeConfirm
	case m.addChild != nil:
		return modeAddChild
	case m.prompt != nil:
		return modePrompt
	case m.palette != nil:
		return modePalette
	case m.filtering:
		return modeFilter
	case m.detail != nil:
		return modeDetail
	case m.text != nil:
		return modeText
	}
	return modeBrowse
}
