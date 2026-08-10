package catalog

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// Cards returns what the catalog knows about the requested printings, keyed by
// Scryfall id. Unknown ids are simply absent; callers ask the API for the
// remainder rather than treating a miss as "no such card".
//
// The returned cards carry no Raw document — the catalog stores only what a lookup
// needs, so store.IDsNeedingDocuments closes that gap once rather than per refresh.
func (c *Catalog) Cards(ids []string) (map[string]scryfall.Card, error) {
	out := make(map[string]scryfall.Card, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	// Chunked because SQLite's parameter limit is finite and a collection can be
	// tens of thousands of cards. 500 keeps each statement well inside it.
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		end := min(start+chunk, len(ids))
		batch := ids[start:end]

		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		q := `SELECT ` + cardColumns + `
		      FROM cards WHERE scryfall_id IN (?` + strings.Repeat(",?", len(batch)-1) + `)`

		rows, err := c.db.Query(q, args...)
		if err != nil {
			return nil, fmt.Errorf("catalog: reading cards: %w", err)
		}
		for rows.Next() {
			card, err := scanCard(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[card.ID] = card
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// rowScanner is what both Query and QueryRow results satisfy.
type rowScanner interface{ Scan(...any) error }

// cardColumns is the projection scanCard reads, in its exact order. Every query
// that feeds scanCard interpolates this rather than restating the list, so the
// column order is decided once, beside the code that depends on it.
const cardColumns = `scryfall_id, name, set_code, collector_number, set_name,
       released_at, lang, finishes, promo_types, frame_effects, frame, border_color,
       image_uri,
       colors, color_identity,
       price_usd, price_usd_foil, price_usd_etched, scryfall_url`

// scanCard reads one catalog row into the shared Card type.
//
// Building a scryfall.Card rather than a catalog-specific type is deliberate:
// every consumer — the price refresh, the finish repair, the add cascade —
// already speaks that type, so a local answer and a remote one are
// interchangeable and no caller has to know which it got.
func scanCard(r rowScanner) (scryfall.Card, error) {
	var c scryfall.Card
	var setName, released, lang, finishes, promos, frames, frame, border *string
	var image *string
	var colors, identity *string
	var usd, foil, etched *float64

	if err := r.Scan(&c.ID, &c.Name, &c.Set, &c.CollectorNumber, &setName,
		&released, &lang, &finishes, &promos, &frames, &frame, &border,
		&image,
		&colors, &identity,
		&usd, &foil, &etched, &c.ScryfallURL); err != nil {
		return scryfall.Card{}, fmt.Errorf("catalog: scanning a card: %w", err)
	}
	c.ImageURI = deref(image)

	c.SetName = deref(setName)
	c.ReleasedAt = deref(released)
	c.Lang = deref(lang)
	c.Frame = deref(frame)
	c.BorderColor = deref(border)
	c.Finishes = decodeArray(finishes)
	c.PromoTypes = decodeArray(promos)
	c.FrameEffects = decodeArray(frames)
	// decodeArray keeps NULL (nil, unknown) distinct from "[]" (empty,
	// colorless) — jsonArrayKeepEmpty wrote them apart for exactly this read.
	c.Colors = decodeArray(colors)
	c.ColorIdentity = decodeArray(identity)
	c.PriceUSD = usd
	// Both columns carry through, exactly as the API client fills them: the
	// foil column keeps FoilPrice's etched fallback so an etched-only printing
	// still reports a foil price, and the etched figure also stands on its own
	// so a card resolved locally and one resolved remotely price identically.
	c.PriceUSDFoil = scryfall.FoilPrice(foil, etched)
	c.PriceUSDEtched = etched
	return c, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func decodeArray(p *string) []string {
	if p == nil || *p == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(*p), &out); err != nil {
		return nil
	}
	return out
}
