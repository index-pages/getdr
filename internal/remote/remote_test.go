package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefusesCrossHostRedirect(t *testing.T) {
	var hit atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit.Add(1) }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/dr", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL, Token: "secret", Timeout: 5 * time.Second}
	out := c.Fetch(context.Background(), []string{"a.com"}, "dr3", false)
	if o := out["a.com"]; !o.NoResponse || !strings.Contains(o.Err.Error(), "refusing redirect") {
		t.Fatalf("%+v", o)
	}
	if hit.Load() != 0 {
		t.Fatal("redirect target must not be contacted")
	}
}

func TestSameHostRedirectAllowed(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dr" {
			http.Redirect(w, r, "/v2/dr", http.StatusPermanentRedirect)
			return
		}
		w.Write([]byte(`{"results":[{"target":"a.com","dr":5,"version":"dr3","checked_at":"2026-10-28T12:00:00Z","cached":true}]}`))
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL, Timeout: 5 * time.Second}
	out := c.Fetch(context.Background(), []string{"a.com"}, "dr3", false)
	if o := out["a.com"]; o.NoResponse || o.DR == nil || *o.DR != 5 || !o.Cached {
		t.Fatalf("%+v", o)
	}
}
