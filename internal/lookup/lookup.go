// Package lookup resolves targets to Domain Rating results: fresh values come
// from the cache, the rest from a Fetcher (Ahrefs or a getdr server), and new
// measurements are written back to the cache.
package lookup

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/index-pages/getdr/internal/cache"
)

// ErrKey marks a missing or rejected Ahrefs API key. Errors wrapping it make
// the CLI exit with code 3 and getdr serve answer 503.
var ErrKey = errors.New("Ahrefs API key")

// Outcome is what a Fetcher learned about one target.
type Outcome struct {
	// DR is nil when there is no data for the target.
	DR *float64
	// CheckedAt is when the value was measured; zero means now.
	CheckedAt time.Time
	// Cached is true when the source served the value from its own cache.
	Cached bool
	// NoResponse is set when the source gave no answer for the target
	// (timeout, rate limit, server error). Such outcomes are not cached.
	NoResponse bool
	// Err explains NoResponse.
	Err error
}

// Fetcher asks a data source about targets. Every target in the request
// must be present in the returned map.
type Fetcher interface {
	Fetch(ctx context.Context, targets []string, version string, fresh bool) map[string]Outcome
}

// Status of a result.
type Status int

const (
	OK         Status = iota // a DR value is known
	NoData                   // the source answered, but has no DR for the target
	NoResponse               // the source did not answer for the target
)

// Result is the answer for one target.
type Result struct {
	Target    string
	Version   string
	DR        *float64
	CheckedAt time.Time // zero when never checked
	Cached    bool
	Status    Status
	Err       error // set with NoResponse
	// History holds the stored checks for (target, version), oldest first,
	// including this result when it was measured. Nil without a cache.
	History []cache.Check
}

// Delta is the difference between the last and the previous check, nil when
// either is missing or has no data.
func (r Result) Delta() *float64 {
	if r.Status == NoResponse || len(r.History) < 2 {
		return nil
	}
	last, prev := r.History[len(r.History)-1], r.History[len(r.History)-2]
	if last.DR == nil || prev.DR == nil {
		return nil
	}
	d := math.Round((*last.DR-*prev.DR)*1e4) / 1e4
	return &d
}

// Record is the JSON contract shared by the CLI, getdr serve, the skill and
// the MCP server.
type Record struct {
	Target    string   `json:"target"`
	DR        *float64 `json:"dr"`
	Version   string   `json:"version"`
	CheckedAt *string  `json:"checked_at"`
	Cached    bool     `json:"cached"`
}

// RecordDelta is Record plus delta, used when delta is requested.
type RecordDelta struct {
	Record
	Delta *float64 `json:"delta"`
}

// Record converts the result to the JSON contract.
func (r Result) Record() Record {
	rec := Record{Target: r.Target, DR: r.DR, Version: r.Version, Cached: r.Cached}
	if !r.CheckedAt.IsZero() {
		s := r.CheckedAt.UTC().Format(time.RFC3339)
		rec.CheckedAt = &s
	}
	return rec
}

// Engine combines a cache and a fetcher.
type Engine struct {
	Cache   *cache.DB // nil disables the cache entirely
	Fetcher Fetcher
	TTL     time.Duration // freshness of checks with a value
	TTLNone time.Duration // freshness of checks without data
	Now     func() time.Time
	// Log receives verbose diagnostics; may be nil.
	Log func(format string, args ...any)

	mu       sync.Mutex
	inflight map[string]*call

	hits, misses atomic.Int64
}

type call struct {
	done chan struct{}
	out  Outcome
}

// Counters returns how many target lookups were answered from the cache and
// how many needed a fetch since the engine was created.
func (e *Engine) Counters() (hits, misses int64) { return e.hits.Load(), e.misses.Load() }

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) logf(format string, args ...any) {
	if e.Log != nil {
		e.Log(format, args...)
	}
}

