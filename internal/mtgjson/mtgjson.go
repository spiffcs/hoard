package mtgjson

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spiffcs/hoard/internal/boundedio"
	"github.com/spiffcs/hoard/internal/buildinfo"
)

var apiBase = "https://mtgjson.com/api/v5"

var providerOrder = []string{"tcgplayer", "cardkingdom", "manapool"}

var foilProviderOrder = []string{"tcgplayer", "manapool", "cardkingdom"}

const ListingOutlierRatio = 20

type Price struct {
	UUID       string
	USD        *float64
	Foil       *float64
	USDSource  string
	FoilSource string
}

var httpClient = &http.Client{Transport: headerBoundedTransport()}

var (
	requestGap = 250 * time.Millisecond
	paceMu     sync.Mutex
	lastStart  time.Time
	paceSleep  = time.Sleep
)

func pace() {
	paceMu.Lock()
	wait := requestGap - time.Since(lastStart)
	if wait > 0 {
		paceSleep(wait)
	}
	lastStart = time.Now()
	paceMu.Unlock()
}

func headerBoundedTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 30 * time.Second
	return t
}

var today = func() string { return time.Now().Format("2006-01-02") }

type Options struct {
	CacheDir string

	Progress func(done, total int64)

	BaseURL string
}

type progressWriter struct {
	w           io.Writer
	done, total int64
	fn          func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	p.fn(p.done, p.total)
	return n, err
}

var idleAfter = 60 * time.Second

type idleReader struct {
	r       io.ReadCloser
	timer   *time.Timer
	wait    time.Duration
	stalled atomic.Bool
}

func newIdleReader(r io.ReadCloser, wait time.Duration) *idleReader {
	ir := &idleReader{r: r, wait: wait}
	ir.timer = time.AfterFunc(wait, func() {
		ir.stalled.Store(true)
		r.Close()
	})
	return ir
}

func (ir *idleReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if err != nil && ir.stalled.Load() {
		return n, fmt.Errorf("mtgjson download stalled: no data for %s; check the connection and try again", ir.wait)
	}
	if n > 0 {
		ir.timer.Reset(ir.wait)
	}
	return n, err
}

func (ir *idleReader) Close() error {
	ir.timer.Stop()
	return ir.r.Close()
}

func fetch(ctx context.Context, o Options, name string) (io.ReadCloser, error) {
	var cachePath string
	if o.CacheDir != "" {
		cachePath = filepath.Join(o.CacheDir, today()+"-"+name)
		if f, err := os.Open(cachePath); err == nil {
			if gzipMagic(f) {
				return f, nil
			}

			f.Close()
			os.Remove(cachePath)
		}
	}

	base := apiBase
	if o.BaseURL != "" {
		base = o.BaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", buildinfo.UserAgent)
	req.Header.Set("Accept", "application/json")
	pace()
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNoSuchSet
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("mtgjson %s returned %d", name, resp.StatusCode)
	}
	body := newIdleReader(resp.Body, idleAfter)
	if cachePath == "" {
		return body, nil
	}

	if err := os.MkdirAll(o.CacheDir, 0o755); err != nil {
		return body, nil //nolint:nilerr // caching is best-effort
	}
	pruneCache(o.CacheDir)
	tmp, err := os.CreateTemp(o.CacheDir, "dl-*")
	if err != nil {
		return body, nil //nolint:nilerr // caching is best-effort
	}
	defer body.Close()
	dst := io.Writer(tmp)
	if o.Progress != nil {

		dst = &progressWriter{w: tmp, total: max(resp.ContentLength, 0), fn: o.Progress}
	}
	if _, err := io.Copy(dst, body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, err
	}
	tmp.Close()

	if bad, err := os.Open(tmp.Name()); err == nil {
		ok := gzipMagic(bad)
		bad.Close()
		if !ok {
			os.Remove(tmp.Name())
			return nil, fmt.Errorf("mtgjson %s: server sent a non-gzip response (an outage page?); try again shortly", name)
		}
	}
	if err := os.Rename(tmp.Name(), cachePath); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	return os.Open(cachePath)
}

func gzipMagic(f *os.File) bool {
	var hdr [2]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return false
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false
	}
	return hdr[0] == 0x1f && hdr[1] == 0x8b
}

func pruneCache(cacheDir string) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	prefix := today() + "-"
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			continue
		}

		if strings.HasPrefix(e.Name(), "dl-") || strings.HasPrefix(e.Name(), "quotes-") {
			continue
		}
		os.Remove(filepath.Join(cacheDir, e.Name()))
	}
}

