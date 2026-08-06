// Package scan reads Magic card names from an iPhone running Hoardling, the
// companion app, reached through an external macOS helper that translates the
// phone's link into a pipe.
//
// The phone is the camera and the reader both: it captures, OCRs the card and
// sends finished events. Nothing here opens a camera, and there is no local
// fallback — the Continuity Camera backend that used to sit beside this one was
// removed once the phone proved better at every part of the job.
//
// A scan is a *session*, not a one-shot: Open holds the phone's camera up so a
// run of cards can be captured one after another. The Go side stays
// cross-platform, with stubs on platforms that have no helper.
package scan

import (
	"encoding/json"
	"errors"
)

// Event kinds emitted by a live capture session.
const (
	EventReady  = "ready"  // the phone's camera is up and previewing
	EventScan   = "scan"   // a capture was taken and read
	EventTorch  = "torch"  // phone torch toggled; see Event.State
	EventError  = "error"  // capture failed; the session is still alive
	EventClosed = "closed" // the session is over
	EventAuto   = "auto"   // auto-trigger state change; see Event.State
)

// Event is one message from a capture session.
type Event struct {
	Kind string `json:"event"`
	// Name is the best guess at the card name; Candidates holds it plus the
	// other lines read, best guess first. Set on EventScan.
	Name       string   `json:"name"`
	Candidates []string `json:"candidates"`
	// Rotation is the angle the phone applied before reading. Parsed but unused:
	// it was the Mac's preview correction back when the Mac owned a camera, and
	// the phone now hands over an already-upright frame. Kept on the struct
	// because it is still on the wire, and a field silently dropped from a
	// decoder is how a protocol forks.
	Rotation int    `json:"rotation"`
	Message  string `json:"message"`
	Device   string `json:"device"`
	// CollectorNumber and SetCode come from the card's bottom border, when it has
	// one. Cards printed before Exodus (1998) carry no collector number at all,
	// and only the M15 frame (2014) reliably prints the set code beside it, so
	// both are routinely empty. Treat an empty read as ordinary, not as failure.
	CollectorNumber string `json:"collectorNumber"`
	SetCode         string `json:"setCode"`
	// BottomLines is the raw text of the bottom band, for debugging a bad read.
	BottomLines []string `json:"bottomLines"`
	// Cards is every card the capture found, in reading order — a fanned
	// spread yields one entry per card. Empty from helpers that predate
	// multi-card scanning; use CardList, which falls back to the flat fields.
	Cards []Card `json:"cards"`
	// Confidence is Vision's confidence (0–1) in the line chosen as the title.
	// Zero from helpers that predate confidence reporting — treat zero as
	// unknown, never as "definitely bad".
	Confidence float64 `json:"confidence"`
	// BandAnchored reports whether the collector band was anchored to a
	// detected card rectangle rather than falling back to the frame's lower
	// half. Only an anchored band's collector read deserves trust.
	BandAnchored bool `json:"bandAnchored"`
	// Auto is true when the helper's auto trigger fired this scan rather than
	// a capture command or the space key.
	Auto bool `json:"auto"`
	// FireReason is why the trigger fired, when the source can say: a card
	// left the frame, a card was laid over the last one, or the parent's own
	// nudge asked for another look.
	//
	// The first two are placements the source watched happen; the third has no
	// physical evidence behind it at all. Empty from a helper too old to send
	// it, which is why the timing fallback survives.
	FireReason string `json:"fireReason,omitempty"`
	// HoldDelta and FaceDelta are the measurements behind FireReason: how far
	// the picture inside the pinned captured window had drifted from the card
	// shot through it, and how closely the nearest card still in frame
	// resembled that card. The source compares them against its own
	// cardChanged and movedFaceMax to pick the reason.
	//
	// Pointers because absent and zero are different answers. A comparison
	// that never ran is not a comparison that came back zero, and zero is the
	// reading for "identical picture" — the strongest same-card evidence
	// there is. Flattening the two would turn a missing measurement into the
	// most confident possible one.
	//
	// FaceDelta is meaningful only on FireReplaced and FireMoved, the two
	// reasons decided by measuring it. FireRemoved means nothing sat on the
	// watched rect to measure and FireNudge means no comparison ran, and the
	// source sends nil for both rather than whatever it last held.
	HoldDelta *float64 `json:"holdDelta,omitempty"`
	FaceDelta *float64 `json:"faceDelta,omitempty"`
	// Features lists source capabilities, advertised on EventReady ("auto"
	// means the phone understands auto-on/auto-off).
	Features []string `json:"features"`
	// State is the auto-trigger state carried by EventAuto, or the "on"/"off"
	// carried by EventTorch.
	State string `json:"state"`
	// CollectorAlts are collector blocks beyond the primary flat fields: a
	// card scanned off a stack shows a sliver of the card beneath it, whose
	// border parses as well as the target's. The caller keeps whichever
	// candidate matches a real printing.
	CollectorAlts []CollectorAlt `json:"collectorAlts"`
	// FinishHint is the primary block's printed finish marker: modern frames
	// star the set/language separator on foil printings and bullet it on
	// nonfoil ones. Empty means no marker was read — never a guess.
	FinishHint string `json:"finishHint"`
}

