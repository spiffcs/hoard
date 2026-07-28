// Package scryfall provides a minimal client for the Scryfall card API and a
// helper for turning a Scryfall card-page URL into the set code + collector
// number pair that the API is keyed on.
package scryfall

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
)

// userAgent identifies this tool to Scryfall. Scryfall requires a descriptive
// User-Agent header on every request.
const userAgent = "mtg-index/0.1"

// apiBase is the Scryfall REST API root.
const apiBase = "https://api.scryfall.com"

// Card is the subset of a Scryfall card object that this tool cares about.
type Card struct {
	ID              string
	Name            string
	Set             string
	CollectorNumber string
	ScryfallURL     string
	// PriceUSD and PriceUSDFoil are nil when Scryfall has no price for that
	// finish (e.g. a card that was never printed in foil).
	PriceUSD     *float64
	PriceUSDFoil *float64
}

// ParseCardURL extracts the set code and collector number from a Scryfall
// card-page URL such as:
//
//	https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre
//
// It returns the set code ("uma") and collector number ("7"). The trailing
// name slug is optional.
func ParseCardURL(raw string) (set, number string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Host != "scryfall.com" && u.Host != "www.scryfall.com" {
		return "", "", fmt.Errorf("not a scryfall.com URL: %q", raw)
	}
	// Path looks like /card/{set}/{number}[/{slug}].
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "card" {
		return "", "", fmt.Errorf("unexpected Scryfall URL path %q; expected /card/{set}/{number}/...", u.Path)
	}
	set, number = parts[1], parts[2]
	if set == "" || number == "" {
		return "", "", fmt.Errorf("could not extract set and collector number from %q", raw)
	}
	return set, number, nil
}

// apiCard mirrors the raw JSON returned by the Scryfall API. Prices arrive as
// strings (or null), so they are decoded as strings and converted afterward.
type apiCard struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Set             string `json:"set"`
	CollectorNumber string `json:"collector_number"`
	ScryfallURI     string `json:"scryfall_uri"`
	Prices          struct {
		USD     string `json:"usd"`
		USDFoil string `json:"usd_foil"`
	} `json:"prices"`
	// Populated on error responses.
	Object  string `json:"object"`
	Details string `json:"details"`
}

// FetchCard retrieves a single card from Scryfall by set code and collector
// number.
func FetchCard(ctx context.Context, set, number string) (*Card, error) {
	endpoint := fmt.Sprintf("%s/cards/%s/%s", apiBase, url.PathEscape(set), url.PathEscape(number))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting card %s/%s: %w", set, number, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response for %s/%s: %w", set, number, err)
	}

	var ac apiCard
	if err := json.Unmarshal(body, &ac); err != nil {
		return nil, fmt.Errorf("decoding response for %s/%s (status %d): %w", set, number, resp.StatusCode, err)
	}

	if resp.StatusCode != http.StatusOK {
		if ac.Details != "" {
			return nil, fmt.Errorf("scryfall returned %d for %s/%s: %s", resp.StatusCode, set, number, ac.Details)
		}
		return nil, fmt.Errorf("scryfall returned %d for %s/%s", resp.StatusCode, set, number)
	}

	return &Card{
		ID:              ac.ID,
		Name:            ac.Name,
		Set:             ac.Set,
		CollectorNumber: ac.CollectorNumber,
		ScryfallURL:     ac.ScryfallURI,
		PriceUSD:        parsePrice(ac.Prices.USD),
		PriceUSDFoil:    parsePrice(ac.Prices.USDFoil),
	}, nil
}

// parsePrice converts a Scryfall price string ("3.49", "", or absent) into a
// *float64, returning nil when there is no usable price.
func parsePrice(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}
