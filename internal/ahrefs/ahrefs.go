// Package ahrefs talks to the free Ahrefs Domain Rating endpoint:
// POST https://api.ahrefs.com/v3/public/domain-rating-free
//
// Response shape (per the official docs):
//
//	{"domain_rating":{"license":"…","targets":[{"target":"x.com","domain_rating":49.0}]}}
//
// Each target object may also carry an "error" string. A target with an
// error, a null or missing domain_rating, or one absent from the response is
// treated as "no data": Ahrefs answered, there is just no rating. A rating of
// 0 is a real value (a domain without meaningful backlinks). Errors come back
// as {"error":"…"} or ["Error","…"]; 401 means a rejected key, 403 a missing one.
package ahrefs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/index-pages/getdr/internal/buildinfo"
	"github.com/index-pages/getdr/internal/lookup"
)

// Endpoint is the only Ahrefs URL getdr calls.
const Endpoint = "https://api.ahrefs.com/v3/public/domain-rating-free"

// Client fetches Domain Rating from Ahrefs in batches, with a requests-per-
// minute limit and retries with exponential backoff on 429, 5xx and network
// errors. It is safe for concurrent use; the limit is shared.
type Client struct {
	Endpoint string
	Key      string
	Batch    int
	RPM      int
	Timeout  time.Duration
	Retries  int
	HTTP     *http.Client
	// Log receives verbose diagnostics; may be nil.
	Log func(format string, args ...any)
	// Progress is called before each batch (1-based); may be nil.
	Progress func(batch, total int)

	// Backoff is the first retry delay, doubled on every retry (default 1s).
	Backoff time.Duration
	// Sleep waits for d or until ctx is done (tests replace it).
	Sleep func(ctx context.Context, d time.Duration) error

	mu   sync.Mutex
	next time.Time // earliest start of the next request

	statMu sync.Mutex
	sent   []time.Time
}

func (c *Client) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// wait blocks until the rate limit allows another request.
func (c *Client) wait(ctx context.Context) error {
	if c.RPM <= 0 {
		return nil
	}
	interval := time.Minute / time.Duration(c.RPM)
	c.mu.Lock()
	now := time.Now()
	start := c.next
	if start.Before(now) {
		start = now
	}
	c.next = start.Add(interval)
	c.mu.Unlock()
	if d := start.Sub(now); d > 0 {
		c.logf("ahrefs: rate limit, waiting %s", d.Round(time.Millisecond))
		return c.sleep(ctx, d)
	}
	return nil
}

// RequestsLastHour counts HTTP requests sent to Ahrefs during the last hour.
func (c *Client) RequestsLastHour() int {
	c.statMu.Lock()
	defer c.statMu.Unlock()
	c.pruneLocked(time.Now())
	return len(c.sent)
}

func (c *Client) pruneLocked(now time.Time) {
	cut := now.Add(-time.Hour)
	i := 0
	for i < len(c.sent) && c.sent[i].Before(cut) {
		i++
	}
	c.sent = c.sent[i:]
}

func (c *Client) record() {
	c.statMu.Lock()
	now := time.Now()
	c.pruneLocked(now)
	c.sent = append(c.sent, now)
	c.statMu.Unlock()
}

type response struct {
	DomainRating struct {
		Targets []struct {
			Target       string   `json:"target"`
			DomainRating *float64 `json:"domain_rating"`
			Error        string   `json:"error"`
		} `json:"targets"`
	} `json:"domain_rating"`
}

// Fetch implements lookup.Fetcher. fresh is ignored: Ahrefs has no cache knob.
func (c *Client) Fetch(ctx context.Context, targets []string, version string, _ bool) map[string]lookup.Outcome {
	out := make(map[string]lookup.Outcome, len(targets))
	fail := func(ts []string, err error) {
		for _, t := range ts {
			out[t] = lookup.Outcome{NoResponse: true, Err: err}
		}
	}
	if c.Key == "" {
		fail(targets, fmt.Errorf("%w: no key set", lookup.ErrKey))
		return out
	}
	size := c.Batch
	if size <= 0 || size > 1000 {
		size = 1000
	}
	total := (len(targets) + size - 1) / size
	for i := 0; i < total; i++ {
		batch := targets[i*size : min((i+1)*size, len(targets))]
		if c.Progress != nil {
			c.Progress(i+1, total)
		}
		c.logf("ahrefs: batch %d/%d, %d targets", i+1, total, len(batch))
		got, err := c.post(ctx, batch, version)
		if err != nil {
			if errors.Is(err, lookup.ErrKey) || ctx.Err() != nil {
				fail(targets[i*size:], err)
				return out
			}
			fail(batch, err)
			continue
		}
		for t, o := range got {
			out[t] = o
		}
	}
	return out
}