// HUDResult reports a resolved card's price outcome to the camera window's
// HUD. Tier set means flash it with the tier's styling and sound. Total set
// means update the persistent session counter — always silently, so a card
// is never a two-sound event. Amount nil with a price tier means the card is
// unpriced; the review tier carries no amount at all (it flashes "Needs
// Review" — the queued printing is unverified, so a price would be a guess).
type HUDResult struct {
	Amount *float64 `json:"amount,omitempty"`
	Tier   string   `json:"tier,omitempty"` // bulk | win | jackpot | unpriced | review
	Total  *float64 `json:"total,omitempty"`
	// Finish is what actually got written — "foil" or "nonfoil" — so a source
	// with a screen can say so.
	//
	// Decided here rather than on the phone, for the same reason the tier is:
	// this side has the catalog. The phone can only report whether it saw a
	// star between the set code and the language, which is a different and
	// weaker claim — plenty of foils print no marker at all, and a printing
	// that does not come in foil cannot be one however the glyph read.
	Finish string `json:"finish,omitempty"`
}

// CollectorAlt is one alternative collector block read from the band. Finish
// is the printed marker beside the set code — "foil" (star), "nonfoil"
// (bullet), or "" on frames that carry no marker.
type CollectorAlt struct {
	Number string `json:"number"`
	Set    string `json:"set"`
	Finish string `json:"finish"`
	// Source and Year mirror Card.NumberSource and Card.CopyrightYear for this
	// block; see those fields for the trust semantics.
	Source string `json:"source"`
	Year   int    `json:"year"`
}

// Card is one card of a capture. Name and Candidates mirror the event's flat
// fields; CollectorNumber and SetCode are set only when the helper could read
// them off this card specifically — keeping the pairing per card is the point,
// since pooling a frame's reads once matched one card's name with another
// card's printing.
type Card struct {
	Name            string   `json:"name"`
	Candidates      []string `json:"candidates"`
	CollectorNumber string   `json:"collectorNumber"`
	SetCode         string   `json:"setCode"`
	// Confidence is Vision's confidence in this entry's title read. Zero from
	// old helpers means unknown.
	Confidence float64 `json:"confidence"`
	// Source is the channel that produced the entry: "crop" (perspective-
	// corrected card rectangle — collector info is card-anchored by
	// construction) or "frame" (frame-wide title pass, never carries collector
	// info). Empty from old helpers.
	Source string `json:"source"`
	// CollectorAlts are collector blocks beyond the primary fields; see
	// Event.CollectorAlts.
	CollectorAlts []CollectorAlt `json:"collectorAlts"`
	// FinishHint is the printed finish marker; see Event.FinishHint.
	FinishHint string `json:"finishHint"`
	// FinishSource is which signal produced FinishHint: "separator" for the
	// modern set row's glyph, "sparkle" for the starburst a retro-frame foil
	// prints at the corner of its text box. Empty from helpers that predate it.
	//
	// Nothing reads this to make a decision, and that is deliberate — a finish
	// is evidence or it is not, whichever signal found it. It is carried so a
	// session's telemetry can show which one is doing the work, because the bug
	// this exists to fix was a signal that was silently absent on every old
	// frame and nobody could see it in the log.
	FinishSource string `json:"finishSource,omitempty"`
	// NumberSource is where CollectorNumber was read from. "" is the collector
	// band of a modern frame — self-consistent evidence, trusted enough that a
	// number matching no printing vetoes an auto-commit. "copyright" is the
	// tail of an old frame's bottom-centre copyright line ("… Wizards of the
	// Coast, Inc. 95/350"), tiny italic serif that misreads digits often enough
	// that it must only ever upgrade a match, never veto one. The Event's flat
	// CollectorNumber stays band-only: an old Go binary reading the flat fields
	// would treat any number there as trusted.
	NumberSource string `json:"numberSource"`
	// CopyrightYear is the end year of the copyright range on the same line
	// ("1993-2003" → 2003), which equals the printing's release year and can
	// break a collector-number tie between two printings of the same card.
	// Zero means no year was read.
	CopyrightYear int `json:"copyrightYear"`
	// BorderColor is the printed border read off the card's own edge: "white",
	// "black", or "" when the helper declined to say. Before 1998 no collector
	// number was printed at all, so the copyright year is the only printing
	// evidence those cards carry and it pins under a quarter of them; the
	// border is the era's other discriminator, and year plus border pins
	// nearly half. Fourth Edition is the pure case — 4ED (white, 1995) and
	// 4BB (black, 1995) share their year, art and artist, so nothing else
	// separates them.
	//
	// Weigh it differently from every other field here. A misread year or
	// number fails closed on its own, because it matches no printing and
	// evaporates. A border is one bit and always matches *something*, so it
	// cannot fail closed unaided and must never be sole evidence for a commit.
	BorderColor string `json:"borderColor"`
	// BorderSource is how the border was established: "footer+ring" when the
	// card's opposite edge agreed, "footer" when only one edge was in shot.
	// Empty when no border was read.
	BorderSource string `json:"borderSource"`
}

