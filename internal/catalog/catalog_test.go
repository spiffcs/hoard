package catalog

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
)

func card(id, name, set, num string, usd string, games ...string) string {
	if len(games) == 0 {
		games = []string{"paper"}
	}
	b, _ := json.Marshal(map[string]any{
		"id": id, "name": name, "set": set, "collector_number": num,
		"set_name": "Test Set", "released_at": "2024-01-01", "rarity": "rare",
		"finishes": []finish.Finish{finish.Nonfoil, finish.Foil}, "border_color": "black",
		"scryfall_uri": "https://scryfall.com/card/" + set + "/" + num,
		"games":        games,
		"prices":       map[string]any{"usd": usd, "usd_foil": "9.99", "usd_etched": nil},
	})
	return string(b)
}

func serveBundle(t *testing.T, updatedAt string, lines []string) *httptest.Server {
	t.Helper()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	for _, l := range lines {
		zw.Write([]byte(l + "\n"))
	}
	zw.Close()
	body := gz.Bytes()

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bundle" {
			w.Write(body)
			return
		}
		fmt.Fprintf(w, `{"data":[
		  {"type":"oracle_cards","updated_at":%q,"jsonl_download_uri":%q,"compressed_size":1},
		  {"type":"default_cards","updated_at":%q,"jsonl_download_uri":%q,"compressed_size":%d}]}`,
			updatedAt, srv.URL+"/bundle", updatedAt, srv.URL+"/bundle", len(body))
	}))
	t.Cleanup(srv.Close)

	old := listingURL
	listingURL = srv.URL + "/bulk-data"
	t.Cleanup(func() { listingURL = old })
	return srv
}

func openTemp(t *testing.T) *Catalog {
	t.Helper()
	c, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestOpenCreatesAnEmptyCatalog(t *testing.T) {
	c := openTemp(t)
	if c.CardCount() != 0 {
		t.Errorf("Cards = %d, want 0 for a fresh catalog", c.CardCount())
	}
	if !c.built().IsZero() {
		t.Errorf("Built = %v, want zero", c.built())
	}
	if _, err := os.Stat(c.Path()); err != nil {
		t.Errorf("no file at %s: %v", c.Path(), err)
	}
}

func TestBuildStoresPaperCardsAndNames(t *testing.T) {
	serveBundle(t, "2026-07-30T00:00:00Z", []string{
		card("a", "Sol Ring", "c21", "1", "2.00"),
		card("b", "Sol Ring", "mps", "1", "120.00"),
		card("c", "Bitterblossom", "uma", "85", "34.11"),

		card("d", "Alchemy Card", "ymid", "1", "0.01", "arena"),
	})
	c := openTemp(t)

	if err := c.Update(context.Background(), nil); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := c.CardCount(); got != 3 {
		t.Errorf("Cards = %d, want the 3 paper printings", got)
	}

	var name, finishes string
	var usd float64
	err := c.db.QueryRow(
		`SELECT name, price_usd, finishes FROM cards WHERE scryfall_id = 'b'`).
		Scan(&name, &usd, &finishes)
	if err != nil {
		t.Fatalf("reading a stored card: %v", err)
	}
	if name != "Sol Ring" || usd != 120.00 {
		t.Errorf("stored %q at %v, want Sol Ring at 120", name, usd)
	}

	if finishes != `["nonfoil","foil"]` {
		t.Errorf("finishes = %q", finishes)
	}

	var names int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM names`).Scan(&names); err != nil {
		t.Fatal(err)
	}
	if names != 2 {
		t.Errorf("names = %d, want 2 distinct", names)
	}
	var tris int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM name_trigrams`).Scan(&tris); err != nil {
		t.Fatal(err)
	}
	if tris == 0 {
		t.Error("no trigrams indexed; fuzzy matching would find nothing")
	}

	var n int
	c.db.QueryRow(`SELECT COUNT(*) FROM names WHERE name = 'Alchemy Card'`).Scan(&n)
	if n != 0 {
		t.Error("a digital-only card reached the name index")
	}
}

