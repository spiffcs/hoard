// Package scryfall provides a minimal client for the Scryfall card API and a
// helper for turning a Scryfall card-page URL into the set code + collector
// number pair that the API is keyed on.
package scryfall

import (
	"bytes"
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
const userAgent = "hoard/0.1"

// apiBase is the Scryfall REST API root. It is a var (not const) so tests can
// point the client at a local httptest server.
var apiBase = "https://api.scryfall.com"

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

	// Display fields, populated by search results and used to disambiguate
	// printings interactively. They are ignored by the store.
	SetName      string
	ReleasedAt   string
	Finishes     []string // e.g. ["nonfoil","foil","etched"]
	PromoTypes   []string
	FrameEffects []string
	BorderColor  string
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
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Set             string   `json:"set"`
	SetName         string   `json:"set_name"`
	CollectorNumber string   `json:"collector_number"`
	ScryfallURI     string   `json:"scryfall_uri"`
	ReleasedAt      string   `json:"released_at"`
	Finishes        []string `json:"finishes"`
	PromoTypes      []string `json:"promo_types"`
	FrameEffects    []string `json:"frame_effects"`
	BorderColor     string   `json:"border_color"`
	Prices          struct {
		USD       string `json:"usd"`
		USDFoil   string `json:"usd_foil"`
		USDEtched string `json:"usd_etched"`
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

	card := ac.toCard()
	return &card, nil
}

// Identifier addresses a single card in a bulk collection request. Exactly one
// addressing scheme should be populated: ID (Scryfall UUID), Set+CollectorNumber,
// or Name. It marshals to the shapes accepted by POST /cards/collection.
type Identifier struct {
	ID              string `json:"id,omitempty"`
	Set             string `json:"set,omitempty"`
	CollectorNumber string `json:"collector_number,omitempty"`
	Name            string `json:"name,omitempty"`
}

// collectionMax is Scryfall's per-request cap for the collection endpoint.
const collectionMax = 75

// FetchCollection resolves many cards at once via POST /cards/collection,
// automatically splitting identifiers into chunks of 75. It returns the found
// cards and the identifiers Scryfall could not match (echoed back verbatim).
func FetchCollection(ctx context.Context, ids []Identifier) (found []Card, notFound []Identifier, err error) {
	client := &http.Client{Timeout: 30 * time.Second}
	for i := 0; i < len(ids); i += collectionMax {
		if i > 0 {
			// Stay well under Scryfall's rate limit between chunks.
			time.Sleep(100 * time.Millisecond)
		}
		end := min(i+collectionMax, len(ids))
		chunkFound, chunkNotFound, err := fetchCollectionChunk(ctx, client, ids[i:end])
		if err != nil {
			return nil, nil, err
		}
		found = append(found, chunkFound...)
		notFound = append(notFound, chunkNotFound...)
	}
	return found, notFound, nil
}

func fetchCollectionChunk(ctx context.Context, client *http.Client, ids []Identifier) ([]Card, []Identifier, error) {
	reqBody, err := json.Marshal(struct {
		Identifiers []Identifier `json:"identifiers"`
	}{ids})
	if err != nil {
		return nil, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/cards/collection", bytes.NewReader(reqBody))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("requesting card collection: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("reading collection response: %w", err)
	}

	var out struct {
		Data     []apiCard    `json:"data"`
		NotFound []Identifier `json:"not_found"`
		Details  string       `json:"details"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, nil, fmt.Errorf("decoding collection response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Details != "" {
			return nil, nil, fmt.Errorf("scryfall collection returned %d: %s", resp.StatusCode, out.Details)
		}
		return nil, nil, fmt.Errorf("scryfall collection returned %d", resp.StatusCode)
	}

	cards := make([]Card, 0, len(out.Data))
	for _, ac := range out.Data {
		cards = append(cards, ac.toCard())
	}
	return cards, out.NotFound, nil
}

// toCard converts a decoded Scryfall JSON card into the exported Card type.
func (ac apiCard) toCard() Card {
	// The catalog has a single "foil" price column; when a card has no foil
	// price but does have an etched price (etched-only printings), use that so
	// etched entries can still be valued.
	foil := parsePrice(ac.Prices.USDFoil)
	if foil == nil {
		foil = parsePrice(ac.Prices.USDEtched)
	}
	return Card{
		ID:              ac.ID,
		Name:            ac.Name,
		Set:             ac.Set,
		SetName:         ac.SetName,
		CollectorNumber: ac.CollectorNumber,
		ScryfallURL:     ac.ScryfallURI,
		ReleasedAt:      ac.ReleasedAt,
		Finishes:        ac.Finishes,
		PromoTypes:      ac.PromoTypes,
		FrameEffects:    ac.FrameEffects,
		BorderColor:     ac.BorderColor,
		PriceUSD:        parsePrice(ac.Prices.USD),
		PriceUSDFoil:    foil,
	}
}

// Autocomplete returns candidate card names for a partial query, using
// GET /cards/autocomplete. Returns an empty slice when nothing matches.
func Autocomplete(ctx context.Context, q string) ([]string, error) {
	endpoint := apiBase + "/cards/autocomplete?q=" + url.QueryEscape(q)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("autocomplete %q: %w", q, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scryfall autocomplete returned %d", resp.StatusCode)
	}
	var out struct {
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decoding autocomplete response: %w", err)
	}
	return out.Data, nil
}

// NamedFuzzy resolves a possibly-imperfect name (e.g. from OCR) to a single
// canonical card via GET /cards/named?fuzzy=. It returns (nil, nil) when Scryfall
// can't confidently match (404), so callers can fall back to manual entry.
func NamedFuzzy(ctx context.Context, text string) (*Card, error) {
	endpoint := apiBase + "/cards/named?fuzzy=" + url.QueryEscape(text)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fuzzy-naming %q: %w", text, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// 404 means no confident match (or the query was too ambiguous).
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	var ac apiCard
	if err := json.Unmarshal(body, &ac); err != nil {
		return nil, fmt.Errorf("decoding fuzzy-name response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		if ac.Details != "" {
			return nil, fmt.Errorf("scryfall fuzzy-name returned %d: %s", resp.StatusCode, ac.Details)
		}
		return nil, fmt.Errorf("scryfall fuzzy-name returned %d", resp.StatusCode)
	}
	card := ac.toCard()
	return &card, nil
}

// SearchPrints returns every paper printing of the card with the given exact
// name, newest first, following pagination. An empty result (Scryfall 404 "no
// cards found") returns (nil, nil) rather than an error.
func SearchPrints(ctx context.Context, exactName string) ([]Card, error) {
	// q = !"Exact Name" game:paper  → exact name match, paper printings only.
	query := fmt.Sprintf(`!"%s" game:paper`, exactName)
	endpoint := apiBase + "/cards/search?unique=prints&order=released&q=" + url.QueryEscape(query)

	client := &http.Client{Timeout: 30 * time.Second}
	var cards []Card
	for endpoint != "" {
		page, next, err := searchPage(ctx, client, endpoint)
		if err != nil {
			return nil, err
		}
		if page == nil { // 404 no-match on the first page
			return nil, nil
		}
		cards = append(cards, page...)
		endpoint = next
		if next != "" {
			time.Sleep(100 * time.Millisecond) // rate-limit courtesy between pages
		}
	}
	return cards, nil
}

// searchPage fetches one page of a /cards/search result. It returns the page's
// cards, the next-page URL ("" when done), or (nil, "", nil) on a 404 no-match.
func searchPage(ctx context.Context, client *http.Client, endpoint string) ([]Card, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("searching prints: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, "", nil // no cards matched
	}
	var out struct {
		Data     []apiCard `json:"data"`
		HasMore  bool      `json:"has_more"`
		NextPage string    `json:"next_page"`
		Details  string    `json:"details"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, "", fmt.Errorf("decoding search response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Details != "" {
			return nil, "", fmt.Errorf("scryfall search returned %d: %s", resp.StatusCode, out.Details)
		}
		return nil, "", fmt.Errorf("scryfall search returned %d", resp.StatusCode)
	}

	cards := make([]Card, 0, len(out.Data))
	for _, ac := range out.Data {
		cards = append(cards, ac.toCard())
	}
	next := ""
	if out.HasMore {
		next = out.NextPage
	}
	return cards, next, nil
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
