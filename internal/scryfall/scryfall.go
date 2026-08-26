package scryfall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spiffcs/hoard/internal/buildinfo"
)

var apiBase = "https://api.scryfall.com"

type Card struct {
	ID              string
	Name            string
	Set             string
	CollectorNumber string
	ScryfallURL     string

	PriceUSD       *float64
	PriceUSDFoil   *float64
	PriceUSDEtched *float64

	SetName    string
	ReleasedAt string

	Lang         string
	Finishes     []string
	PromoTypes   []string
	FrameEffects []string

	Frame       string
	BorderColor string

	Colors        []string
	ColorIdentity []string

	Raw json.RawMessage
}

var cardLangs = map[string]bool{
	"en": true, "es": true, "fr": true, "de": true, "it": true, "pt": true,
	"ja": true, "ko": true, "ru": true, "zhs": true, "zht": true, "he": true,
	"la": true, "grc": true, "ar": true, "sa": true, "ph": true, "qya": true,
}

func ParseCardURL(raw string) (set, number, lang string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", "", fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Host != "scryfall.com" && u.Host != "www.scryfall.com" {
		return "", "", "", fmt.Errorf("not a scryfall.com URL: %q", raw)
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "card" {
		return "", "", "", fmt.Errorf("unexpected Scryfall URL path %q; expected a /card/{set}/{number}/ prefix", u.Path)
	}
	set, number = parts[1], parts[2]
	if set == "" || number == "" {
		return "", "", "", fmt.Errorf("could not extract set and collector number from %q", raw)
	}
	if len(parts) > 3 {
		if code := strings.ToLower(parts[3]); cardLangs[code] {
			lang = code
		}
	}
	return set, number, lang, nil
}

type apiCard struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Set             string   `json:"set"`
	SetName         string   `json:"set_name"`
	CollectorNumber string   `json:"collector_number"`
	ScryfallURI     string   `json:"scryfall_uri"`
	ReleasedAt      string   `json:"released_at"`
	Lang            string   `json:"lang"`
	Finishes        []string `json:"finishes"`
	PromoTypes      []string `json:"promo_types"`
	FrameEffects    []string `json:"frame_effects"`
	Frame           string   `json:"frame"`
	BorderColor     string   `json:"border_color"`
	Colors          []string `json:"colors"`
	ColorIdentity   []string `json:"color_identity"`
	Prices          struct {
		USD       string `json:"usd"`
		USDFoil   string `json:"usd_foil"`
		USDEtched string `json:"usd_etched"`
	} `json:"prices"`

	Object  string `json:"object"`
	Details string `json:"details"`
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

var (
	slowGap    = 500 * time.Millisecond
	defaultGap = 100 * time.Millisecond
)

var slowEndpoints = []string{"/cards/search", "/cards/named", "/cards/random", "/cards/collection"}

func endpointClass(endpoint string) (string, time.Duration) {
	path := endpoint
	if u, err := url.Parse(endpoint); err == nil {
		path = u.Path
	}
	for _, s := range slowEndpoints {
		if strings.HasPrefix(path, s) {
			return s, slowGap
		}
	}
	return "", defaultGap
}

type pacer struct {
	mu   sync.Mutex
	next map[string]time.Time
	all  time.Time
}

var apiPacer = pacer{next: map[string]time.Time{}}

func (p *pacer) wait(ctx context.Context, endpoint string) error {
	class, gap := endpointClass(endpoint)
	p.mu.Lock()
	now := time.Now()
	at := now
	if c := p.next[class]; c.After(at) {
		at = c
	}
	if p.all.After(at) {
		at = p.all
	}
	p.next[class] = at.Add(gap)
	p.all = at.Add(defaultGap)
	p.mu.Unlock()
	if d := at.Sub(now); d > 0 {
		return sleepCtx(ctx, d)
	}
	return nil
}

func Pace(ctx context.Context, endpoint string) error {
	return apiPacer.wait(ctx, endpoint)
}

type apiResponse struct {
	status int
	header http.Header
	body   []byte
}

