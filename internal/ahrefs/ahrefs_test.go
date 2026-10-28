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

func TestParseUsesItemOnce(t *testing.T) {
	data := []byte(`{"domain_rating":{"targets":[{"target":"https://b.com/","domain_rating":5},{"target":"a.com","domain_rating":77}]}}`)
	out, err := parse(data, []string{"a.com", "b.com"})
	if err != nil {
		t.Fatal(err)
	}
	if o := out["a.com"]; o.DR == nil || *o.DR != 77 {
		t.Errorf("a.com %+v", o)
	}
	if o := out["b.com"]; o.DR == nil || *o.DR != 5 {
		t.Errorf("b.com must match https://b.com/ by normalized name, got %+v", o.DR)
	}
	// Unknown name at a position already claimed by name: no data, never a reused value.
	data = []byte(`{"domain_rating":{"targets":[{"target":"zzz","domain_rating":5},{"target":"a.com","domain_rating":77}]}}`)
	out, _ = parse(data, []string{"a.com", "b.com"})
	if o := out["a.com"]; o.DR == nil || *o.DR != 77 {
		t.Errorf("a.com %+v", o)
	}
	if o := out["b.com"]; o.DR != nil {
		t.Errorf("b.com must be no data, got %v", *o.DR)
	}
	// Positional fallback never reuses an item matched by name.
	data = []byte(`{"domain_rating":{"targets":[{"target":"a.com","domain_rating":77},{"target":"zzz","domain_rating":3}]}}`)
	out, _ = parse(data, []string{"b.com", "a.com"})
	if o := out["a.com"]; o.DR == nil || *o.DR != 77 {
		t.Errorf("a.com %+v", o)
	}
	if o := out["b.com"]; o.DR != nil {
		t.Errorf("b.com position 0 holds a.com's item, must be no data: %v", *o.DR)
	}
	// Different lengths: no positional fallback.
	data = []byte(`{"domain_rating":{"targets":[{"target":"x","domain_rating":1}]}}`)
	out, _ = parse(data, []string{"a.com", "b.com"})
	if out["a.com"].DR != nil || out["b.com"].DR != nil {
		t.Error("no fallback when lengths differ")
	}
}

func TestRefusesCrossHostRedirect(t *testing.T) {
	var hit atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit.Add(1) }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c := &Client{Endpoint: srv.URL, Key: "good", Timeout: 5 * time.Second}
	out := c.Fetch(context.Background(), []string{"a.com"}, "dr3", false)
	if o := out["a.com"]; !o.NoResponse || !strings.Contains(o.Err.Error(), "refusing redirect") {
		t.Fatalf("%+v", o)
	}
	if hit.Load() != 0 {
		t.Fatal("redirect target must not be contacted")
	}
}

// realisticAhrefs mimics the live endpoint: the first invalid target fails
// the whole batch with 400, items echo targets with a trailing slash, and
// unknown domains get 0.
type realisticAhrefs struct {
	mu     sync.Mutex
	bodies [][]string
}

