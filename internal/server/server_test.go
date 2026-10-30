package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/index-pages/getdr/internal/buildinfo"
	"github.com/index-pages/getdr/internal/cache"
	"github.com/index-pages/getdr/internal/lookup"
)

type slowFetcher struct {
	calls   atomic.Int32
	targets atomic.Int32
	delay   time.Duration
	keyErr  bool
}

func (f *slowFetcher) Fetch(ctx context.Context, targets []string, version string, fresh bool) map[string]lookup.Outcome {
	f.calls.Add(1)
	f.targets.Add(int32(len(targets)))
	time.Sleep(f.delay)
	out := map[string]lookup.Outcome{}
	for _, t := range targets {
		if f.keyErr {
			out[t] = lookup.Outcome{NoResponse: true, Err: fmt.Errorf("%w rejected by Ahrefs (HTTP 401: Unauthorized)", lookup.ErrKey)}
			continue
		}
		if strings.HasPrefix(t, "nodata") {
			out[t] = lookup.Outcome{}
			continue
		}
		v := float64(len(t))
		out[t] = lookup.Outcome{DR: &v}
	}
	return out
}

func newServer(t *testing.T, f lookup.Fetcher, token string) (*httptest.Server, *Server) {
	db, err := cache.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := &Server{
		Engine:         &lookup.Engine{Cache: db, Fetcher: f, TTL: time.Hour, TTLNone: time.Hour},
		DefaultVersion: "dr3", HasKey: true, Token: token, MaxTargets: 5, Cache: db,
		Requests: func() int { return 7 },
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s
}

func do(t *testing.T, method, url, token, body string) (int, http.Header, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func TestEndpoints(t *testing.T) {
	ts, _ := newServer(t, &slowFetcher{}, "")
	code, h, body := do(t, "GET", ts.URL+"/healthz", "", "")
	if code != 200 || body != "ok\n" || h.Get("Server") != buildinfo.ServerHeader() {
		t.Fatalf("healthz %d %q %q", code, body, h.Get("Server"))
	}
	code, _, body = do(t, "GET", ts.URL+"/teapot", "", "")
	if code != http.StatusTeapot {
		t.Fatalf("teapot %d", code)
	}
	code, _, body = do(t, "GET", ts.URL+"/", "", "")
	var info map[string]string
	json.Unmarshal([]byte(body), &info)
	if code != 200 || info["name"] != "getdr" || info["version"] != buildinfo.Version || info["homepage"] != "https://index-pages.com/getdr/" || info["docs"] == "" {
		t.Fatalf("root %d %s", code, body)
	}
	code, _, _ = do(t, "GET", ts.URL+"/nope", "", "")
	if code != 404 {
		t.Fatalf("404 %d", code)
	}
	code, h, _ = do(t, "DELETE", ts.URL+"/dr", "", "")
	if code != 405 || h.Get("Allow") != "GET, POST" {
		t.Fatalf("405 %d %q", code, h.Get("Allow"))
	}

	code, _, body = do(t, "GET", ts.URL+"/dr?target=https://www.Example.com/x", "", "")
	var rec map[string]any
	json.Unmarshal([]byte(body), &rec)
	if code != 200 || rec["target"] != "example.com" || rec["dr"] != 11.0 || rec["version"] != "dr3" || rec["cached"] != false || rec["checked_at"] == nil {
		t.Fatalf("GET /dr %d %s", code, body)
	}
	code, _, body = do(t, "GET", ts.URL+"/dr?target=example.com&version=dr3", "", "")
	if code != 200 || !strings.Contains(body, `"cached":true`) {
		t.Fatalf("second GET must be cached: %s", body)
	}
	code, _, body = do(t, "GET", ts.URL+"/dr?target=example.com&fresh=1", "", "")
	if !strings.Contains(body, `"cached":false`) {
		t.Fatalf("fresh GET: %s", body)
	}
	code, _, body = do(t, "GET", ts.URL+"/dr?target=https://Example.com/x&raw=1", "", "")
	if !strings.Contains(body, `"target":"https://Example.com/x"`) {
		t.Fatalf("raw GET: %s", body)
	}

	code, _, body = do(t, "POST", ts.URL+"/dr", "", `{"targets":["a.com","www.a.com","nodata.com"],"version":"dr4"}`)
	var res struct {
		Results []map[string]any `json:"results"`
	}
	json.Unmarshal([]byte(body), &res)
	if code != 200 || len(res.Results) != 2 || res.Results[0]["target"] != "a.com" || res.Results[0]["version"] != "dr4" {
		t.Fatalf("POST %d %s", code, body)
	}
	if res.Results[1]["dr"] != nil || res.Results[1]["checked_at"] == nil {
		t.Fatalf("no data must have dr null and checked_at set: %s", body)
	}

	for _, c := range []struct {
		method, path, body string
		code               int
		msg                string
	}{
		{"GET", "/dr", "", 400, "missing target"},
		{"GET", "/dr?target=a.com&version=dr9", "", 400, "invalid version"},
		{"GET", "/dr?target=a.com&fresh=maybe", "", 400, "fresh"},
		{"POST", "/dr", `{"targets":[]}`, 400, "no valid targets"},
		{"POST", "/dr", `{"targets":["a.com"],"ttl":"1s"}`, 400, "unknown field"},
		{"POST", "/dr", `not json`, 400, "invalid JSON"},
		{"POST", "/dr", `{"targets":["a.com"]} junk`, 400, "after the JSON object"},
		{"POST", "/dr", `{"targets":["a.com"]}{"targets":["b.com"]}`, 400, "after the JSON object"},
		{"POST", "/dr", `{"targets":["a.com","b.com","c.com","d.com","e.com","f.com"]}`, 413, "at most 5"},
	} {
		code, _, body := do(t, c.method, ts.URL+c.path, "", c.body)
		if code != c.code || !strings.Contains(body, c.msg) || !strings.HasPrefix(body, `{"error":`) {
			t.Errorf("%s %s %s: %d %s", c.method, c.path, c.body, code, body)
		}
	}

	code, _, body = do(t, "GET", ts.URL+"/stats", "", "")
	var st struct {
		Cache   *cache.Stats `json:"cache"`
		Lookups struct {
			Hits    int64   `json:"hits"`
			Misses  int64   `json:"misses"`
			HitRate float64 `json:"hit_rate"`
		} `json:"lookups"`
		Requests int `json:"ahrefs_requests_last_hour"`
	}
	if err := json.Unmarshal([]byte(body), &st); err != nil || code != 200 {
		t.Fatalf("stats %d %s", code, body)
	}
	if strings.Contains(body, `"path"`) {
		t.Fatalf("stats must not expose the cache path: %s", body)
	}
	if st.Cache == nil || st.Cache.Rows == 0 || st.Cache.SizeBytes == 0 || st.Lookups.Hits != 1 || st.Lookups.Misses != 5 || st.Requests != 7 {
		t.Fatalf("stats %s", body)
	}
}

func TestAuth(t *testing.T) {
	ts, _ := newServer(t, &slowFetcher{}, "s3cret")
	for _, tok := range []string{"", "wrong", "s3cret2"} {
		code, h, body := do(t, "GET", ts.URL+"/dr?target=a.com", tok, "")
		if code != 401 || h.Get("WWW-Authenticate") == "" || !strings.Contains(body, "token") {
			t.Errorf("token %q: %d %s", tok, code, body)
		}
	}
	if code, _, _ := do(t, "GET", ts.URL+"/stats", "", ""); code != 401 {
		t.Errorf("stats without token: %d", code)
	}
	if code, _, _ := do(t, "GET", ts.URL+"/dr?target=a.com", "s3cret", ""); code != 200 {
		t.Errorf("good token: %d", code)
	}
	if code, _, _ := do(t, "POST", ts.URL+"/dr", "s3cret", `{"targets":["a.com"]}`); code != 200 {
		t.Errorf("good token POST: %d", code)
	}
	for _, p := range []string{"/healthz", "/", "/teapot"} {
		if code, _, _ := do(t, "GET", ts.URL+p, "", ""); code == 401 {
			t.Errorf("%s must not need a token", p)
		}
	}
}

func TestNoKey(t *testing.T) {
	f := &slowFetcher{}
	ts, s := newServer(t, f, "")
	s.HasKey = false
	code, _, body := do(t, "GET", ts.URL+"/dr?target=a.com", "", "")
	if code != 503 || !strings.Contains(body, "no Ahrefs API key") || !strings.Contains(body, "https://index-pages.com/getdr/#key") {
		t.Fatalf("%d %s", code, body)
	}
	if f.calls.Load() != 0 {
		t.Fatal("must not fetch without key")
	}
	if code, _, _ := do(t, "GET", ts.URL+"/healthz", "", ""); code != 200 {
		t.Fatal("healthz must work without key")
	}
}

func TestRejectedKey(t *testing.T) {
	ts, _ := newServer(t, &slowFetcher{keyErr: true}, "")
	code, _, body := do(t, "POST", ts.URL+"/dr", "", `{"targets":["a.com"]}`)
	if code != 503 || !strings.Contains(body, "rejected") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestDedup(t *testing.T) {
	f := &slowFetcher{delay: 200 * time.Millisecond}
	ts, _ := newServer(t, f, "")
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var code int
			var body string
			if i%2 == 0 {
				code, _, body = do(t, "GET", ts.URL+"/dr?target=same.com", "", "")
			} else {
				code, _, body = do(t, "POST", ts.URL+"/dr", "", `{"targets":["same.com"]}`)
			}
			if code != 200 || !strings.Contains(body, `"dr":8`) {
				errs <- errors.New(body)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if n := f.targets.Load(); n != 1 {
		t.Fatalf("same.com fetched %d times, want 1", n)
	}
}

func TestHeadDoesNoLookup(t *testing.T) {
	f := &slowFetcher{}
	ts, _ := newServer(t, f, "")
	code, h, _ := do(t, "HEAD", ts.URL+"/dr?target=a.com", "", "")
	if code != http.StatusMethodNotAllowed || h.Get("Allow") != "GET, POST" || f.calls.Load() != 0 {
		t.Fatalf("HEAD /dr: %d allow %q fetches %d", code, h.Get("Allow"), f.calls.Load())
	}
	if code, _, _ := do(t, "HEAD", ts.URL+"/healthz", "", ""); code != 200 {
		t.Fatalf("HEAD /healthz: %d", code)
	}
}

func TestInvalidTargetsServe(t *testing.T) {
	f := &slowFetcher{}
	ts, _ := newServer(t, f, "")
	code, _, body := do(t, "POST", ts.URL+"/dr", "", `{"targets":["exa mple.com","https://","a.com"]}`)
	want := `{"results":[{"target":"exa mple.com","dr":null,"version":"dr3","checked_at":null,"cached":false,"error":"invalid target"},` +
		`{"target":"https://","dr":null,"version":"dr3","checked_at":null,"cached":false,"error":"invalid target"},{"target":"a.com","dr":5,`
	if code != 200 || !strings.HasPrefix(body, want) {
		t.Fatalf("%d %s", code, body)
	}
	if f.targets.Load() != 1 {
		t.Fatalf("invalid targets must not be fetched: %d", f.targets.Load())
	}
}
