package ahrefs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/index-pages/getdr/internal/buildinfo"
	"github.com/index-pages/getdr/internal/lookup"
)

type mock struct {
	t        *testing.T
	calls    atomic.Int32
	mu       sync.Mutex
	sizes    []int
	versions []string
	// fail returns a status to send instead of data for the n-th call (1-based), 0 = ok.
	fail func(n int) int
}

func (m *mock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := int(m.calls.Add(1))
	if r.Method != http.MethodPost || r.URL.Path != "/v3/public/domain-rating-free" || r.URL.Query().Get("output") != "json" {
		m.t.Errorf("unexpected request %s %s", r.Method, r.URL)
	}
	if ua := r.Header.Get("User-Agent"); ua != buildinfo.UserAgent() {
		m.t.Errorf("User-Agent %q", ua)
	}
	if r.Header.Get("Authorization") != "Bearer good" {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`["Error","Unauthorized"]`))
		return
	}
	var body struct {
		Targets []string `json:"targets"`
		Version string   `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		m.t.Errorf("decode: %v", err)
	}
	m.mu.Lock()
	m.sizes = append(m.sizes, len(body.Targets))
	m.versions = append(m.versions, body.Version)
	m.mu.Unlock()
	if m.fail != nil {
		if st := m.fail(n); st != 0 {
			if st == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "2")
			}
			w.WriteHeader(st)
			w.Write([]byte(`{"error":"slow down"}`))
			return
		}
	}
	var items []string
	for _, t := range body.Targets {
		switch {
		case strings.HasPrefix(t, "missing"):
			continue
		case strings.HasPrefix(t, "null"):
			items = append(items, fmt.Sprintf(`{"target":%q,"domain_rating":null}`, t))
		case strings.HasPrefix(t, "err"):
			items = append(items, fmt.Sprintf(`{"target":%q,"error":"invalid target"}`, t))
		case strings.HasPrefix(t, "zero"):
			items = append(items, fmt.Sprintf(`{"target":%q,"domain_rating":0.0}`, t))
		default:
			items = append(items, fmt.Sprintf(`{"target":%q,"domain_rating":%d.5}`, t, len(t)))
		}
	}
	fmt.Fprintf(w, `{"domain_rating":{"license":"https://ahrefs.com/legal/domain-rating-license","targets":[%s]}}`, strings.Join(items, ","))
}

func newClient(t *testing.T, m *mock) (*Client, *[]time.Duration) {
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	var mu sync.Mutex
	slept := &[]time.Duration{}
	c := &Client{
		Endpoint: srv.URL + "/v3/public/domain-rating-free",
		Key:      "good", Batch: 1000, RPM: 0, Retries: 3, Timeout: 5 * time.Second,
		Sleep: func(ctx context.Context, d time.Duration) error {
			mu.Lock()
			*slept = append(*slept, d)
			mu.Unlock()
			return ctx.Err()
		},
	}
	return c, slept
}

func TestBatching(t *testing.T) {
	m := &mock{t: t}
	c, _ := newClient(t, m)
	c.Batch = 1000
	var targets []string
	for i := 0; i < 2500; i++ {
		targets = append(targets, fmt.Sprintf("d%d.com", i))
	}
	var progress []int
	c.Progress = func(b, total int) { progress = append(progress, b*10+total) }
	out := c.Fetch(context.Background(), targets, "dr4", false)
	if len(out) != 2500 {
		t.Fatalf("got %d outcomes", len(out))
	}
	if fmt.Sprint(m.sizes) != "[1000 1000 500]" || fmt.Sprint(m.versions) != "[dr4 dr4 dr4]" {
		t.Fatalf("batches %v versions %v", m.sizes, m.versions)
	}
	if fmt.Sprint(progress) != "[13 23 33]" {
		t.Fatalf("progress %v", progress)
	}
	o := out["d7.com"]
	if o.NoResponse || o.DR == nil || *o.DR != 6.5 {
		t.Fatalf("d7.com %+v", o)
	}
}

func TestNoDataShapes(t *testing.T) {
	m := &mock{t: t}
	c, _ := newClient(t, m)
	out := c.Fetch(context.Background(), []string{"a.com", "null.com", "err.com", "missing.com", "zero.com"}, "dr3", false)
	for _, k := range []string{"null.com", "err.com", "missing.com"} {
		if o := out[k]; o.NoResponse || o.DR != nil {
			t.Errorf("%s must be no data, got %+v", k, o)
		}
	}
	if o := out["zero.com"]; o.DR == nil || *o.DR != 0 {
		t.Errorf("zero.com must be 0, got %+v", o)
	}
	if o := out["a.com"]; o.DR == nil || *o.DR != 5.5 {
		t.Errorf("a.com %+v", o)
	}
}

func TestRetriesThenSuccess(t *testing.T) {
	m := &mock{t: t, fail: func(n int) int {
		switch n {
		case 1:
			return http.StatusTooManyRequests
		case 2:
			return http.StatusBadGateway
		}
		return 0
	}}
	c, slept := newClient(t, m)
	out := c.Fetch(context.Background(), []string{"a.com"}, "dr3", false)
	if o := out["a.com"]; o.NoResponse || o.DR == nil {
		t.Fatalf("a.com %+v", o)
	}
	if m.calls.Load() != 3 {
		t.Fatalf("calls %d", m.calls.Load())
	}
	// First wait honours Retry-After: 2s (> 1s backoff), second is 2s backoff.
	if fmt.Sprint(*slept) != "[2s 2s]" {
		t.Fatalf("slept %v", *slept)
	}
}

func TestRetriesExhaustedOnlyFailBatch(t *testing.T) {
	m := &mock{t: t, fail: func(n int) int {
		if n <= 4 {
			return http.StatusTooManyRequests
		}
		return 0
	}}
	c, slept := newClient(t, m)
	c.Batch = 2
	out := c.Fetch(context.Background(), []string{"a.com", "b.com", "c.com"}, "dr3", false)
	for _, k := range []string{"a.com", "b.com"} {
		o := out[k]
		if !o.NoResponse || o.Err == nil || !strings.Contains(o.Err.Error(), "429") || !strings.Contains(o.Err.Error(), "after 3 retries") {
			t.Errorf("%s: %+v", k, o)
		}
	}
	if o := out["c.com"]; o.NoResponse || o.DR == nil {
		t.Errorf("second batch must succeed: %+v", o)
	}
	if len(*slept) != 3 {
		t.Errorf("slept %v", *slept)
	}
}

func TestBackoffDoubles(t *testing.T) {
	m := &mock{t: t, fail: func(int) int { return http.StatusInternalServerError }}
	c, slept := newClient(t, m)
	c.Retries = 4
	out := c.Fetch(context.Background(), []string{"a.com"}, "dr3", false)
	if !out["a.com"].NoResponse {
		t.Fatal("must fail")
	}
	if fmt.Sprint(*slept) != "[1s 2s 4s 8s]" {
		t.Fatalf("slept %v", *slept)
	}
}

func TestBadRequestNotRetried(t *testing.T) {
	m := &mock{t: t, fail: func(int) int { return http.StatusBadRequest }}
	c, _ := newClient(t, m)
	out := c.Fetch(context.Background(), []string{"a.com"}, "dr3", false)
	if !out["a.com"].NoResponse || m.calls.Load() != 1 {
		t.Fatalf("%+v calls %d", out["a.com"], m.calls.Load())
	}
}

func TestKeyErrors(t *testing.T) {
	m := &mock{t: t}
	c, _ := newClient(t, m)
	c.Key = "bad"
	c.Batch = 1
	out := c.Fetch(context.Background(), []string{"a.com", "b.com"}, "dr3", false)
	for _, k := range []string{"a.com", "b.com"} {
		if o := out[k]; !o.NoResponse || !errors.Is(o.Err, lookup.ErrKey) || !strings.Contains(o.Err.Error(), "Unauthorized") {
			t.Errorf("%s: %+v", k, o)
		}
	}
	if m.calls.Load() != 1 {
		t.Errorf("must stop after a rejected key, calls %d", m.calls.Load())
	}
	c.Key = ""
	out = c.Fetch(context.Background(), []string{"a.com"}, "dr3", false)
	if !errors.Is(out["a.com"].Err, lookup.ErrKey) || m.calls.Load() != 1 {
		t.Errorf("no key must not call Ahrefs: %+v", out["a.com"])
	}
}

func TestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	}))
	defer srv.Close()
	c := &Client{Endpoint: srv.URL, Key: "good", Timeout: 50 * time.Millisecond, Retries: 1,
		Sleep: func(ctx context.Context, d time.Duration) error { return nil }}
	out := c.Fetch(context.Background(), []string{"a.com"}, "dr3", false)
	if o := out["a.com"]; !o.NoResponse || !strings.Contains(o.Err.Error(), "timeout") {
		t.Fatalf("%+v", o)
	}
	if c.RequestsLastHour() != 2 {
		t.Fatalf("requests %d", c.RequestsLastHour())
	}
}

func TestRateLimit(t *testing.T) {
	m := &mock{t: t}
	c, slept := newClient(t, m)
	c.RPM = 60
	c.Batch = 1
	c.Fetch(context.Background(), []string{"a.com", "b.com", "c.com"}, "dr3", false)
	if len(*slept) != 2 {
		t.Fatalf("slept %v", *slept)
	}
	for _, d := range *slept {
		if d <= 0 || d > 2*time.Second {
			t.Fatalf("slept %v", *slept)
		}
	}
}

func TestErrorText(t *testing.T) {
	cases := map[string]string{
		`{"error":"bad target"}`:   "bad target",
		`["Error","Unauthorized"]`: "Unauthorized",
		`oops`:                     "oops",
		``:                         "empty response",
	}
	for in, want := range cases {
		if got := errorText([]byte(in)); got != want {
			t.Errorf("errorText(%q) = %q", in, got)
		}
	}
}