func apiDo(ctx context.Context, method, endpoint string, reqBody []byte, what string) (apiResponse, error) {
	if err := apiPacer.wait(ctx, endpoint); err != nil {
		return apiResponse{}, err
	}
	var r io.Reader
	if reqBody != nil {
		r = bytes.NewReader(reqBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, r)
	if err != nil {
		return apiResponse{}, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent)
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return apiResponse{}, fmt.Errorf("requesting %s: %w", what, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return apiResponse{}, fmt.Errorf("reading %s response: %w", what, err)
	}
	return apiResponse{status: resp.StatusCode, header: resp.Header, body: body}, nil
}

func statusErr(r apiResponse, what string) error {
	var e struct {
		Details string `json:"details"`
	}
	_ = json.Unmarshal(r.body, &e)
	if e.Details != "" {
		return fmt.Errorf("scryfall %s returned %d: %s", what, r.status, e.Details)
	}
	return fmt.Errorf("scryfall %s returned %d", what, r.status)
}

func FetchCard(ctx context.Context, set, number string) (*Card, error) {
	return FetchCardLang(ctx, set, number, "")
}

func FetchCardLang(ctx context.Context, set, number, lang string) (*Card, error) {
	endpoint := fmt.Sprintf("%s/cards/%s/%s", apiBase, url.PathEscape(set), url.PathEscape(number))
	what := fmt.Sprintf("card %s/%s", set, number)
	if lang != "" && !strings.EqualFold(lang, "en") {
		endpoint += "/" + url.PathEscape(strings.ToLower(lang))
		what += " in " + strings.ToLower(lang)
	}
	r, err := apiDo(ctx, http.MethodGet, endpoint, nil, what)
	if err != nil {
		return nil, err
	}
	if r.status != http.StatusOK {
		return nil, statusErr(r, what)
	}
	var ac apiCard
	if err := json.Unmarshal(r.body, &ac); err != nil {
		return nil, fmt.Errorf("decoding response for %s/%s: %w", set, number, err)
	}
	card := ac.toCard(r.body)
	return &card, nil
}

type Identifier struct {
	ID              string `json:"id,omitempty"`
	Set             string `json:"set,omitempty"`
	CollectorNumber string `json:"collector_number,omitempty"`
	Name            string `json:"name,omitempty"`
}

const collectionMax = 75

func FetchCollection(ctx context.Context, ids []Identifier) (found []Card, notFound []Identifier, err error) {
	return FetchCollectionProgress(ctx, ids, nil)
}

func FetchCollectionProgress(ctx context.Context, ids []Identifier,
	onChunk func(done, total int, note string)) (found []Card, notFound []Identifier, err error) {

	for i := 0; i < len(ids); i += collectionMax {
		end := min(i+collectionMax, len(ids))
		var onWait func(string)
		if onChunk != nil {
			onWait = func(note string) { onChunk(i, len(ids), note) }
		}
		chunkFound, chunkNotFound, err := fetchCollectionChunkRetrying(ctx, ids[i:end], onWait)
		if err != nil {
			return nil, nil, err
		}
		found = append(found, chunkFound...)
		notFound = append(notFound, chunkNotFound...)
		if onChunk != nil {
			onChunk(end, len(ids), "")
		}
	}
	return found, notFound, nil
}

const rateLimitRetries = 3

var (
	transientPause = 2 * time.Second
	rateLimitBase  = 5 * time.Second
)

type errRateLimited struct {
	retryAfter time.Duration
	instructed bool
}

func (e errRateLimited) Error() string {
	return fmt.Sprintf("scryfall rate-limited the request (retry after %s)", e.retryAfter)
}

type errTransient struct{ err error }

func (e errTransient) Error() string { return e.err.Error() }
func (e errTransient) Unwrap() error { return e.err }

func fetchCollectionChunkRetrying(ctx context.Context, ids []Identifier,
	onWait func(note string)) ([]Card, []Identifier, error) {
	var lastErr error
	for attempt := 0; attempt <= rateLimitRetries; attempt++ {
		cards, missing, err := fetchCollectionChunk(ctx, ids)
		var limited errRateLimited
		var transient errTransient
		var wait time.Duration
		var reason string
		switch {
		case errors.As(err, &limited):
			wait = limited.retryAfter
			if !limited.instructed {
				wait <<= attempt
			}
			reason = "rate limited"
		case errors.As(err, &transient):
			wait = transientPause * time.Duration(attempt+1)
			reason = "scryfall hiccup"
		default:
			return cards, missing, err
		}
		lastErr = err
		if attempt == rateLimitRetries {
			break
		}
		if onWait != nil {
			onWait(fmt.Sprintf("%s; retrying in %s", reason, wait.Round(time.Second)))
		}
		if err := sleepCtx(ctx, wait); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, lastErr
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func retryAfter(h http.Header, fallback time.Duration) (time.Duration, bool) {
	const maxWait = 90 * time.Second
	if v := h.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
			return min(time.Duration(secs)*time.Second, maxWait), true
		}
	}
	return fallback, false
}

func fetchCollectionChunk(ctx context.Context, ids []Identifier) ([]Card, []Identifier, error) {
	reqBody, err := json.Marshal(struct {
		Identifiers []Identifier `json:"identifiers"`
	}{ids})
	if err != nil {
		return nil, nil, err
	}
	r, err := apiDo(ctx, http.MethodPost, apiBase+"/cards/collection", reqBody, "card collection")
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, err
		}
		return nil, nil, errTransient{err}
	}

	if r.status == http.StatusTooManyRequests {
		wait, instructed := retryAfter(r.header, rateLimitBase)
		return nil, nil, errRateLimited{retryAfter: wait, instructed: instructed}
	}
	if r.status >= http.StatusInternalServerError {
		return nil, nil, errTransient{statusErr(r, "collection")}
	}
	if r.status != http.StatusOK {
		return nil, nil, statusErr(r, "collection")
	}

	var out struct {
		Data     []json.RawMessage `json:"data"`
		NotFound []Identifier      `json:"not_found"`
	}
	if err := json.Unmarshal(r.body, &out); err != nil {
		return nil, nil, fmt.Errorf("decoding collection response: %w", err)
	}
	cards, err := decodeCards(out.Data)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding collection response: %w", err)
	}
	return cards, out.NotFound, nil
}

