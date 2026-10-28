package lookup_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/index-pages/getdr/internal/cache"
	"github.com/index-pages/getdr/internal/lookup"
)

func f(v float64) *float64 { return &v }

// fake answers from a map; targets absent from vals get no data, targets in
// down get no response.
type fake struct {
	mu    sync.Mutex
	vals  map[string]float64
	down  map[string]bool
	calls [][]string
	gate  chan struct{} // if set, Fetch blocks until closed
}

func (f *fake) Fetch(ctx context.Context, targets []string, version string, fresh bool) map[string]lookup.Outcome {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), targets...))
	f.mu.Unlock()
	if f.gate != nil {
		<-f.gate
	}
	out := map[string]lookup.Outcome{}
	for _, t := range targets {
		switch v, ok := f.vals[t]; {
		case f.down[t]:
			out[t] = lookup.Outcome{NoResponse: true, Err: errors.New("HTTP 429")}
		case ok:
			if version == "dr4" {
				v += 100
			}
			out[t] = lookup.Outcome{DR: &v}
		default:
			out[t] = lookup.Outcome{}
		}
	}
	return out
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func setup(t *testing.T) (*lookup.Engine, *fake, *clock) {
	db, err := cache.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fk := &fake{vals: map[string]float64{"a.com": 40, "b.com": 61.3}, down: map[string]bool{}}
	clk := &clock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	e := &lookup.Engine{Cache: db, Fetcher: fk, TTL: 7 * 24 * time.Hour, TTLNone: 24 * time.Hour, Now: clk.now}
	return e, fk, clk
}

func TestTTLAndCache(t *testing.T) {
	e, fk, clk := setup(t)
	ctx := context.Background()
	if _, err := e.Lookup(ctx, []string{"a.com", "none.com"}, "dr3", false); err != nil {
		t.Fatal(err)
	}
	rs, _ := e.Lookup(ctx, []string{"a.com", "none.com"}, "dr3", false)
	if rs[0].Status != lookup.OK || !rs[0].Cached || *rs[0].DR != 40 {
		t.Fatalf("a.com %+v", rs[0])
	}
	if rs[1].Status != lookup.NoData || !rs[1].Cached {
		t.Fatalf("none.com must be cached no data: %+v", rs[1])
	}
	fk.down["x.com"] = true
	rs, _ = e.Lookup(ctx, []string{"x.com"}, "dr3", false)
	if rs[0].Status != lookup.NoResponse || rs[0].Cached || !rs[0].CheckedAt.IsZero() {
		t.Fatalf("x.com %+v", rs[0])
	}
	if rec := rs[0].Record(); rec.CheckedAt != nil || rec.DR != nil {
		t.Fatalf("no response record %+v", rec)
	}
	// No response is never cached.
	fk.calls = nil
	e.Lookup(ctx, []string{"x.com"}, "dr3", false)
	if len(fk.calls) != 1 {
		t.Fatalf("no response must not be cached, calls %v", fk.calls)
	}

	// After 2 days: no-data entries (ttl_none 1d) are stale, values are not.
	clk.t = clk.t.Add(48 * time.Hour)
	fk.calls = nil
	rs, _ = e.Lookup(ctx, []string{"a.com", "none.com"}, "dr3", false)
	if len(fk.calls) != 1 || len(fk.calls[0]) != 1 || fk.calls[0][0] != "none.com" {
		t.Fatalf("calls %v", fk.calls)
	}
	if !rs[0].Cached || rs[1].Cached {
		t.Fatalf("cached flags %v %v", rs[0].Cached, rs[1].Cached)
	}
	// After 8 days everything is stale.
	clk.t = clk.t.Add(6 * 24 * time.Hour)
	fk.vals["a.com"] = 44
	fk.calls = nil
	rs, _ = e.Lookup(ctx, []string{"a.com"}, "dr3", false)
	if rs[0].Cached || *rs[0].DR != 44 || len(fk.calls) != 1 {
		t.Fatalf("a.com %+v", rs[0])
	}
	if d := rs[0].Delta(); d == nil || *d != 4 {
		t.Fatalf("delta %v", d)
	}
	if len(rs[0].History) != 2 {
		t.Fatalf("history %+v", rs[0].History)
	}
}

