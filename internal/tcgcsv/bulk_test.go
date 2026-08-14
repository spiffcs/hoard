package tcgcsv

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

type bulkServer struct {
	mu             sync.Mutex
	groupGets      int
	archiveHeads   int
	archiveGets    int
	archiveMissing bool

	groupBytes int
	opts       Options
}

func newBulkServer(t *testing.T, groupBytes int) *bulkServer {
	t.Helper()
	b := &bulkServer{groupBytes: groupBytes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/archive/"):
			if b.archiveMissing {
				w.WriteHeader(http.StatusNotFound)
				return
			}

			body := strings.Repeat("z", archiveFixtureBytes)
			if r.Method == http.MethodHead {
				b.archiveHeads++
				w.Header().Set("Content-Length", fmt.Sprint(len(body)))
				return
			}
			b.archiveGets++
			w.Write([]byte(body))
		case strings.HasSuffix(r.URL.Path, "/prices"):
			b.groupGets++
			gid := strings.Split(r.URL.Path, "/")[3]
			w.Write([]byte(priceFixture(gid, b.groupBytes)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	b.opts = Options{BaseURL: srv.URL, CacheDir: t.TempDir()}
	return b
}

const archiveFixtureBytes = 4 << 20

func priceFixture(gid string, n int) string {
	var sb strings.Builder
	sb.WriteString(`{"results": [{"productId": ` + gid + `, "marketPrice": 12.5, "lowPrice": 1.0, "subTypeName": "Normal"}`)
	for i := 900000; sb.Len() < n; i++ {
		fmt.Fprintf(&sb, `,{"productId": %d, "marketPrice": 1.5, "lowPrice": 0.5, "subTypeName": "Foil"}`, i)
	}
	sb.WriteString(`]}`)
	return sb.String()
}

func stubArchive(t *testing.T, date string, gids ...int) {
	t.Helper()
	prev := readArchiveMembers
	readArchiveMembers = func(_ string, want map[string]bool) (map[string][]byte, error) {
		out := map[string][]byte{}
		for _, gid := range gids {
			member := fmt.Sprintf("%s/%d/%d/prices", date, magicCategory, gid)
			if want[member] {
				out[member] = []byte(priceFixture(fmt.Sprint(gid), 0))
			}
		}
		return out, nil
	}
	t.Cleanup(func() { readArchiveMembers = prev })
}

func pinDate(t *testing.T, date string) {
	t.Helper()
	prev := today
	today = func() string { return date }
	t.Cleanup(func() { today = prev })
}

func ids(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = 1000 + i
	}
	return out
}

func TestBulkKeepsPerGroupForASmallHoard(t *testing.T) {
	pinDate(t, "2026-08-12")
	b := newBulkServer(t, 82<<10)

	got, err := GroupQuotesBulk(context.Background(), b.opts, ids(8))
	if err != nil {
		t.Fatalf("GroupQuotesBulk: %v", err)
	}
	if len(got) != 8 {
		t.Fatalf("read %d groups, want 8", len(got))
	}
	if b.groupGets != 8 {
		t.Errorf("%d per-group requests, want 8", b.groupGets)
	}
	if b.archiveGets != 0 || b.archiveHeads != 0 {
		t.Errorf("a small hoard touched the archive: %d HEAD, %d GET", b.archiveHeads, b.archiveGets)
	}
}

func TestBulkTakesTheArchiveForALargeHoard(t *testing.T) {
	pinDate(t, "2026-08-12")
	b := newBulkServer(t, 82<<10)
	want := ids(90)
	stubArchive(t, "2026-08-12", want...)

	got, err := GroupQuotesBulk(context.Background(), b.opts, want)
	if err != nil {
		t.Fatalf("GroupQuotesBulk: %v", err)
	}
	if len(got) != 90 {
		t.Fatalf("read %d groups, want 90", len(got))
	}
	if b.archiveGets != 1 {
		t.Errorf("%d archive downloads, want exactly 1", b.archiveGets)
	}
	if b.groupGets != 0 {
		t.Errorf("%d per-group requests after taking the archive, want 0", b.groupGets)
	}

	before := b.archiveGets + b.groupGets
	if _, err := GroupQuotesBulk(context.Background(), b.opts, want); err != nil {
		t.Fatalf("second read: %v", err)
	}
	if after := b.archiveGets + b.groupGets; after != before {
		t.Errorf("a fully cached day cost %d more requests, want 0", after-before)
	}
}

func TestBulkSizesTheFetchNotTheCollection(t *testing.T) {
	pinDate(t, "2026-08-12")
	b := newBulkServer(t, 82<<10)
	want := ids(90)
	stubArchive(t, "2026-08-12", want...)

	if _, err := GroupQuotesBulk(context.Background(), b.opts, want[:87]); err != nil {
		t.Fatalf("priming: %v", err)
	}
	b.mu.Lock()
	b.archiveGets, b.archiveHeads, b.groupGets = 0, 0, 0
	b.mu.Unlock()

	got, err := GroupQuotesBulk(context.Background(), b.opts, want)
	if err != nil {
		t.Fatalf("GroupQuotesBulk: %v", err)
	}
	if len(got) != 90 {
		t.Fatalf("read %d groups, want 90", len(got))
	}
	if b.archiveGets != 0 || b.archiveHeads != 0 {
		t.Errorf("three missing groups pulled the archive again: %d HEAD, %d GET",
			b.archiveHeads, b.archiveGets)
	}
	if b.groupGets != 3 {
		t.Errorf("%d per-group requests, want 3 — only what was missing", b.groupGets)
	}
}

func TestBulkFallsBackWhenTheArchiveIsMissing(t *testing.T) {
	pinDate(t, "2026-08-12")
	b := newBulkServer(t, 82<<10)
	b.archiveMissing = true

	var noted []string
	b.opts.Note = func(msg string) { noted = append(noted, msg) }

	got, err := GroupQuotesBulk(context.Background(), b.opts, ids(90))
	if err != nil {
		t.Fatalf("GroupQuotesBulk: %v", err)
	}
	if len(got) != 90 {
		t.Errorf("read %d groups, want all 90 by the other route", len(got))
	}
	if b.groupGets != 90 {
		t.Errorf("%d per-group requests, want 90", b.groupGets)
	}

	_ = noted
}

func TestBulkWithoutACacheKeepsPerGroup(t *testing.T) {
	pinDate(t, "2026-08-12")
	b := newBulkServer(t, 82<<10)
	b.opts.CacheDir = ""

	got, err := GroupQuotesBulk(context.Background(), b.opts, ids(90))
	if err != nil {
		t.Fatalf("GroupQuotesBulk: %v", err)
	}
	if len(got) != 90 {
		t.Errorf("read %d groups, want 90", len(got))
	}
	if b.archiveGets != 0 || b.archiveHeads != 0 {
		t.Errorf("the archive was fetched with nowhere to keep it")
	}
}

func TestBulkLeavesUnreadableGroupsAbsent(t *testing.T) {
	pinDate(t, "2026-08-12")
	b := newBulkServer(t, 1<<10)

	got, err := GroupQuotesBulk(context.Background(), b.opts, []int{1000, 1001})
	if err != nil {
		t.Fatalf("GroupQuotesBulk: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("read %d groups, want 2", len(got))
	}

	os.Remove(dayCachePath(b.opts, 1001))
	b.archiveMissing = true
	srvDown := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srvDown.Close()
	down := b.opts
	down.BaseURL = srvDown.URL

	got, err = GroupQuotesBulk(context.Background(), down, []int{1000, 1001})
	if err != nil {
		t.Fatalf("GroupQuotesBulk: %v", err)
	}
	if _, ok := got[1000]; !ok {
		t.Error("the cached group went missing")
	}
	if _, ok := got[1001]; ok {
		t.Error("an unreadable group came back present; it must be absent, not empty")
	}
}
