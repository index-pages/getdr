// Package server implements the getdr serve HTTP API.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/index-pages/getdr/internal/buildinfo"
	"github.com/index-pages/getdr/internal/cache"
	"github.com/index-pages/getdr/internal/lookup"
	"github.com/index-pages/getdr/internal/target"
)

// Server holds what the handlers need.
type Server struct {
	Engine         *lookup.Engine
	DefaultVersion string
	// HasKey is false when the server has no Ahrefs key; /dr then answers 503.
	HasKey     bool
	Token      string // empty = no authorization
	MaxTargets int
	Cache      *cache.DB  // for /stats; may be nil
	Requests   func() int // Ahrefs requests during the last hour; may be nil
	Log        func(format string, args ...any)
}

// NoKeyMessage is the 503 text when the server has no Ahrefs key.
const NoKeyMessage = "this getdr server has no Ahrefs API key configured (set GETDR_KEY or AHREFS_KEY on the server); see " + buildinfo.KeyHelp

func (s *Server) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	route := map[string]struct {
		methods string
		auth    bool
		h       func(http.ResponseWriter, *http.Request)
	}{
		"/":        {"GET", false, s.root},
		"/healthz": {"GET", false, s.healthz},
		"/teapot":  {"GET", false, s.teapot},
		"/stats":   {"GET", true, s.stats},
		"/dr":      {"GET POST", true, s.dr},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", buildinfo.ServerHeader())
		s.logf("serve: %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		rt, ok := route[r.URL.Path]
		if !ok {
			writeError(w, http.StatusNotFound, "not found; see "+buildinfo.Docs)
			return
		}
		method := r.Method
		if method == http.MethodHead {
			method = http.MethodGet
		}
		if !strings.Contains(rt.methods, method) {
			w.Header().Set("Allow", strings.ReplaceAll(rt.methods, " ", ", "))
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if rt.auth && !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="getdr"`)
			writeError(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		rt.h(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	if s.Token == "" {
		return true
	}
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) < len(p) || !strings.EqualFold(h[:len(p)], p) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(h[len(p):])), []byte(s.Token)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) root(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"name":     "getdr",
		"version":  buildinfo.Version,
		"homepage": buildinfo.Homepage,
		"docs":     buildinfo.Docs,
	})
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok\n"))
}

func (s *Server) teapot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusTeapot)
	w.Write([]byte("I'm a teapot\n"))
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	hits, misses := s.Engine.Counters()
	rate := 0.0
	if hits+misses > 0 {
		rate = float64(hits) / float64(hits+misses)
	}
	out := map[string]any{
		"cache":                     nil,
		"lookups":                   map[string]any{"hits": hits, "misses": misses, "hit_rate": rate},
		"ahrefs_requests_last_hour": 0,
	}
	if s.Cache != nil {
		st, err := s.Cache.Stats()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out["cache"] = st
	}
	if s.Requests != nil {
		out["ahrefs_requests_last_hour"] = s.Requests()
	}
	writeJSON(w, http.StatusOK, out)
}

func boolParam(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "", "0", "false", "no":
		return false, nil
	case "1", "true", "yes":
		return true, nil
	}
	return false, fmt.Errorf("invalid boolean %q", v)
}

func (s *Server) dr(w http.ResponseWriter, r *http.Request) {
	var (
		targets []string
		version string
		fresh   bool
		raw     bool
	)
	single := r.Method != http.MethodPost
	if single {
		q := r.URL.Query()
		t := q.Get("target")
		if t == "" {
			writeError(w, http.StatusBadRequest, "missing target parameter, e.g. /dr?target=example.com")
			return
		}
		var err error
		if fresh, err = boolParam(q.Get("fresh")); err != nil {
			writeError(w, http.StatusBadRequest, "fresh: "+err.Error())
			return
		}
		if raw, err = boolParam(q.Get("raw")); err != nil {
			writeError(w, http.StatusBadRequest, "raw: "+err.Error())
			return
		}
		targets, version = []string{t}, q.Get("version")
	} else {
		limit := int64(max(1<<20, s.MaxTargets*2048))
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		var body struct {
			Targets []string `json:"targets"`
			Version string   `json:"version"`
			Fresh   bool     `json:"fresh"`
			Raw     bool     `json:"raw"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body larger than %d bytes", limit))
				return
			}
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error()+`; expected {"targets":["example.com"],"version":"dr3","fresh":false,"raw":false}`)
			return
		}
		targets, version, fresh, raw = body.Targets, body.Version, body.Fresh, body.Raw
	}
	if version == "" {
		version = s.DefaultVersion
	}
	if version != "dr3" && version != "dr4" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid version %q, use dr3 or dr4", version))
		return
	}
	prepared := target.Prepare(targets, raw)
	if len(prepared) == 0 {
		writeError(w, http.StatusBadRequest, "no valid targets")
		return
	}
	if len(prepared) > s.MaxTargets {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("too many targets: %d, this server accepts at most %d per request", len(prepared), s.MaxTargets))
		return
	}
	if !s.HasKey {
		writeError(w, http.StatusServiceUnavailable, NoKeyMessage)
		return
	}
	// Shared in-flight fetches must not die with one impatient client.
	ctx := context.WithoutCancel(r.Context())
	results, err := s.Engine.Lookup(ctx, prepared, version, fresh)
	if err != nil {
		if errors.Is(err, lookup.ErrKey) {
			writeError(w, http.StatusServiceUnavailable, "this getdr server cannot use its Ahrefs API key: "+err.Error()+"; see "+buildinfo.KeyHelp)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, res := range results {
		if res.Status == lookup.NoResponse {
			s.logf("serve: no response for %s: %v", res.Target, res.Err)
		}
	}
	if single {
		writeJSON(w, http.StatusOK, results[0].Record())
		return
	}
	recs := make([]lookup.Record, len(results))
	for i, res := range results {
		recs[i] = res.Record()
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": recs})
}