// Lookup resolves targets (already normalized and de-duplicated) for one DR
// version. With fresh the cache is not read, but still written. Results keep
// the order of targets. The error is non-nil only for cache failures or
// wraps ErrKey when the key is missing or rejected.
func (e *Engine) Lookup(ctx context.Context, targets []string, version string, fresh bool) ([]Result, error) {
	now := e.now().UTC().Truncate(time.Second)
	var hist map[string][]cache.Check
	if e.Cache != nil {
		var err error
		if hist, err = e.Cache.History(version, targets); err != nil {
			return nil, err
		}
	}
	results := make([]Result, len(targets))
	var need []string
	for i, t := range targets {
		h := hist[t]
		results[i] = Result{Target: t, Version: version, History: h}
		if e.Cache != nil && results[i].History == nil {
			results[i].History = []cache.Check{}
		}
		if !fresh && len(h) > 0 {
			last := h[len(h)-1]
			ttl := e.TTL
			if last.DR == nil {
				ttl = e.TTLNone
			}
			if now.Sub(last.CheckedAt) < ttl {
				results[i].DR, results[i].CheckedAt, results[i].Cached = last.DR, last.CheckedAt, true
				results[i].Status = OK
				if last.DR == nil {
					results[i].Status = NoData
				}
				continue
			}
		}
		need = append(need, t)
	}
	e.hits.Add(int64(len(targets) - len(need)))
	e.misses.Add(int64(len(need)))
	e.logf("cache: %d fresh, %d to fetch (%s)", len(targets)-len(need), len(need), version)
	if len(need) == 0 {
		return results, nil
	}

	outs := e.fetch(ctx, need, version, fresh, now)
	var keyErr error
	for i := range results {
		if results[i].Cached {
			continue
		}
		o := outs[results[i].Target]
		r := &results[i]
		if o.NoResponse {
			r.Status, r.Err = NoResponse, o.Err
			if keyErr == nil && errors.Is(o.Err, ErrKey) {
				keyErr = o.Err
			}
			continue
		}
		r.DR, r.CheckedAt, r.Cached = o.DR, o.CheckedAt, o.Cached
		if r.CheckedAt.IsZero() {
			r.CheckedAt = now
		}
		r.Status = OK
		if o.DR == nil {
			r.Status = NoData
		}
		if r.History != nil {
			n := len(r.History)
			if n == 0 || !r.History[n-1].CheckedAt.Equal(r.CheckedAt) {
				r.History = append(r.History, cache.Check{DR: r.DR, CheckedAt: r.CheckedAt})
			}
		}
	}
	return results, keyErr
}

// fetch asks the fetcher about targets, joining requests already in flight
// for the same (target, version), and writes new measurements to the cache.
func (e *Engine) fetch(ctx context.Context, targets []string, version string, fresh bool, now time.Time) map[string]Outcome {
	e.mu.Lock()
	if e.inflight == nil {
		e.inflight = map[string]*call{}
	}
	var mine []string
	owned := map[string]*call{}
	waits := map[string]*call{}
	for _, t := range targets {
		k := version + "\x00" + t
		if c, ok := e.inflight[k]; ok {
			waits[t] = c
			continue
		}
		c := &call{done: make(chan struct{})}
		e.inflight[k] = c
		owned[t] = c
		mine = append(mine, t)
	}
	e.mu.Unlock()
	if len(waits) > 0 {
		e.logf("dedup: %d targets already in flight", len(waits))
	}

	out := make(map[string]Outcome, len(targets))
	if len(mine) > 0 {
		got := e.Fetcher.Fetch(ctx, mine, version, fresh)
		var rows []cache.Row
		for _, t := range mine {
			o, ok := got[t]
			if !ok {
				o = Outcome{NoResponse: true, Err: errors.New("missing from response")}
			}
			if !o.NoResponse && o.CheckedAt.IsZero() {
				o.CheckedAt = now
			}
			out[t] = o
			if !o.NoResponse {
				rows = append(rows, cache.Row{Domain: t, DR: o.DR, CheckedAt: o.CheckedAt})
			}
		}
		if e.Cache != nil {
			if err := e.Cache.Insert(version, rows); err != nil {
				e.logf("%v", err)
			}
		}
		e.mu.Lock()
		for _, t := range mine {
			c := owned[t]
			c.out = out[t]
			delete(e.inflight, version+"\x00"+t)
			close(c.done)
		}
		e.mu.Unlock()
	}
	for t, c := range waits {
		select {
		case <-c.done:
			out[t] = c.out
		case <-ctx.Done():
			out[t] = Outcome{NoResponse: true, Err: ctx.Err()}
		}
	}
	return out
}