type setCardJSON struct {
	UUID        string `json:"uuid"`
	Identifiers struct {
		ScryfallID string `json:"scryfallId"`

		TCGProductID string `json:"tcgplayerProductId"`
		TCGAltFoilID string `json:"tcgplayerAlternativeFoilProductId"`
		TCGEtchedID  string `json:"tcgplayerEtchedProductId"`

		CKID       string `json:"cardKingdomId"`
		CKFoilID   string `json:"cardKingdomFoilId"`
		CKEtchedID string `json:"cardKingdomEtchedId"`
	} `json:"identifiers"`
	PurchaseUrls struct {
		CardKingdom     string `json:"cardKingdom"`
		CardKingdomFoil string `json:"cardKingdomFoil"`
	} `json:"purchaseUrls"`
}

type setData struct {
	Cards []setCardJSON `json:"cards"`
}

type setFile struct {
	Data setData `json:"data"`
}

func addSetCards(out map[string]SetCard, cards []setCardJSON) {
	for _, c := range cards {
		if c.Identifiers.ScryfallID == "" || c.UUID == "" {
			continue
		}
		out[c.Identifiers.ScryfallID] = SetCard{
			UUID:            c.UUID,
			CKURL:           c.PurchaseUrls.CardKingdom,
			CKFoilURL:       c.PurchaseUrls.CardKingdomFoil,
			AltProductID:    c.Identifiers.TCGAltFoilID,
			EtchedProductID: c.Identifiers.TCGEtchedID,
			TCGProductID:    c.Identifiers.TCGProductID,
			CKFoilID:        c.Identifiers.CKFoilID,
			CKEtchedID:      c.Identifiers.CKEtchedID,
		}
	}
}

type SetCard struct {
	UUID      string
	CKURL     string
	CKFoilURL string

	AltProductID string

	EtchedProductID string

	TCGProductID string

	CKFoilID   string
	CKEtchedID string
}

var ErrNoSuchSet = errors.New("no such MTGJSON set")

func SetIdentifiers(ctx context.Context, o Options, setCode string) (map[string]SetCard, error) {
	body, err := fetch(ctx, o, strings.ToUpper(setCode)+".json.gz")
	if err != nil {
		if errors.Is(err, ErrNoSuchSet) {
			return nil, err
		}
		return nil, fmt.Errorf("fetching %s: %w", setCode, err)
	}
	defer body.Close()

	bc := &boundedio.Counter{R: body}
	zr, err := gzip.NewReader(bc)
	if err != nil {
		return nil, fmt.Errorf("decompressing %s: %w", setCode, err)
	}
	defer zr.Close()
	bounded := boundedio.LimitRatio(zr, bc, "the "+setCode+" set file")

	var sf setFile
	if err := json.NewDecoder(bounded).Decode(&sf); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", setCode, err)
	}

	out := make(map[string]SetCard, len(sf.Data.Cards))
	addSetCards(out, sf.Data.Cards)
	return out, nil
}

type byFinish struct {
	Normal map[string]float64 `json:"normal"`
	Foil   map[string]float64 `json:"foil"`
	Etched map[string]float64 `json:"etched"`
}

func (b byFinish) foilSlot() map[string]float64 {
	if len(b.Foil) > 0 {
		return b.Foil
	}
	return b.Etched
}

type vendor struct {
	Currency string   `json:"currency"`
	Retail   byFinish `json:"retail"`
	Buylist  byFinish `json:"buylist"`
}

type priceRecord struct {
	Paper map[string]vendor `json:"paper"`
}

type Quote struct {
	Provider string
	Kind     string
	Finish   finish.Finish
	Price    float64
}

const (
	Retail  = "retail"
	Buylist = "buylist"
	Ask     = "ask"
)

type ExtraPrices struct {
	Foil   map[string]float64
	Etched map[string]float64
}

type ExtraSeries map[string]ExtraPrices

func mergeExtra(rec *priceRecord, extra ExtraPrices) {
	if len(extra.Foil) == 0 && len(extra.Etched) == 0 {
		return
	}
	if rec.Paper == nil {
		rec.Paper = map[string]vendor{}
	}
	v, ok := rec.Paper["tcgplayer"]
	if !ok {
		v = vendor{Currency: "USD"}
	}
	if v.Currency != "USD" {
		return
	}
	fill := func(dst *map[string]float64, series map[string]float64) {
		if len(series) == 0 {
			return
		}
		if *dst == nil {
			*dst = map[string]float64{}
		}
		for date, price := range series {
			if _, taken := (*dst)[date]; !taken && price > 0 {
				(*dst)[date] = price
			}
		}
	}
	fill(&v.Retail.Foil, extra.Foil)
	fill(&v.Retail.Etched, extra.Etched)
	rec.Paper["tcgplayer"] = v
}

