// Package ahrefs talks to the free Ahrefs Domain Rating endpoint:
// POST https://api.ahrefs.com/v3/public/domain-rating-free
//
// Response shape (docs, confirmed against the live API in October 2026):
//
//	{"domain_rating":{"license":"…","targets":[{"target":"x.com/","domain_rating":49.0}]}}
//
// Items keep the request order and echo each target the way Ahrefs
// normalizes it, with a trailing slash ("ahrefs.com/", "www.theverge.com/tech")
// and IDNs in Unicode ("пример.рф/" for "xn--e1afmkfd.xn--p1ai"); getdr matches
// them by normalized name first and by position only as a fallback. Unknown
// domains get 0.0, not null; an item with an "error", a null or missing
// domain_rating, or a target absent from the response is still treated as
// "no data".
//
// One target Ahrefs does not accept fails the whole batch with HTTP 400 and
// {"error":"bad 'targets': invalid domain or url: '<target>'"}; the client
// then marks that target invalid and resends the rest. Other errors come back
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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/index-pages/getdr/internal/buildinfo"
	"github.com/index-pages/getdr/internal/lookup"
	"github.com/index-pages/getdr/internal/target"
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
			if _, done := out[t]; !done {
				out[t] = lookup.Outcome{NoResponse: true, Err: err}
			}
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
		c.logf("ahrefs: batch %d/%d, %s", i+1, total, plural(len(batch), "target"))
		got, err := c.postIsolating(ctx, batch, version, out)
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

// postIsolating sends a batch. Ahrefs rejects a whole batch with HTTP 400
// when one target is not a domain or URL and names that target in the error;
// postIsolating then marks it invalid in out and resends the rest at once (a
// new request under the rate limit, not a retry) until the batch passes or
// nothing is left.
func (c *Client) postIsolating(ctx context.Context, batch []string, version string, out map[string]lookup.Outcome) (map[string]lookup.Outcome, error) {
	for len(batch) > 0 {
		got, err := c.post(ctx, batch, version)
		var bad *invalidTargetError
		if !errors.As(err, &bad) {
			return got, err
		}
		c.logf("ahrefs: Ahrefs rejected %q as invalid, resending the other %s", bad.target, plural(len(batch)-1, "target"))
		out[bad.target] = lookup.Outcome{Invalid: true, Err: fmt.Errorf("%w (Ahrefs: %s)", lookup.ErrInvalid, bad.msg)}
		rest := make([]string, 0, len(batch)-1)
		for _, t := range batch {
			if t != bad.target {
				rest = append(rest, t)
			}
		}
		batch = rest
	}
	return nil, nil
}

// invalidTargetError is a 400 that names one target of the batch.
type invalidTargetError struct{ target, msg string }

func (e *invalidTargetError) Error() string { return e.msg }

// invalidRe matches the 400 error Ahrefs sends for a bad target, e.g.
// "bad 'targets': invalid domain or url: 'exa mple.com'".
var invalidRe = regexp.MustCompile(`invalid domain or url: '(.*)'\s*$`)

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
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
		case status == http.StatusBadRequest:
			msg := errorText(data)
			if m := invalidRe.FindStringSubmatch(msg); m != nil {
				for _, t := range batch {
					if t == m[1] {
						return nil, &invalidTargetError{target: t, msg: msg}
					}
				}
			}
			return nil, fmt.Errorf("HTTP %d: %s", status, msg)
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
		hc = defaultHTTP
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
		for _, k := range []string{it.Target, strings.ToLower(it.Target), target.Normalize(it.Target)} {
			if _, dup := byTarget[k]; !dup && k != "" {
				byTarget[k] = i
			}
		}
	}
	// Each response item is used at most once: first match by name, then,
	// when the response has as many items as the request, fall back to the
	// request order for targets whose name did not match and whose own
	// position holds an item no other target claimed.
	match := make([]int, len(batch))
	used := make([]bool, len(items))
	for i, t := range batch {
		j, ok := byTarget[t]
		if !ok {
			j, ok = byTarget[strings.ToLower(t)]
		}
		if !ok {
			j, ok = byTarget[target.Normalize(t)]
		}
		if ok && !used[j] {
			match[i], used[j] = j, true
		} else {
			match[i] = -1
		}
	}
	if len(items) == len(batch) {
		for i := range batch {
			if match[i] < 0 && !used[i] {
				match[i], used[i] = i, true
			}
		}
	}
	out := make(map[string]lookup.Outcome, len(batch))
	for i, t := range batch {
		o := lookup.Outcome{}
		if j := match[i]; j >= 0 && items[j].Error == "" && items[j].DomainRating != nil {
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

// defaultHTTP refuses redirects to another host, so a response can never
// send getdr anywhere but the host it was configured to talk to.
var defaultHTTP = &http.Client{CheckRedirect: sameHost}

func sameHost(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("too many redirects")
	}
	if req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
		return fmt.Errorf("refusing redirect from %s://%s to %s://%s", via[0].URL.Scheme, via[0].URL.Host, req.URL.Scheme, req.URL.Host)
	}
	return nil
}