// post sends one batch, retrying transient failures.
func (c *Client) post(ctx context.Context, batch []string, version string) (map[string]lookup.Outcome, error) {
	body, _ := json.Marshal(map[string]any{"targets": batch, "version": version})
	backoff := c.Backoff
	if backoff <= 0 {
		backoff = time.Second
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := c.wait(ctx); err != nil {
			return nil, err
		}
		status, data, retryAfter, err := c.do(ctx, body)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
		case status == http.StatusOK:
			return parse(data, batch)
		case status == http.StatusUnauthorized:
			return nil, fmt.Errorf("%w rejected by Ahrefs (HTTP 401: %s)", lookup.ErrKey, errorText(data))
		case status == http.StatusForbidden:
			return nil, fmt.Errorf("%w refused by Ahrefs (HTTP 403: %s)", lookup.ErrKey, errorText(data))
		case status == http.StatusTooManyRequests || status >= 500:
			lastErr = fmt.Errorf("HTTP %d: %s", status, errorText(data))
		default:
			return nil, fmt.Errorf("HTTP %d: %s", status, errorText(data))
		}
		if attempt >= c.Retries {
			if attempt > 0 {
				return nil, fmt.Errorf("%v (after %d retries)", lastErr, attempt)
			}
			return nil, lastErr
		}
		d := backoff << attempt
		if retryAfter > d {
			d = retryAfter
		}
		d = min(d, time.Minute)
		c.logf("ahrefs: %v, retry %d/%d in %s", lastErr, attempt+1, c.Retries, d)
		if err := c.sleep(ctx, d); err != nil {
			return nil, err
		}
	}
}

func (c *Client) do(ctx context.Context, body []byte) (status int, data []byte, retryAfter time.Duration, err error) {
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = Endpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"?output=json", bytes.NewReader(body))
	if err != nil {
		return 0, nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	c.record()
	resp, err := hc.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
			return 0, nil, 0, fmt.Errorf("timeout after %s", c.Timeout)
		}
		return 0, nil, 0, err
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return 0, nil, 0, err
	}
	if s := resp.Header.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
			retryAfter = time.Duration(n) * time.Second
		}
	}
	return resp.StatusCode, data, retryAfter, nil
}

func parse(data []byte, batch []string) (map[string]lookup.Outcome, error) {
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("unexpected response from Ahrefs: %v", err)
	}
	items := r.DomainRating.Targets
	byTarget := make(map[string]int, len(items))
	for i, it := range items {
		byTarget[it.Target] = i
		byTarget[strings.ToLower(it.Target)] = i
	}
	out := make(map[string]lookup.Outcome, len(batch))
	for i, t := range batch {
		j, ok := byTarget[t]
		if !ok {
			j, ok = byTarget[strings.ToLower(t)]
		}
		if !ok && len(items) == len(batch) {
			j, ok = i, true // same length: fall back to request order
		}
		o := lookup.Outcome{}
		if ok && items[j].Error == "" && items[j].DomainRating != nil {
			v := *items[j].DomainRating
			o.DR = &v
		}
		out[t] = o
	}
	return out, nil
}

// errorText extracts a message from {"error":"…"}, ["Error","…"] or raw text.
func errorText(data []byte) string {
	var obj struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &obj) == nil && obj.Error != "" {
		return obj.Error
	}
	var arr []any
	if json.Unmarshal(data, &arr) == nil && len(arr) > 0 {
		if s, ok := arr[len(arr)-1].(string); ok {
			return s
		}
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		s = "empty response"
	}
	return s
}