// Lines returns the card's OCR'd text, best guess first, falling back to Name
// when there is no candidate list.
func (c Card) Lines() []string {
	if len(c.Candidates) > 0 {
		return c.Candidates
	}
	if c.Name != "" {
		return []string{c.Name}
	}
	return nil
}

// Lines returns the OCR'd text of a scan event, best guess first, falling back
// to Name when the helper reported no candidate list.
func (e Event) Lines() []string {
	if len(e.Candidates) > 0 {
		return e.Candidates
	}
	if e.Name != "" {
		return []string{e.Name}
	}
	return nil
}

// CardList returns the capture's cards. A helper too old to report a card
// list is not an error: its flat fields describe exactly one card, so they
// become a list of one — the compatibility seam in a single place.
func (e Event) CardList() []Card {
	if len(e.Cards) > 0 {
		return e.Cards
	}
	if len(e.Lines()) == 0 {
		return nil
	}
	// The flat fields describe the frame-wide read. Its collector info is
	// card-anchored exactly when the band was, which is the property Source
	// encodes for real entries — so borrow the same vocabulary here.
	source := "frame"
	if e.BandAnchored {
		source = "crop"
	}
	return []Card{{
		Name:            e.Name,
		Candidates:      e.Candidates,
		CollectorNumber: e.CollectorNumber,
		SetCode:         e.SetCode,
		Confidence:      e.Confidence,
		Source:          source,
		CollectorAlts:   e.CollectorAlts,
		FinishHint:      e.FinishHint,
	}}
}

// parseEvent decodes one newline-delimited JSON event from the helper.
func parseEvent(data []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(data, &e); err != nil {
		return Event{}, err
	}
	if e.Kind == "" {
		return Event{}, errors.New("event has no kind")
	}
	return e, nil
}

// Fire reasons a source may report on a scan event.
//
// The first two are placements the source watched happen — a card left, or a
// card was laid over the last one. FireMoved is the same card sliding, which
// used to be reported as FireReplaced and is the reason one card could commit
// five times in six seconds. FireNudge has no physical evidence behind it at
// all: a timer expired on this side and asked for another look.
//
// Ordered by how much they license. Removed and Replaced are evidence a card
// was placed; Moved is evidence the opposite — the source is saying it believes
// this is the card it already read.
const (
	FireRemoved  = "removed"
	FireReplaced = "replaced"
	FireMoved    = "moved"
	// FireNudge is "nudged", not "nudge". The phone's enum case is `nudged` and
	// always has been; this constant said "nudge" from the day it was written,
	// so the branch matching on it never once ran. Nudge detection has been
	// silently falling through to the clock fallback the field was added to
	// replace — which is why a nudge re-look of an unreadable scene queued
	// instead of being killed as a phantom, 10s after the commit that armed it
	// and so outside the fallback's 4s window too.
	FireNudge = "nudged"
)

// Device is a phone the helper can capture from. Kind is a short human tag,
// always KindRemote today, kept because it is on the wire and because it is
// what a picker renders beside the name.
type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	// NeedsPairing is set by the caller, not by the helper: whether a device
	// requires a pairing code depends on what this machine has already paired
	// with, which the helper cannot know. Excluded from the wire for the same
	// reason.
	NeedsPairing bool `json:"-"`
}

// KindRemote is the Device.Kind an iPhone running the companion app carries.
// The helper sets it and the Go side reads it back, so the two spellings have
// to stay in step. Every device carries it now that there is one kind of
// source; it survives as the wire's own word for what a source is.
const KindRemote = "Hoardling"

// OpenOptions says which phone to open and how.
//
// A struct rather than positional arguments: a session is identified by a
// device *and* a code, and two bare strings at every call site would make it
// easy to swap them.
type OpenOptions struct {
	// DeviceID is the paired phone to use; empty lets the helper pick the only
	// one it can see.
	DeviceID string
	// PairingCode authorizes the session. Six digits, shown on the app's Pair
	// tab, remembered per device in scan.json. A session without one cannot
	// complete the handshake, so this is required in practice.
	PairingCode string
	// Mirror asks for a preview window on this Mac too. Off by default: the
	// phone shows the preview, the price and the cue, and a second window is a
	// second place to look during a session.
	Mirror bool
}

var (
	// ErrUnsupported is returned on platforms without a scan helper.
	ErrUnsupported = errors.New("card scanning is only available on macOS builds")
	// ErrHelperMissing means the native helper binary was not found.
	ErrHelperMissing = errors.New("hoard-scan helper not found; build it with ./build-scan.sh")
)

// deviceList matches the JSON the helper prints for --list-devices.
type deviceList struct {
	Devices []Device `json:"devices"`
}

// parseDevices turns the helper's --list-devices JSON into a device slice.
func parseDevices(data []byte) ([]Device, error) {
	var dl deviceList
	if err := json.Unmarshal(data, &dl); err != nil {
		return nil, err
	}
	return dl.Devices, nil
}
