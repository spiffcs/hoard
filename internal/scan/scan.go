package scan

import (
	"encoding/json"
	"errors"
)

const (
	EventReady  = "ready"
	EventScan   = "scan"
	EventTorch  = "torch"
	EventError  = "error"
	EventClosed = "closed"
	EventAuto   = "auto"

	EventPromote = "promote"
)

type Event struct {
	Kind string `json:"event"`

	Name       string   `json:"name"`
	Candidates []string `json:"candidates"`

	Rotation int    `json:"rotation"`
	Message  string `json:"message"`
	Device   string `json:"device"`

	CollectorNumber string `json:"collectorNumber"`
	SetCode         string `json:"setCode"`

	BottomLines []string `json:"bottomLines"`

	Cards []Card `json:"cards"`

	Confidence float64 `json:"confidence"`

	BandAnchored bool `json:"bandAnchored"`

	Auto bool `json:"auto"`

	FireReason string `json:"fireReason,omitempty"`

	HoldDelta *float64 `json:"holdDelta,omitempty"`
	FaceDelta *float64 `json:"faceDelta,omitempty"`

	Features []string `json:"features"`

	AppVersion string `json:"appVersion"`

	State string `json:"state"`

	CollectorAlts []CollectorAlt `json:"collectorAlts"`

	FinishHint string `json:"finishHint"`

	Language string `json:"language,omitempty"`
}

type HUDResult struct {
	Amount *float64 `json:"amount,omitempty"`
	Tier   string   `json:"tier,omitempty"`
	Total  *float64 `json:"total,omitempty"`

	Name string `json:"name,omitempty"`

	Finish string `json:"finish,omitempty"`

	Note string `json:"note,omitempty"`

	Promote bool `json:"promote,omitempty"`
}

type CollectorAlt struct {
	Number string `json:"number"`
	Set    string `json:"set"`
	Finish string `json:"finish"`

	Source string `json:"source"`
	Year   int    `json:"year"`

	Language string `json:"language,omitempty"`
}

type Card struct {
	Name            string   `json:"name"`
	Candidates      []string `json:"candidates"`
	CollectorNumber string   `json:"collectorNumber"`
	SetCode         string   `json:"setCode"`

	Confidence float64 `json:"confidence"`

	Source string `json:"source"`

	CollectorAlts []CollectorAlt `json:"collectorAlts"`

	FinishHint string `json:"finishHint"`

	Language string `json:"language,omitempty"`

	FinishSource string `json:"finishSource,omitempty"`

	SparkleScore    *float64 `json:"sparkleScore,omitempty"`
	SparkleOffsetU  *float64 `json:"sparkleOffsetU,omitempty"`
	SparkleOffsetV  *float64 `json:"sparkleOffsetV,omitempty"`
	SparkleContrast *float64 `json:"sparkleContrast,omitempty"`

	SparkleChromaScore    *float64 `json:"sparkleChromaScore,omitempty"`
	SparkleChromaContrast *float64 `json:"sparkleChromaContrast,omitempty"`

	NumberSource string `json:"numberSource"`

	CopyrightYear int `json:"copyrightYear"`

	BorderColor string `json:"borderColor"`

	BorderSource string `json:"borderSource"`

	FrameStyle string `json:"frameStyle"`
}

func (c Card) Lines() []string {
	if len(c.Candidates) > 0 {
		return c.Candidates
	}
	if c.Name != "" {
		return []string{c.Name}
	}
	return nil
}

func (e Event) Lines() []string {
	if len(e.Candidates) > 0 {
		return e.Candidates
	}
	if e.Name != "" {
		return []string{e.Name}
	}
	return nil
}

func (e Event) CardList() []Card {
	if len(e.Cards) > 0 {
		return e.Cards
	}
	if len(e.Lines()) == 0 {
		return nil
	}

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
		Language:        e.Language,
	}}
}

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

const (
	FireRemoved  = "removed"
	FireReplaced = "replaced"
	FireMoved    = "moved"

	FireNudge = "nudged"
)

type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

const KindRemote = "Hoardling"

type OpenOptions struct {
	DeviceID string

	PairingCode string
}