func decodeCards(raws []json.RawMessage) ([]Card, error) {
	cards := make([]Card, 0, len(raws))
	for _, raw := range raws {
		var ac apiCard
		if err := json.Unmarshal(raw, &ac); err != nil {
			return nil, err
		}
		cards = append(cards, ac.toCard(raw))
	}
	return cards, nil
}

func (ac apiCard) toCard(raw json.RawMessage) Card {
	etched := parsePrice(ac.Prices.USDEtched)
	foil := FoilPrice(parsePrice(ac.Prices.USDFoil), etched)
	return Card{
		ID:              ac.ID,
		Name:            ac.Name,
		Set:             ac.Set,
		SetName:         ac.SetName,
		CollectorNumber: ac.CollectorNumber,
		ScryfallURL:     ac.ScryfallURI,
		ReleasedAt:      ac.ReleasedAt,
		Lang:            ac.Lang,
		Finishes:        ac.Finishes,
		PromoTypes:      ac.PromoTypes,
		FrameEffects:    ac.FrameEffects,
		Frame:           ac.Frame,
		BorderColor:     ac.BorderColor,
		Colors:          ac.Colors,
		ColorIdentity:   ac.ColorIdentity,
		PriceUSD:        parsePrice(ac.Prices.USD),
		PriceUSDFoil:    foil,
		PriceUSDEtched:  etched,
		Raw:             raw,
	}
}

func Autocomplete(ctx context.Context, q string) ([]string, error) {
	endpoint := apiBase + "/cards/autocomplete?q=" + url.QueryEscape(q)
	r, err := apiDo(ctx, http.MethodGet, endpoint, nil, "autocomplete")
	if err != nil {
		return nil, err
	}
	if r.status != http.StatusOK {
		return nil, statusErr(r, "autocomplete")
	}
	var out struct {
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(r.body, &out); err != nil {
		return nil, fmt.Errorf("decoding autocomplete response: %w", err)
	}
	return out.Data, nil
}

func NamedFuzzy(ctx context.Context, text string) (*Card, error) {
	endpoint := apiBase + "/cards/named?fuzzy=" + url.QueryEscape(text)
	r, err := apiDo(ctx, http.MethodGet, endpoint, nil, "fuzzy-name")
	if err != nil {
		return nil, err
	}

	if r.status == http.StatusNotFound {
		return nil, nil
	}
	if r.status != http.StatusOK {
		return nil, statusErr(r, "fuzzy-name")
	}
	var ac apiCard
	if err := json.Unmarshal(r.body, &ac); err != nil {
		return nil, fmt.Errorf("decoding fuzzy-name response: %w", err)
	}
	card := ac.toCard(r.body)
	return &card, nil
}

func SearchPrints(ctx context.Context, exactName string) ([]Card, error) {

	query := fmt.Sprintf(`!"%s" game:paper`, exactName)
	endpoint := apiBase + "/cards/search?unique=prints&order=released&q=" + url.QueryEscape(query)

	const searchMaxPages = 20

	var cards []Card
	for page := 0; endpoint != "" && page < searchMaxPages; page++ {
		found, next, err := searchPage(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		if found == nil {
			return nil, nil
		}
		cards = append(cards, found...)

		endpoint = next
	}
	return cards, nil
}

func searchPage(ctx context.Context, endpoint string) ([]Card, string, error) {
	r, err := apiDo(ctx, http.MethodGet, endpoint, nil, "search")
	if err != nil {
		return nil, "", err
	}
	if r.status == http.StatusNotFound {
		return nil, "", nil
	}
	if r.status != http.StatusOK {
		return nil, "", statusErr(r, "search")
	}
	var out struct {
		Data     []json.RawMessage `json:"data"`
		HasMore  bool              `json:"has_more"`
		NextPage string            `json:"next_page"`
	}
	if err := json.Unmarshal(r.body, &out); err != nil {
		return nil, "", fmt.Errorf("decoding search response: %w", err)
	}
	cards, err := decodeCards(out.Data)
	if err != nil {
		return nil, "", fmt.Errorf("decoding search response: %w", err)
	}
	next := ""
	if out.HasMore {
		next = out.NextPage
	}
	return cards, next, nil
}

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

func (id Identifier) Key() string {
	switch {
	case id.ID != "":
		return id.ID
	case id.Set != "" && id.CollectorNumber != "":
		return strings.ToLower(id.Set) + "/" + id.CollectorNumber
	default:
		return strings.ToLower(id.Name)
	}
}

func (id Identifier) Label() string {
	switch {
	case id.Name != "":
		return id.Name
	case id.Set != "" && id.CollectorNumber != "":
		return id.Set + "/" + id.CollectorNumber
	default:
		return id.ID
	}
}