func TodayQuotes(ctx context.Context, o Options, want map[string]bool) (map[string][]Quote, error) {
	return TodayQuotesWith(nil)(ctx, o, want)
}

func TodayQuotesWith(extra ExtraSeries) func(context.Context, Options, map[string]bool) (map[string][]Quote, error) {
	return func(ctx context.Context, o Options, want map[string]bool) (map[string][]Quote, error) {
		out := map[string][]Quote{}
		err := streamPrices(ctx, o, todayFile, want, func(uuid string, rec priceRecord) {
			mergeExtra(&rec, extra[uuid])
			var qs []Quote
			for _, name := range providerOrder {
				v, ok := rec.Paper[name]
				if !ok || v.Currency != "USD" {
					continue
				}

				for _, k := range []struct {
					kind string
					side byFinish
				}{{Retail, v.Retail}, {Buylist, v.Buylist}} {
					for _, f := range []struct {
						finish finish.Finish
						byDate map[string]float64
					}{{finish.Nonfoil, k.side.Normal}, {finish.Foil, k.side.Foil}, {finish.Etched, k.side.Etched}} {
						if p := latest(f.byDate); p != nil {
							qs = append(qs, Quote{
								Provider: name, Kind: k.kind, Finish: f.finish, Price: *p,
							})
						}
					}
				}
			}
			if len(qs) > 0 {
				out[uuid] = qs
			}
		})
		if err != nil {
			return nil, err
		}
		return out, nil
	}
}

const (
	todayFile   = "AllPricesToday.json.gz"
	archiveFile = "AllPrices.json.gz"
)

func PrefetchPriceHistory(ctx context.Context, o Options) error {
	if o.CacheDir == "" {
		return nil
	}
	body, err := fetch(ctx, o, archiveFile)
	if err != nil {
		return err
	}
	return body.Close()
}

func streamPrices(ctx context.Context, o Options, file string, want map[string]bool, visit func(string, priceRecord)) error {
	if len(want) == 0 {
		return nil
	}
	body, err := fetch(ctx, o, file)
	if err != nil {
		return fmt.Errorf("fetching prices: %w", err)
	}
	defer body.Close()

	bc := &boundedio.Counter{R: body}
	zr, err := gzip.NewReader(bc)
	if err != nil {
		return fmt.Errorf("decompressing prices: %w", err)
	}
	defer zr.Close()
	bounded := boundedio.LimitRatio(zr, bc, "the price file")

	return scanKeyedObjects(bounded, want, func(uuid string, raw []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var rec priceRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return fmt.Errorf("decoding prices for %s: %w", uuid, err)
		}
		visit(uuid, rec)
		return nil
	})
}

func scanKeyedObjects(r io.Reader, want map[string]bool, visit func(string, []byte) error) error {

	if len(want) > setScanMin {
		if kl := uniformKeyLen(want); kl > 0 {
			return scanKeyedObjectsSet(r, want, kl, visit)
		}
	}
	type needle struct {
		key string
		pat []byte
	}
	needles := make([]needle, 0, len(want))
	maxPat := 0
	for k := range want {
		n := needle{key: k, pat: []byte(`"` + k + `":`)}
		needles = append(needles, n)
		maxPat = max(maxPat, len(n.pat))
	}

	const chunk = 4 << 20
	buf := make([]byte, 0, chunk*2)
	tmp := make([]byte, chunk)
	scanned := 0
	pending := -1
	for len(needles) > 0 {
		n, rerr := r.Read(tmp)
		buf = append(buf, tmp[:n]...)

		from := max(scanned-maxPat+1, 0)
		if pending >= 0 && pending < from {
			from = pending
		}
		pending = -1
		for i := 0; i < len(needles); {
			nd := needles[i]
			at := bytes.Index(buf[from:], nd.pat)
			if at < 0 {
				i++
				continue
			}
			start := from + at
			objStart := start + len(nd.pat)
			for objStart < len(buf) && (buf[objStart] == ' ' || buf[objStart] == '\n' || buf[objStart] == '\t') {
				objStart++
			}
			end := scanJSONValue(buf, objStart)
			if end < 0 {

				if pending < 0 || start < pending {
					pending = start
				}
				i++
				continue
			}
			if err := visit(nd.key, buf[objStart:end]); err != nil {
				return err
			}
			needles[i] = needles[len(needles)-1]
			needles = needles[:len(needles)-1]
		}
		scanned = len(buf)

		keep := len(buf) - (maxPat - 1)
		if pending >= 0 && pending < keep {
			keep = pending
		}
		if keep > 0 {
			buf = append(buf[:0], buf[keep:]...)
			scanned -= keep
			if pending >= 0 {
				pending -= keep
			}
		}

		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			return fmt.Errorf("reading prices: %w", rerr)
		}
	}
	return nil
}