func TestBuildStoresColorIdentity(t *testing.T) {
	withColors := func(line string, identity []string) string {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad fixture: %v", err)
		}
		m["colors"] = identity
		m["color_identity"] = identity
		b, _ := json.Marshal(m)
		return string(b)
	}
	serveBundle(t, "2026-07-30T00:00:00Z", []string{
		withColors(card("az", "Absorb", "rna", "151", "0.50"), []string{"W", "U"}),
		withColors(card("sol", "Sol Ring", "c21", "1", "2.00"), []string{}),
		card("old", "Fixture Without Colors", "tst", "9", "1.00"),
	})
	c := openTemp(t)
	if err := c.Update(context.Background(), nil); err != nil {
		t.Fatalf("Update: %v", err)
	}

	cards, err := c.Cards([]string{"az", "sol", "old"})
	if err != nil {
		t.Fatalf("Cards: %v", err)
	}
	if got := cards["az"].ColorIdentity; !slices.Equal(got, []string{"W", "U"}) {
		t.Errorf("Absorb identity = %v, want [W U]", got)
	}
	if got := cards["sol"].ColorIdentity; got == nil || len(got) != 0 {
		t.Errorf("Sol Ring identity = %#v, want empty but known (colorless)", got)
	}
	if got := cards["old"].ColorIdentity; got != nil {
		t.Errorf("colorless-field-absent identity = %#v, want nil (unknown)", got)
	}
}

func TestBuildRecordsProvenance(t *testing.T) {
	serveBundle(t, "2026-07-30T12:00:00Z", []string{card("a", "Sol Ring", "c21", "1", "2.00")})
	c := openTemp(t)
	if err := c.Update(context.Background(), nil); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := c.sourceUpdated().Format(time.RFC3339); got != "2026-07-30T12:00:00Z" {
		t.Errorf("SourceUpdated = %s, want the bundle's own timestamp", got)
	}
	if c.built().IsZero() {
		t.Error("Built not recorded")
	}
	if c.Bytes() == 0 {
		t.Error("Bytes = 0")
	}
}

func TestRebuildReplacesRatherThanMerges(t *testing.T) {
	dir := t.TempDir()
	srv := serveBundle(t, "2026-07-29T00:00:00Z", []string{
		card("a", "Sol Ring", "c21", "1", "2.00"),
		card("gone", "Removed Card", "xxx", "1", "1.00"),
	})
	_ = srv
	c, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Update(context.Background(), nil); err != nil {
		t.Fatalf("first build: %v", err)
	}
	c.Close()

	serveBundle(t, "2026-07-30T00:00:00Z", []string{card("a", "Sol Ring", "c21", "1", "2.50")})
	c, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Update(context.Background(), nil); err != nil {
		t.Fatalf("second build: %v", err)
	}

	if got := c.CardCount(); got != 1 {
		t.Errorf("Cards = %d, want 1 after the rebuild", got)
	}
	var n int
	c.db.QueryRow(`SELECT COUNT(*) FROM cards WHERE scryfall_id = 'gone'`).Scan(&n)
	if n != 0 {
		t.Error("a card dropped from the bundle survived the rebuild")
	}
	var usd float64
	c.db.QueryRow(`SELECT price_usd FROM cards WHERE scryfall_id = 'a'`).Scan(&usd)
	if usd != 2.50 {
		t.Errorf("price = %v, want the new bundle's 2.50", usd)
	}
}