func (m *realisticAhrefs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Targets []string `json:"targets"`
		Version string   `json:"version"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	m.mu.Lock()
	m.bodies = append(m.bodies, body.Targets)
	m.mu.Unlock()
	if body.Version != "dr3" && body.Version != "dr4" {
		w.WriteHeader(400)
		fmt.Fprintf(w, `{ "error": "bad 'version': '%s' is not one of [dr3, dr4] (case-sensitive)" }`, body.Version)
		return
	}
	var items []string
	for _, t := range body.Targets {
		if t == "" || t == "localhost" || strings.Contains(t, " ") || strings.Contains(t, "..") || t == "ghost" {
			if t == "ghost" {
				t = "somebody-else" // a 400 naming a target we did not send
			}
			w.WriteHeader(400)
			fmt.Fprintf(w, `{ "error": "bad 'targets': invalid domain or url: '%s'" }`, t)
			return
		}
		dr := 0.0
		if t == "ahrefs.com" {
			dr = 91
		}
		items = append(items, fmt.Sprintf(`{"target":%q,"domain_rating":%v}`, t+"/", dr))
	}
	fmt.Fprintf(w, `{"domain_rating":{"license":"x","targets":[%s]}}`, strings.Join(items, ","))
}

func TestInvalidTargetIsolation(t *testing.T) {
	m := &realisticAhrefs{}
	srv := httptest.NewServer(m)
	defer srv.Close()
	c := &Client{Endpoint: srv.URL, Key: "k", Batch: 1000, Retries: 0, Timeout: 5 * time.Second}
	out := c.Fetch(context.Background(), []string{"ahrefs.com", "exa mple.com", "localhost", "a..com", "unknown-site.example"}, "dr3", false)
	for _, bad := range []string{"exa mple.com", "localhost", "a..com"} {
		o := out[bad]
		if !o.Invalid || o.NoResponse || o.DR != nil || !errors.Is(o.Err, lookup.ErrInvalid) {
			t.Errorf("%s: %+v", bad, o)
		}
	}
	if o := out["ahrefs.com"]; o.Invalid || o.NoResponse || o.DR == nil || *o.DR != 91 {
		t.Errorf("ahrefs.com %+v", o)
	}
	if o := out["unknown-site.example"]; o.Invalid || o.DR == nil || *o.DR != 0 {
		t.Errorf("unknown domain gets 0 from Ahrefs: %+v", o)
	}
	if len(m.bodies) != 4 || len(m.bodies[3]) != 2 {
		t.Fatalf("expected 3 rejected attempts and 1 good one, got %v", m.bodies)
	}

	// Every target invalid: the batch empties, no further request.
	m.bodies = nil
	out = c.Fetch(context.Background(), []string{"localhost"}, "dr3", false)
	if !out["localhost"].Invalid || len(m.bodies) != 1 {
		t.Fatalf("%+v %v", out["localhost"], m.bodies)
	}

	// A 400 naming something we did not send, or any other 400: whole batch, no response.
	out = c.Fetch(context.Background(), []string{"ahrefs.com", "ghost"}, "dr3", false)
	for _, k := range []string{"ahrefs.com", "ghost"} {
		if o := out[k]; !o.NoResponse || o.Invalid {
			t.Errorf("%s: %+v", k, o)
		}
	}
	out = c.Fetch(context.Background(), []string{"ahrefs.com"}, "dr5", false)
	if o := out["ahrefs.com"]; !o.NoResponse || !strings.Contains(o.Err.Error(), "bad 'version'") {
		t.Errorf("bad version: %+v", o)
	}
}

func TestInvalidIsolationPerBatch(t *testing.T) {
	m := &realisticAhrefs{}
	srv := httptest.NewServer(m)
	defer srv.Close()
	c := &Client{Endpoint: srv.URL, Key: "k", Batch: 2, Timeout: 5 * time.Second}
	out := c.Fetch(context.Background(), []string{"a.com", "localhost", "b.com", "c.com"}, "dr3", false)
	if !out["localhost"].Invalid || out["a.com"].DR == nil || out["b.com"].DR == nil || out["c.com"].DR == nil {
		t.Fatalf("%+v", out)
	}
	if len(m.bodies) != 3 {
		t.Fatalf("requests %v", m.bodies)
	}
}

func TestParseIDNAndRawEcho(t *testing.T) {
	// Ahrefs echoes IDN targets in Unicode and raw URLs normalized its own way.
	data := []byte(`{"domain_rating":{"targets":[{"target":"пример.рф/","domain_rating":10},{"target":"www.theverge.com/tech","domain_rating":93}]}}`)
	out, _ := parse(data, []string{"https://www.Theverge.com/tech", "xn--e1afmkfd.xn--p1ai"})
	if o := out["https://www.Theverge.com/tech"]; o.DR == nil || *o.DR != 93 {
		t.Errorf("raw URL %+v", o)
	}
	if o := out["xn--e1afmkfd.xn--p1ai"]; o.DR == nil || *o.DR != 10 {
		t.Errorf("punycode %+v", o)
	}
}
