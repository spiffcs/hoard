package decksource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cphillips918/hoard/internal/scryfall"
)

const archidektUserAgent = "hoard/0.1"

// archidektProvider imports decks from archidekt.com via its public JSON API.
type archidektProvider struct{}

func (archidektProvider) Matches(u *url.URL) bool {
	h := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	return h == "archidekt.com"
}

// archidektDeck mirrors the subset of the Archidekt deck payload we use.
type archidektDeck struct {
	Name   string `json:"name"`
	Format int    `json:"deckFormat"`
	Cards  []struct {
		Quantity   int      `json:"quantity"`
		Modifier   string   `json:"modifier"` // Normal|Foil|Etched
		Categories []string `json:"categories"`
		Card       struct {
			UID             string `json:"uid"` // Scryfall ID
			CollectorNumber string `json:"collectorNumber"`
			Edition         struct {
				EditionCode string `json:"editioncode"`
			} `json:"edition"`
			OracleCard struct {
				Name string `json:"name"`
			} `json:"oracleCard"`
		} `json:"card"`
	} `json:"cards"`
}

func (p archidektProvider) Fetch(ctx context.Context, u *url.URL) (*Deck, error) {
	id, err := parseArchidektID(u)
	if err != nil {
		return nil, err
	}

	endpoint := "https://archidekt.com/api/decks/" + id + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", archidektUserAgent)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching Archidekt deck %s: %w", id, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("archidekt returned %d for deck %s", resp.StatusCode, id)
	}

	var ad archidektDeck
	if err := json.Unmarshal(body, &ad); err != nil {
		return nil, fmt.Errorf("decoding Archidekt deck %s: %w", id, err)
	}
	return archidektToDeck(id, u.String(), &ad), nil
}

// parseArchidektID extracts the numeric deck id from /decks/{id}/{slug}.
func parseArchidektID(u *url.URL) (string, error) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "decks" {
		return "", fmt.Errorf("unexpected Archidekt URL path %q; expected /decks/{id}/...", u.Path)
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		return "", fmt.Errorf("Archidekt deck id %q is not numeric", parts[1])
	}
	return parts[1], nil
}

func archidektToDeck(id, sourceURL string, ad *archidektDeck) *Deck {
	d := &Deck{
		Name:      ad.Name,
		Source:    "archidekt",
		SourceID:  id,
		SourceURL: sourceURL,
		Format:    archidektFormat(ad.Format),
	}
	for _, c := range ad.Cards {
		ident := scryfall.Identifier{ID: c.Card.UID}
		if ident.ID == "" {
			// Fall back to set + collector number, then name.
			if c.Card.Edition.EditionCode != "" && c.Card.CollectorNumber != "" {
				ident = scryfall.Identifier{Set: c.Card.Edition.EditionCode, CollectorNumber: c.Card.CollectorNumber}
			} else {
				ident = scryfall.Identifier{Name: c.Card.OracleCard.Name}
			}
		}
		d.Entries = append(d.Entries, Entry{
			Ident:    ident,
			Quantity: c.Quantity,
			Finish:   normalizeFinish(c.Modifier),
			Board:    boardFromCategories(c.Categories),
		})
	}
	return d
}

// boardFromCategories maps Archidekt's built-in categories to a board.
func boardFromCategories(categories []string) string {
	for _, cat := range categories {
		switch strings.ToLower(cat) {
		case "commander":
			return BoardCommander
		case "sideboard":
			return BoardSide
		case "maybeboard":
			return BoardMaybe
		}
	}
	return BoardMain
}

func normalizeFinish(modifier string) string {
	switch strings.ToLower(modifier) {
	case "foil":
		return "foil"
	case "etched":
		return "etched"
	default:
		return "normal"
	}
}

// archidektFormat maps Archidekt's numeric format codes to readable names.
func archidektFormat(code int) string {
	names := map[int]string{
		1: "Standard", 2: "Modern", 3: "Commander/EDH", 4: "Legacy",
		5: "Vintage", 6: "Pauper", 7: "Custom", 8: "Frontier",
		9: "Future Standard", 10: "Penny Dreadful", 11: "1v1 Commander",
		12: "Duel Commander", 13: "Brawl", 14: "Oathbreaker", 15: "Pioneer",
		16: "Historic", 17: "Pauper EDH", 18: "Alchemy", 19: "Explorer",
		20: "Historic Brawl", 21: "Gladiator", 22: "Premodern", 23: "Predh",
	}
	if n, ok := names[code]; ok {
		return n
	}
	if code == 0 {
		return ""
	}
	return "Format " + strconv.Itoa(code)
}