var setScanMin = 24

func uniformKeyLen(want map[string]bool) int {
	kl := 0
	for k := range want {
		if kl == 0 {
			kl = len(k)
		} else if len(k) != kl {
			return 0
		}
	}
	return kl
}

func scanKeyedObjectsSet(r io.Reader, want map[string]bool, keyLen int, visit func(string, []byte) error) error {
	found := make(map[string]bool, len(want))
	remaining := len(want)

	pat := keyLen + 3

	const chunk = 4 << 20
	buf := make([]byte, 0, chunk*2)
	tmp := make([]byte, chunk)
	scanned := 0
	pending := -1
	for remaining > 0 {
		n, rerr := r.Read(tmp)
		buf = append(buf, tmp[:n]...)

		from := max(scanned-pat+1, 0)
		if pending >= 0 && pending < from {
			from = pending
		}
		pending = -1
		for i := from; i < len(buf); {
			rel := bytes.IndexByte(buf[i:], ':')
			if rel < 0 {
				break
			}
			j := i + rel
			i = j + 1

			ks := j - keyLen - 1
			if ks < 1 || buf[j-1] != '"' || buf[ks-1] != '"' {
				continue
			}
			key := buf[ks : j-1]
			if !want[string(key)] || found[string(key)] {
				continue
			}
			objStart := j + 1
			for objStart < len(buf) && (buf[objStart] == ' ' || buf[objStart] == '\n' || buf[objStart] == '\t') {
				objStart++
			}
			end := scanJSONValue(buf, objStart)
			if end < 0 {
				if pending < 0 {
					pending = ks - 1
				}
				break
			}
			if err := visit(string(key), buf[objStart:end]); err != nil {
				return err
			}
			found[string(key)] = true
			remaining--
			if remaining == 0 {
				return nil
			}
		}
		scanned = len(buf)

		keep := len(buf) - (pat - 1)
		if pending >= 0 && pending < keep {
			keep = pending
		}
		if keep > 0 {
			buf = append(buf[:0], buf[keep:]...)
			scanned -= keep
			if pending >= 0 {
				pending -= keep
			}
		}

		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			return fmt.Errorf("reading prices: %w", rerr)
		}
	}
	return nil
}

func scanJSONValue(buf []byte, i int) int {
	if i >= len(buf) {
		return -1
	}
	if buf[i] != '{' && buf[i] != '[' {

		return -1
	}
	depth, inStr, esc := 0, false, false
	for j := i; j < len(buf); j++ {
		c := buf[j]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return j + 1
			}
		}
	}
	return -1
}

func TodayPrices(ctx context.Context, o Options, want map[string]bool) (map[string]Price, error) {
	return TodayPricesWith(nil)(ctx, o, want)
}

