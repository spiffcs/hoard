package catalog

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spiffcs/hoard/internal/scryfall"
)

func (c *Catalog) Cards(ids []string) (map[string]scryfall.Card, error) {
	out := make(map[string]scryfall.Card, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

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

		if err := c.read(func(db *sql.DB) error {
			rows, err := db.Query(q, args...)
			if err != nil {
				return fmt.Errorf("catalog: reading cards: %w", err)
			}
			defer rows.Close()
			for rows.Next() {
				card, err := scanCard(rows)
				if err != nil {
					return err
				}
				out[card.ID] = card
			}
			return rows.Err()
		}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

type rowScanner interface{ Scan(...any) error }

const cardColumns = `scryfall_id, name, set_code, collector_number, set_name,
       released_at, lang, finishes, promo_types, frame_effects, frame, border_color,
       colors, color_identity,
       price_usd, price_usd_foil, price_usd_etched, scryfall_url, image_uri`

func scanCard(r rowScanner) (scryfall.Card, error) {
	var c scryfall.Card
	var setName, released, lang, finishes, promos, frames, frame, border *string
	var colors, identity, imageURI *string
	var usd, foil, etched *float64

	if err := r.Scan(&c.ID, &c.Name, &c.Set, &c.CollectorNumber, &setName,
		&released, &lang, &finishes, &promos, &frames, &frame, &border,
		&colors, &identity,
		&usd, &foil, &etched, &c.ScryfallURL, &imageURI); err != nil {
		return scryfall.Card{}, fmt.Errorf("catalog: scanning a card: %w", err)
	}

	c.SetName = deref(setName)
	c.ReleasedAt = deref(released)
	c.Lang = deref(lang)
	c.Frame = deref(frame)
	c.BorderColor = deref(border)
	c.ImageURI = deref(imageURI)
	c.Finishes = decodeArray(finishes)
	c.PromoTypes = decodeArray(promos)
	c.FrameEffects = decodeArray(frames)

	c.Colors = decodeArray(colors)
	c.ColorIdentity = decodeArray(identity)
	c.PriceUSD = usd

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
