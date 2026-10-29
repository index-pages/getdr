// Package remote fetches Domain Rating from a getdr server (the "api"
// setting) instead of Ahrefs: one POST /dr per lookup; the server batches and
// caches on its own.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/index-pages/getdr/internal/buildinfo"
	"github.com/index-pages/getdr/internal/lookup"
)

// Client is a getdr server client.
type Client struct {
	URL   string // base URL, e.g. http://192.168.1.10:8080
	Token string // sent as Bearer when non-empty
	Raw   bool   // ask the server not to normalize targets
	// Timeout bounds the whole request; the server may need several
	// rate-limited Ahrefs batches before it answers.
	Timeout time.Duration
	HTTP    *http.Client
	Log     func(format string, args ...any)
}

type request struct {
	Targets []string `json:"targets"`
	Version string   `json:"version"`
	Fresh   bool     `json:"fresh"`
	Raw     bool     `json:"raw"`
}

// Fetch implements lookup.Fetcher.
func (c *Client) Fetch(ctx context.Context, targets []string, version string, fresh bool) map[string]lookup.Outcome {
	out := make(map[string]lookup.Outcome, len(targets))
	fail := func(err error) map[string]lookup.Outcome {
		for _, t := range targets {
			out[t] = lookup.Outcome{NoResponse: true, Err: err}
		}
		return out
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	body, _ := json.Marshal(request{Targets: targets, Version: version, Fresh: fresh, Raw: c.Raw})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/dr", bytes.NewReader(body))
	if err != nil {
		return fail(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.Log != nil {
		n := fmt.Sprintf("%d targets", len(targets))
		if len(targets) == 1 {
			n = "1 target"
		}
		c.Log("api: POST %s/dr, %s", c.URL, n)
	}
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fail(fmt.Errorf("getdr server %s: %v", c.URL, err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return fail(fmt.Errorf("getdr server %s: %v", c.URL, err))
	}
	if resp.StatusCode != http.StatusOK {
		msg := errorText(data)
		switch resp.StatusCode {
		case http.StatusServiceUnavailable:
			return fail(fmt.Errorf("%w: getdr server %s: %s", lookup.ErrKey, c.URL, msg))
		case http.StatusUnauthorized:
			return fail(fmt.Errorf("getdr server %s: %s (set the token with --token or GETDR_SERVE_TOKEN)", c.URL, msg))
		}
		return fail(fmt.Errorf("getdr server %s: HTTP %d: %s", c.URL, resp.StatusCode, msg))
	}
	var r struct {
		Results []lookup.Record `json:"results"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return fail(fmt.Errorf("getdr server %s: unexpected response: %v", c.URL, err))
	}
	for _, rec := range r.Results {
		o := lookup.Outcome{DR: rec.DR, Cached: rec.Cached}
		if rec.Error == lookup.InvalidError {
			o.Invalid, o.Err = true, lookup.ErrInvalid
		} else if rec.CheckedAt == nil {
			o.NoResponse, o.Err = true, fmt.Errorf("getdr server %s: no response from Ahrefs", c.URL)
		} else if t, err := time.Parse(time.RFC3339, *rec.CheckedAt); err == nil {
			o.CheckedAt = t.UTC()
		}
		out[rec.Target] = o
	}
	for _, t := range targets {
		if _, ok := out[t]; !ok {
			out[t] = lookup.Outcome{NoResponse: true, Err: errors.New("missing from getdr server response")}
		}
	}
	return out
}

func errorText(data []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error != "" {
		return e.Error
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 200 {
		s = s[:200] + "…"
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