func TodayPricesWith(extra ExtraSeries) func(context.Context, Options, map[string]bool) (map[string]Price, error) {
	return func(ctx context.Context, o Options, want map[string]bool) (map[string]Price, error) {
		out := make(map[string]Price, len(want))
		err := streamPrices(ctx, o, todayFile, want, func(uuid string, rec priceRecord) {
			mergeExtra(&rec, extra[uuid])
			if p, ok := bestUSD(uuid, rec); ok {
				out[uuid] = p
			}
		})
		if err != nil {
			return nil, err
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	}
}

type Observation struct {
	Date   string
	Finish finish.Finish
	Price  float64
	Source string
}

const bidProvider = "cardkingdom"

type CardHistory struct {
	Retail []Observation
	Bids   []Observation
}

func PriceHistory(ctx context.Context, o Options, want map[string]bool) (map[string]CardHistory, error) {
	return PriceHistoryWith(nil)(ctx, o, want)
}

func PriceHistoryWith(extra ExtraSeries) func(context.Context, Options, map[string]bool) (map[string]CardHistory, error) {
	return func(ctx context.Context, o Options, want map[string]bool) (map[string]CardHistory, error) {
		return priceHistory(ctx, o, want, extra)
	}
}

func priceHistory(ctx context.Context, o Options, want map[string]bool, extra ExtraSeries) (map[string]CardHistory, error) {
	out := make(map[string]CardHistory, len(want))
	side := func(v vendor, prices byFinish, source string) []Observation {
		if v.Currency != "USD" {
			return nil
		}
		var obs []Observation
		for fin, byDate := range map[finish.Finish]map[string]float64{
			finish.Nonfoil: prices.Normal, finish.Foil: prices.Foil,
		} {
			for date, price := range byDate {
				obs = append(obs, Observation{
					Date: date, Finish: fin, Price: price, Source: source,
				})
			}
		}
		return obs
	}

	retailFinish := func(rec priceRecord, fin finish.Finish) []Observation {
		order := providerOrder
		if fin == finish.Foil {
			order = foilProviderOrder
		}
		series := func(name string) map[string]float64 {
			v, ok := rec.Paper[name]
			if !ok || v.Currency != "USD" {
				return nil
			}
			if fin == finish.Foil {
				return v.Retail.foilSlot()
			}
			return v.Retail.Normal
		}

		type candidate struct {
			name   string
			latest float64
		}
		var cands []candidate
		for _, name := range order {
			if byDate := series(name); len(byDate) > 0 {
				cands = append(cands, candidate{name, *latest(byDate)})
			}
		}
		if len(cands) == 0 {
			return nil
		}
		latests := make([]float64, 0, len(cands))
		for _, c := range cands {
			latests = append(latests, c.latest)
		}
		pickName := cands[0].name
		for _, c := range cands {
			if NonPrice(c.latest, latests) {
				continue
			}
			pickName = c.name
			break
		}
		byDate := series(pickName)
		obs := make([]Observation, 0, len(byDate))
		for date, price := range byDate {
			obs = append(obs, Observation{Date: date, Finish: fin, Price: price, Source: pickName})
		}
		return obs
	}
	err := streamPrices(ctx, o, archiveFile, want, func(uuid string, rec priceRecord) {
		mergeExtra(&rec, extra[uuid])
		var h CardHistory
		h.Retail = append(retailFinish(rec, finish.Nonfoil), retailFinish(rec, finish.Foil)...)
		if v, ok := rec.Paper[bidProvider]; ok {
			h.Bids = side(v, v.Buylist, bidProvider)
		}
		if len(h.Retail) > 0 || len(h.Bids) > 0 {
			out[uuid] = h
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func bestUSD(uuid string, rec priceRecord) (Price, bool) {
	pick := func(order []string, retail func(vendor) map[string]float64) (*float64, string) {
		type quote struct {
			name string
			p    float64
		}
		var quotes []quote
		for _, name := range order {
			v, ok := rec.Paper[name]
			if !ok || v.Currency != "USD" {
				continue
			}
			if p := latest(retail(v)); p != nil {
				quotes = append(quotes, quote{name, *p})
			}
		}
		if len(quotes) == 0 {
			return nil, ""
		}
		prices := make([]float64, 0, len(quotes))
		for _, q := range quotes {
			prices = append(prices, q.p)
		}
		for _, q := range quotes {
			if NonPrice(q.p, prices) {
				continue
			}
			p := q.p
			return &p, q.name
		}
		p := quotes[0].p
		return &p, quotes[0].name
	}
	normal, normalSrc := pick(providerOrder, func(v vendor) map[string]float64 { return v.Retail.Normal })
	foil, foilSrc := pick(foilProviderOrder, func(v vendor) map[string]float64 { return v.Retail.foilSlot() })
	if normal == nil && foil == nil {
		return Price{}, false
	}
	return Price{
		UUID: uuid, USD: normal, Foil: foil,
		USDSource: normalSrc, FoilSource: foilSrc,
	}, true
}

func latest(byDate map[string]float64) *float64 {
	var newest string
	for d := range byDate {
		if d > newest {
			newest = d
		}
	}
	if newest == "" {
		return nil
	}
	v := byDate[newest]
	return &v
}

func NonPrice(price float64, figures []float64) bool {
	if price <= 0 || len(figures) < 2 {
		return false
	}
	if len(figures) == 2 {
		cheapest := min(figures[0], figures[1])
		return cheapest > 0 && price > cheapest*ListingOutlierRatio
	}
	mid := median(figures)
	if mid <= 0 {
		return false
	}
	return price/mid >= ListingOutlierRatio || mid/price >= ListingOutlierRatio
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	if n := len(s); n%2 == 1 {
		return s[n/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}
