// Command mockserve runs the real getdr serve handler backed by a fake
// Ahrefs, for testing clients (the getdr skill, the MCP server) without an
// Ahrefs key or network access. It lives under testdata, so it is never part
// of the getdr binary or of "go build ./...".
//
//	go run -trimpath ./testdata/mockserve -listen 127.0.0.1:8089 [-token T] [-no-key] [-ahrefs 127.0.0.1:8090]
//
// Fake data is deterministic: every domain gets a DR derived from its name,
// except these special targets:
//
//	nodata.example, *.nodata.example – Ahrefs has no data (dr null)
//	index-pages.com                  – DR 42
//	ahrefs.com                       – DR 91
//
// With -ahrefs ADDR the fake Ahrefs endpoint is also exposed at
// http://ADDR/v3/public/domain-rating-free (Bearer key required; the key
// "bad" is rejected with 401) for testing direct-to-Ahrefs clients.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/index-pages/getdr/internal/ahrefs"
	"github.com/index-pages/getdr/internal/cache"
	"github.com/index-pages/getdr/internal/lookup"
	"github.com/index-pages/getdr/internal/server"
)

func fakeDR(target, version string) *float64 {
	t := strings.ToLower(target)
	switch {
	case t == "nodata.example" || strings.HasSuffix(t, ".nodata.example"):
		return nil
	case t == "index-pages.com":
		v := 42.0
		return &v
	case t == "ahrefs.com":
		v := 91.0
		return &v
	}
	h := fnv.New32a()
	h.Write([]byte(version + "|" + t))
	v := float64(h.Sum32()%1000) / 10
	return &v
}

// fakeAhrefs mimics POST /v3/public/domain-rating-free.
func fakeAhrefs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || auth == "Bearer " {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`["Error","Forbidden"]`))
		return
	}
	if auth == "Bearer bad" {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`["Error","Unauthorized"]`))
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte(`{"error":"use POST"}`))
		return
	}
	var body struct {
		Targets []string `json:"targets"`
		Version string   `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Targets) == 0 || len(body.Targets) > 1000 {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid request"}`))
		return
	}
	if body.Version == "" {
		body.Version = "dr3"
	}
	type item struct {
		Target       string   `json:"target"`
		DomainRating *float64 `json:"domain_rating"`
	}
	items := make([]item, len(body.Targets))
	for i, t := range body.Targets {
		items[i] = item{t, fakeDR(t, body.Version)}
	}
	json.NewEncoder(w).Encode(map[string]any{"domain_rating": map[string]any{
		"license": "https://ahrefs.com/legal/domain-rating-license", "targets": items,
	}})
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8089", "getdr serve address")
	token := flag.String("token", "", "require this bearer token")
	noKey := flag.Bool("no-key", false, "behave like a server without an Ahrefs key (503)")
	ahrefsAddr := flag.String("ahrefs", "", "also expose the fake Ahrefs endpoint on this address")
	cachePath := flag.String("cache", "", "cache file (default: a fresh temporary file)")
	ttl := flag.Duration("ttl", 7*24*time.Hour, "cache TTL")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/v3/public/domain-rating-free", fakeAhrefs)
	fake := httptest.NewServer(mux)
	defer fake.Close()
	if *ahrefsAddr != "" {
		ln, err := net.Listen("tcp", *ahrefsAddr)
		if err != nil {
			log.Fatal(err)
		}
		go http.Serve(ln, mux)
		log.Printf("fake Ahrefs on http://%s/v3/public/domain-rating-free", ln.Addr())
	}

	if *cachePath == "" {
		dir, err := os.MkdirTemp("", "getdr-mockserve-")
		if err != nil {
			log.Fatal(err)
		}
		defer os.RemoveAll(dir)
		*cachePath = filepath.Join(dir, "cache.db")
	}
	db, err := cache.Open(*cachePath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	client := &ahrefs.Client{
		Endpoint: fake.URL + "/v3/public/domain-rating-free",
		Key:      "mock", Batch: 1000, RPM: 0, Timeout: 10 * time.Second, Retries: 0,
	}
	s := &server.Server{
		Engine:         &lookup.Engine{Cache: db, Fetcher: client, TTL: *ttl, TTLNone: 24 * time.Hour},
		DefaultVersion: "dr3",
		HasKey:         !*noKey,
		Token:          *token,
		MaxTargets:     10000,
		Cache:          db,
		Requests:       client.RequestsLastHour,
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "mockserve: getdr API on http://%s (cache %s)\n", ln.Addr(), *cachePath)
	errc := make(chan error, 1)
	go func() { errc <- http.Serve(ln, s.Handler()) }()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errc:
		log.Print(err)
	case <-sig:
	}
	// Returning runs the deferred cleanup (temporary cache, fake Ahrefs).
}