func TestFailedBuildLeavesThePreviousCatalog(t *testing.T) {
	dir := t.TempDir()
	serveBundle(t, "2026-07-29T00:00:00Z", []string{card("a", "Sol Ring", "c21", "1", "2.00")})
	c, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Update(context.Background(), nil); err != nil {
		t.Fatalf("first build: %v", err)
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bundle" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"data":[{"type":"default_cards","updated_at":"2026-07-31T00:00:00Z",
		  "jsonl_download_uri":%q,"compressed_size":1}]}`, srv.URL+"/bundle")
	}))
	defer srv.Close()
	old := listingURL
	listingURL = srv.URL + "/bulk-data"
	defer func() { listingURL = old }()

	if err := c.Update(context.Background(), nil); err == nil {
		t.Fatal("a failed download reported success")
	}
	if got := c.CardCount(); got != 1 {
		t.Errorf("Cards = %d, want the previous catalog intact", got)
	}
	if got := c.sourceUpdated().Format(time.RFC3339); got != "2026-07-29T00:00:00Z" {
		t.Errorf("SourceUpdated = %s, want the old build's", got)
	}

	if _, err := os.Stat(filepath.Join(dir, fileName+".building")); !os.IsNotExist(err) {
		t.Error("the partial build was left on disk")
	}
}

func TestStatusReportsStaleness(t *testing.T) {
	serveBundle(t, "2026-07-29T00:00:00Z", []string{card("a", "Sol Ring", "c21", "1", "2.00")})
	c := openTemp(t)
	if err := c.Update(context.Background(), nil); err != nil {
		t.Fatalf("Update: %v", err)
	}

	s := c.CheckStatus(context.Background())
	if s.Checked || s.Stale {
		t.Errorf("status right after a build = %+v, want no check and not stale", s)
	}
	if s.Cards != 1 || s.Empty() {
		t.Errorf("status = %+v, want 1 card", s)
	}

	if err := c.setMeta(keyChecked, "2020-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	serveBundle(t, "2026-07-30T00:00:00Z", []string{card("a", "Sol Ring", "c21", "1", "2.00")})
	s = c.CheckStatus(context.Background())
	if !s.Checked || !s.Stale {
		t.Errorf("status = %+v, want a check reporting stale", s)
	}
	if s.Remote.Format(time.RFC3339) != "2026-07-30T00:00:00Z" {
		t.Errorf("Remote = %v", s.Remote)
	}
}

func TestStatusHonoursTheCheckInterval(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		fmt.Fprint(w, `{"data":[{"type":"default_cards","updated_at":"2026-07-30T00:00:00Z",
		  "jsonl_download_uri":"http://example.invalid/x","compressed_size":1}]}`)
	}))
	defer srv.Close()
	old := listingURL
	listingURL = srv.URL
	defer func() { listingURL = old }()

	c := openTemp(t)
	for range 5 {
		c.CheckStatus(context.Background())
	}
	if hits != 1 {
		t.Errorf("made %d listing requests across 5 status calls, want 1", hits)
	}
}

func TestStatusIsSilentWhenOffline(t *testing.T) {
	old := listingURL
	listingURL = "http://127.0.0.1:1/bulk-data"
	defer func() { listingURL = old }()

	c := openTemp(t)
	s := c.CheckStatus(context.Background())
	if s.Checked || s.Stale {
		t.Errorf("status = %+v, want no claim about the remote", s)
	}
	if !s.Empty() {
		t.Errorf("status = %+v, want it to report an empty catalog", s)
	}
}

func TestOpenRebuildsOnSchemaMismatch(t *testing.T) {
	dir := t.TempDir()
	serveBundle(t, "2026-07-29T00:00:00Z", []string{card("a", "Sol Ring", "c21", "1", "2.00")})
	c, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Update(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := c.setMeta(keySchema, "999"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	c, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer c.Close()
	if got := c.CardCount(); got != 0 {
		t.Errorf("Cards = %d, want an empty catalog after the version changed", got)
	}
	if v, _ := c.metaInt(keySchema); v != schemaVersion {
		t.Errorf("schema_version = %d, want %d", v, schemaVersion)
	}
}

func TestOpenReplacesGarbage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Open(dir)
	if err != nil {
		t.Fatalf("Open over garbage: %v", err)
	}
	defer c.Close()
	if c.CardCount() != 0 {
		t.Errorf("Cards = %d", c.CardCount())
	}
}

func TestBuildCancellation(t *testing.T) {
	lines := make([]string, 0, 5000)
	for i := range 5000 {
		lines = append(lines, card(fmt.Sprint(i), fmt.Sprintf("Card %d", i), "tst", fmt.Sprint(i), "1.00"))
	}
	serveBundle(t, "2026-07-30T00:00:00Z", lines)
	c := openTemp(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Update(ctx, nil); err == nil {
		t.Fatal("a cancelled build reported success")
	}
	if c.CardCount() != 0 {
		t.Errorf("Cards = %d after a cancelled build", c.CardCount())
	}
}

func TestTrimJSONLine(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{`{"a":1}`, `{"a":1}`},
		{`[{"a":1},`, `{"a":1}`},
		{`  {"a":1}  `, `{"a":1}`},
		{`{"a":1}]`, `{"a":1}`},
		{`]`, ``},
		{``, ``},
	} {
		if got := string(trimJSONLine([]byte(tt.in))); got != tt.want {
			t.Errorf("trimJSONLine(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