func TestFreshAndVersions(t *testing.T) {
	e, fk, _ := setup(t)
	ctx := context.Background()
	e.Lookup(ctx, []string{"a.com"}, "dr3", false)
	fk.calls = nil
	rs, _ := e.Lookup(ctx, []string{"a.com"}, "dr4", false)
	if rs[0].Cached || *rs[0].DR != 140 || len(fk.calls) != 1 {
		t.Fatalf("dr4 must not use dr3 cache: %+v", rs[0])
	}
	rs, _ = e.Lookup(ctx, []string{"a.com"}, "dr3", false)
	if !rs[0].Cached || *rs[0].DR != 40 {
		t.Fatalf("dr3 %+v", rs[0])
	}
	fk.calls = nil
	rs, _ = e.Lookup(ctx, []string{"a.com"}, "dr3", true)
	if rs[0].Cached || len(fk.calls) != 1 {
		t.Fatalf("fresh must refetch: %+v", rs[0])
	}
	hits, misses := e.Counters()
	if hits != 1 || misses != 3 {
		t.Fatalf("counters %d/%d", hits, misses)
	}
}

func TestNoCache(t *testing.T) {
	fk := &fake{vals: map[string]float64{"a.com": 40}}
	e := &lookup.Engine{Fetcher: fk, TTL: time.Hour, TTLNone: time.Hour}
	e.Lookup(context.Background(), []string{"a.com"}, "dr3", false)
	rs, _ := e.Lookup(context.Background(), []string{"a.com"}, "dr3", false)
	if len(fk.calls) != 2 || rs[0].Cached || rs[0].History != nil || rs[0].Delta() != nil {
		t.Fatalf("calls %v result %+v", fk.calls, rs[0])
	}
}

func TestKeyError(t *testing.T) {
	e := &lookup.Engine{Fetcher: fetchFunc(func(ts []string) map[string]lookup.Outcome {
		out := map[string]lookup.Outcome{}
		for _, t := range ts {
			out[t] = lookup.Outcome{NoResponse: true, Err: errors.Join(lookup.ErrKey, errors.New("rejected"))}
		}
		return out
	})}
	_, err := e.Lookup(context.Background(), []string{"a.com"}, "dr3", false)
	if !errors.Is(err, lookup.ErrKey) {
		t.Fatalf("err %v", err)
	}
}

type fetchFunc func([]string) map[string]lookup.Outcome

func (fn fetchFunc) Fetch(_ context.Context, ts []string, _ string, _ bool) map[string]lookup.Outcome {
	return fn(ts)
}

func TestInflightDedup(t *testing.T) {
	fk := &fake{vals: map[string]float64{"a.com": 40, "b.com": 50}, gate: make(chan struct{})}
	e := &lookup.Engine{Fetcher: fk, TTL: time.Hour, TTLNone: time.Hour}
	var wg sync.WaitGroup
	results := make([][]lookup.Result, 5)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = e.Lookup(context.Background(), []string{"a.com", "b.com"}, "dr3", false)
		}(i)
	}
	// Wait until the first fetch is in flight and the others joined it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		fk.mu.Lock()
		n := len(fk.calls)
		fk.mu.Unlock()
		if n >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(fk.gate)
	wg.Wait()
	if len(fk.calls) != 1 {
		t.Fatalf("expected 1 fetch, got %d: %v", len(fk.calls), fk.calls)
	}
	for _, rs := range results {
		if *rs[0].DR != 40 || *rs[1].DR != 50 {
			t.Fatalf("results %+v", rs)
		}
	}
}

func TestRecordJSON(t *testing.T) {
	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	r := lookup.Result{Target: "x.com", DR: f(49), Version: "dr3", CheckedAt: at, Cached: true}
	b, _ := json.Marshal(r.Record())
	want := `{"target":"x.com","dr":49,"version":"dr3","checked_at":"2026-10-28T12:00:00Z","cached":true}`
	if string(b) != want {
		t.Fatalf("got %s", b)
	}
	b, _ = json.Marshal(lookup.RecordDelta{Record: lookup.Result{Target: "y.com", Version: "dr4", Status: lookup.NoResponse}.Record()})
	want = `{"target":"y.com","dr":null,"version":"dr4","checked_at":null,"cached":false,"delta":null}`
	if string(b) != want {
		t.Fatalf("got %s", b)
	}
}
