package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/index-pages/getdr/internal/buildinfo"
	"github.com/index-pages/getdr/internal/cache"
	"github.com/index-pages/getdr/internal/lookup"
	"github.com/index-pages/getdr/internal/server"
)

// fakeAhrefs answers like Ahrefs: DR = 10 × label length; "nodata*" → null;
// a batch containing "down*" → 500; key "bad" → 401.
type fakeAhrefs struct {
	mu       sync.Mutex
	requests int
	targets  int
}

func (f *fakeAhrefs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests++
	f.mu.Unlock()
	if r.Header.Get("User-Agent") != buildinfo.UserAgent() {
		http.Error(w, "bad UA", 400)
		return
	}
	if r.Header.Get("Authorization") == "Bearer bad" {
		w.WriteHeader(401)
		w.Write([]byte(`["Error","Unauthorized"]`))
		return
	}
	var body struct {
		Targets []string `json:"targets"`
		Version string   `json:"version"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.targets += len(body.Targets)
	f.mu.Unlock()
	var items []string
	for _, t := range body.Targets {
		if t == "localhost" || strings.Contains(t, "..") {
			w.WriteHeader(400)
			fmt.Fprintf(w, `{ "error": "bad 'targets': invalid domain or url: '%s'" }`, t)
			return
		}
		if strings.HasPrefix(t, "down") {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":"internal"}`))
			return
		}
		if strings.HasPrefix(t, "nodata") {
			items = append(items, fmt.Sprintf(`{"target":%q,"domain_rating":null}`, t))
			continue
		}
		v := float64(len(strings.Split(t, ".")[0]) * 10)
		if body.Version == "dr4" {
			v += 1.5
		}
		items = append(items, fmt.Sprintf(`{"target":%q,"domain_rating":%v}`, t, v))
	}
	fmt.Fprintf(w, `{"domain_rating":{"license":"x","targets":[%s]}}`, strings.Join(items, ","))
}

type harness struct {
	t     *testing.T
	fake  *fakeAhrefs
	dir   string
	env   map[string]string
	stdin string
	tty   bool
	width int
}

func newHarness(t *testing.T) *harness {
	fake := &fakeAhrefs{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	old := ahrefsEndpoint
	ahrefsEndpoint = srv.URL
	t.Cleanup(func() { ahrefsEndpoint = old })
	dir := t.TempDir()
	return &harness{t: t, fake: fake, dir: dir, env: map[string]string{
		"HOME":                 dir,
		"XDG_CONFIG_HOME":      filepath.Join(dir, "config"),
		"GETDR_CACHE_PATH":     filepath.Join(dir, "cache.db"),
		"GETDR_KEY":            "good-key-1234",
		"GETDR_AHREFS_RETRIES": "0",
		"GETDR_AHREFS_RPM":     "100000",
		"GETDR_AHREFS_TIMEOUT": "5s",
		"LANG":                 "en_US.UTF-8",
		"TERM":                 "xterm-256color",
	}}
}

func (h *harness) run(args ...string) (code int, stdout, stderr string) {
	var out, errb strings.Builder
	env := &Env{
		Stdin:     strings.NewReader(h.stdin),
		Stdout:    &out,
		Stderr:    &errb,
		Getenv:    func(k string) string { return h.env[k] },
		StdinTTY:  h.stdin == "",
		StdoutTTY: h.tty,
		Width:     h.width,
	}
	code = Run(args, env)
	return code, out.String(), errb.String()
}

func TestTSVDefaultAndCache(t *testing.T) {
	h := newHarness(t)
	code, out, errs := h.run("https://www.abc.com/x", "abcd.io", "abc.com", "nodata.net")
	if code != 0 || out != "abc.com\t30\nabcd.io\t40\nnodata.net\t\n" {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
	if errs != "" {
		t.Fatalf("stderr must stay empty: %q", errs)
	}
	// Second run: everything from the cache, flags after positionals.
	code, out, _ = h.run("abc.com", "nodata.net", "--format", "json", "--fields", "domain,dr,delta")
	if code != 0 || h.fake.targets != 3 {
		t.Fatalf("code %d, Ahrefs saw %d targets", code, h.fake.targets)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["target"] != "abc.com" || rec["dr"] != 30.0 || rec["cached"] != true || rec["version"] != "dr3" || rec["checked_at"] == nil {
		t.Fatalf("json %s", lines[0])
	}
	if _, ok := rec["delta"]; !ok {
		t.Fatalf("delta requested: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"dr":null`) || !strings.Contains(lines[1], `"cached":true`) {
		t.Fatalf("no data must be cached: %s", lines[1])
	}
	// --fresh refetches; dr4 never reuses dr3.
	h.run("abc.com", "--fresh")
	h.run("abc.com", "--version", "dr4")
	if h.fake.targets != 5 {
		t.Fatalf("Ahrefs saw %d targets, want 5", h.fake.targets)
	}
	_, out, _ = h.run("abc.com", "--version=dr4", "-b", "-q")
	if out != "31.5\n" {
		t.Fatalf("dr4 bare %q", out)
	}
}

func TestStdinFileBare(t *testing.T) {
	h := newHarness(t)
	h.stdin = "# list\nb.com\n\nccc.com\nb.com\n"
	code, out, errs := h.run("-b", "-q")
	if code != 0 || out != "10\n30\n" || errs != "" {
		t.Fatalf("stdin: %d %q %q", code, out, errs)
	}
	file := filepath.Join(h.dir, "list.txt")
	os.WriteFile(file, []byte("dddd.com\n#x.com\nnodata.org\n"), 0o600)
	h.stdin = ""
	code, out, _ = h.run("-f", file, "-b")
	if code != 0 || out != "40\n\n" {
		t.Fatalf("file: %d %q", code, out)
	}
	h.stdin = "ee.com\n"
	code, out, _ = h.run("-f", "-", "--format", "csv")
	if code != 0 || out != "domain,dr\nee.com,20\n" {
		t.Fatalf("-f -: %d %q", code, out)
	}
	outFile := filepath.Join(h.dir, "out.tsv")
	h.stdin = ""
	h.tty = true
	code, out, _ = h.run("ee.com", "-o", outFile)
	data, _ := os.ReadFile(outFile)
	if code != 0 || out != "" || string(data) != "ee.com\t20\n" {
		t.Fatalf("-o: %d %q %q", code, out, data)
	}
}

func TestExitCodes(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.run("aaaaa.com", "bb.com", "--min", "30", "-q"); code != 1 {
		t.Errorf("--min fail: %d", code)
	}
	code, out, _ := h.run("aaaaa.com", "bb.com", "--max", "30", "-q")
	if code != 1 || out != "bb.com\t20\n" {
		t.Errorf("--max: %d %q", code, out)
	}
	if code, _, _ := h.run("aaaaa.com", "bb.com", "--min", "10", "--max", "60", "-q"); code != 0 {
		t.Errorf("all pass: %d", code)
	}
	if code, _, _ := h.run("nodata.com", "--min", "0", "-q"); code != 1 {
		t.Errorf("no data fails --min: %d", code)
	}

	h.env["GETDR_AHREFS_BATCH"] = "1"
	code, out, errs := h.run("down.com", "ok.com", "-q")
	if code != 2 || out != "down.com\t\nok.com\t20\n" || !strings.Contains(errs, "no response for 1 target(s): HTTP 500: internal") || !strings.Contains(errs, "down.com") {
		t.Errorf("no response: %d %q %q", code, out, errs)
	}
	code, out, _ = h.run("down.com", "--format", "json", "-q")
	if code != 2 || out != `{"target":"down.com","dr":null,"version":"dr3","checked_at":null,"cached":false}`+"\n" {
		t.Errorf("no response json: %d %q", code, out)
	}
	// No response is not cached.
	before := h.fake.requests
	h.run("down.com", "-q")
	if h.fake.requests != before+1 {
		t.Error("no response must not be cached")
	}

	h.env["GETDR_KEY"] = ""
	code, _, errs = h.run("new1.com")
	if code != 3 || !strings.Contains(errs, "https://index-pages.com/getdr#key") {
		t.Errorf("no key: %d %q", code, errs)
	}
	if code, _, _ = h.run("ok.com", "-q"); code != 0 {
		t.Errorf("cached targets need no key: %d", code)
	}
	code, _, errs = h.run("new2.com", "--key", "bad")
	if code != 3 || !strings.Contains(errs, "401") || !strings.Contains(errs, "#key") {
		t.Errorf("bad key: %d %q", code, errs)
	}
	if code, _, _ := h.run("--version"); code != 2 {
		t.Errorf("--version without value: %d", code)
	}
	if code, _, _ := h.run("x.com", "--format", "xml"); code != 2 {
		t.Errorf("bad format: %d", code)
	}
	if code, _, _ := h.run("x.com", "--nope"); code != 2 {
		t.Errorf("unknown flag: %d", code)
	}
	if code, _, errs := h.run(); code != 2 || !strings.Contains(errs, "Usage:") {
		t.Errorf("no input on a TTY: %d %q", code, errs)
	}
}

func TestVersionAndHelp(t *testing.T) {
	h := newHarness(t)
	if code, out, _ := h.run("-V"); code != 0 || out != "getdr "+buildinfo.Version+"\n" {
		t.Errorf("-V: %d %q", code, out)
	}
	if code, out, _ := h.run("--help"); code != 0 || !strings.Contains(out, "--no-cache") {
		t.Errorf("--help: %d", code)
	}
}

func TestCardAndQuiet(t *testing.T) {
	h := newHarness(t)
	h.tty, h.width = true, 100
	code, out, errs := h.run("index-pages.com")
	if code != 0 || !strings.Contains(out, "that's us") || strings.Contains(out, "Ahrefs") || !strings.Contains(out, "\x1b[") {
		t.Fatalf("card: %d %q", code, out)
	}
	if strings.Contains(errs, "Ahrefs") {
		t.Fatalf("card must not print anything on stderr: %q", errs)
	}
	h.env["NO_COLOR"] = "1"
	_, out, _ = h.run("aa.com", "bbb.com", "nodata.com")
	if strings.Contains(out, "\x1b[") || !strings.Contains(out, "3 domains") || !strings.Contains(out, "no data") {
		t.Fatalf("NO_COLOR list:\n%s", out)
	}
	h.env["TERM"] = "dumb"
	_, out, _ = h.run("aa.com")
	if strings.ContainsAny(out, "█░·") {
		t.Fatalf("TERM=dumb:\n%s", out)
	}
	h.tty = false
	_, _, errs = h.run("aa.com", "-q")
	if errs != "" {
		t.Fatalf("-q stderr %q", errs)
	}
}

func TestNoCache(t *testing.T) {
	h := newHarness(t)
	h.run("aa.com", "--no-cache", "-q")
	h.run("aa.com", "--no-cache", "-q")
	if _, err := os.Stat(h.env["GETDR_CACHE_PATH"]); !os.IsNotExist(err) {
		t.Fatal("--no-cache must not create the cache")
	}
	if h.fake.targets != 2 {
		t.Fatalf("Ahrefs saw %d targets", h.fake.targets)
	}
}

func TestRawTargets(t *testing.T) {
	h := newHarness(t)
	_, out, _ := h.run("--raw", "WWW.Abc.com", "-q")
	if out != "WWW.Abc.com\t30\n" {
		t.Fatalf("raw %q", out)
	}
}

func TestAPIMode(t *testing.T) {
	h := newHarness(t)
	var mu sync.Mutex
	var fresh []bool
	db, err := cache.Open(filepath.Join(h.dir, "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fetcher := lookupFunc(func(ts []string, version string, f bool) map[string]lookup.Outcome {
		mu.Lock()
		fresh = append(fresh, f)
		mu.Unlock()
		out := map[string]lookup.Outcome{}
		for _, t := range ts {
			v := 77.0
			if strings.HasPrefix(t, "nodata") {
				out[t] = lookup.Outcome{}
				continue
			}
			out[t] = lookup.Outcome{DR: &v}
		}
		return out
	})
	s := &server.Server{Engine: &lookup.Engine{Cache: db, Fetcher: fetcher, TTL: time.Hour, TTLNone: time.Hour},
		DefaultVersion: "dr3", HasKey: true, Token: "tok", MaxTargets: 100}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	h.env["GETDR_KEY"] = ""
	h.env["GETDR_API"] = srv.URL

	code, _, errs := h.run("a.com", "-q")
	if code != 2 || !strings.Contains(errs, "token") {
		t.Fatalf("missing token: %d %q", code, errs)
	}
	h.env["GETDR_SERVE_TOKEN"] = "tok"
	code, out, _ := h.run("a.com", "nodata.com", "--format", "json", "-q")
	if code != 0 || !strings.Contains(out, `{"target":"a.com","dr":77,"version":"dr3"`) || !strings.Contains(out, `"target":"nodata.com","dr":null`) {
		t.Fatalf("api json: %d %q", code, out)
	}
	if h.fake.requests != 0 {
		t.Fatal("api mode must not call Ahrefs")
	}
	h.run("a.com", "--fresh", "-q")
	if len(fresh) != 2 || fresh[0] || !fresh[1] {
		t.Fatalf("fresh forwarded: %v", fresh)
	}
	s.HasKey = false
	code, _, errs = h.run("b.com", "-q")
	if code != 3 || !strings.Contains(errs, "no Ahrefs API key") {
		t.Fatalf("server without key: %d %q", code, errs)
	}
	srv.Close()
	code, out, errs = h.run("c.com", "-q")
	if code != 2 || out != "c.com\t\n" || !strings.Contains(errs, "getdr server") {
		t.Fatalf("server down: %d %q %q", code, out, errs)
	}
}

type lookupFunc func([]string, string, bool) map[string]lookup.Outcome

func (f lookupFunc) Fetch(_ context.Context, ts []string, v string, fresh bool) map[string]lookup.Outcome {
	return f(ts, v, fresh)
}

func TestConfigShow(t *testing.T) {
	h := newHarness(t)
	cfgDir := filepath.Join(h.env["XDG_CONFIG_HOME"], "getdr")
	os.MkdirAll(cfgDir, 0o700)
	os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("[ahrefs]\nbatch = 500\n"), 0o600)
	code, out, _ := h.run("config", "show", "--ttl", "2d")
	if code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"****1234", "env GETDR_KEY", "ahrefs.batch", "500", "file", "cache.ttl", "2d", "flag", "version", "dr3", "default", "config file: " + filepath.Join(cfgDir, "config.toml")} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "good-key-1234") {
		t.Error("key must be masked")
	}
}

func TestCacheCommands(t *testing.T) {
	h := newHarness(t)
	if code, out, _ := h.run("cache", "stats"); code != 0 || !strings.Contains(out, "does not exist") {
		t.Fatalf("stats before: %d %q", code, out)
	}
	h.run("aa.com", "bb.com", "-q")
	db, _ := cache.Open(h.env["GETDR_CACHE_PATH"])
	old := time.Now().Add(-90 * 24 * time.Hour)
	v := 1.0
	db.Insert("dr3", []cache.Row{{Domain: "aa.com", DR: &v, CheckedAt: old}, {Domain: "zz.com", DR: &v, CheckedAt: old}})
	db.Close()
	code, out, _ := h.run("cache", "stats")
	if code != 0 || !strings.Contains(out, "checks") || !strings.Contains(out, "4") || !strings.Contains(out, "dr3 3") {
		t.Fatalf("stats: %d %q", code, out)
	}
	code, out, _ = h.run("cache", "purge")
	if code != 0 || !strings.Contains(out, "deleted 1 checks older than 30d") {
		t.Fatalf("purge: %d %q", code, out)
	}
	code, out, _ = h.run("cache", "clear")
	if code != 0 || !strings.Contains(out, "deleted 3 checks") {
		t.Fatalf("clear: %d %q", code, out)
	}
	if code, _, _ := h.run("cache", "wipe"); code != 2 {
		t.Fatal("unknown cache subcommand")
	}
}

func TestKeyCheck(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("key", "check", "-q")
	if code != 0 || !strings.Contains(out, "key ok") || !strings.Contains(out, "ahrefs.com DR 60") {
		t.Fatalf("key check: %d %q", code, out)
	}
	code, _, errs := h.run("key", "check", "--key", "bad")
	if code != 3 || !strings.Contains(errs, "#key") {
		t.Fatalf("bad key: %d %q", code, errs)
	}
	h.env["GETDR_KEY"] = ""
	if code, _, _ := h.run("key", "check"); code != 3 {
		t.Fatalf("no key: %d", code)
	}
}

func TestReadOnlyCacheWarns(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.run("aa.com", "-q"); code != 0 {
		t.Fatal(code)
	}
	path := h.env["GETDR_CACHE_PATH"]
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		os.Chmod(p, 0o444)
	}
	t.Cleanup(func() {
		for _, p := range []string{path, path + "-wal", path + "-shm"} {
			os.Chmod(p, 0o644)
		}
	})
	code, out, errs := h.run("bb.com", "cc.com")
	if code != 0 || out != "bb.com\t20\ncc.com\t20\n" || strings.Count(errs, "could not write the cache") != 1 {
		t.Fatalf("read-only cache: %d %q %q", code, out, errs)
	}
	if _, _, errs := h.run("dd.com", "-q"); strings.Contains(errs, "could not write") {
		t.Fatalf("-q silences the warning: %q", errs)
	}
}

func TestInvalidTargets(t *testing.T) {
	h := newHarness(t)
	code, out, errs := h.run("ahrefs.com", "exa mple.com", "localhost", "github.com", "-q")
	if code != 2 || out != "ahrefs.com\t60\nexa mple.com\t\nlocalhost\t\ngithub.com\t60\n" {
		t.Fatalf("code %d out %q err %q", code, out, errs)
	}
	if errs != "getdr: invalid target: exa mple.com\ngetdr: invalid target: localhost\n" {
		t.Fatalf("stderr %q", errs)
	}
	before := h.fake.requests
	code, out, _ = h.run("localhost", "a..com", "ahrefs.com", "--format", "json", "-q")
	want := `{"target":"localhost","dr":null,"version":"dr3","checked_at":null,"cached":false}` + "\n" +
		`{"target":"a..com","dr":null,"version":"dr3","checked_at":null,"cached":false}` + "\n" +
		`{"target":"ahrefs.com","dr":60,"version":"dr3"`
	if code != 2 || !strings.HasPrefix(out, want) {
		t.Fatalf("json %d %q", code, out)
	}
	// Invalid targets are never cached: localhost and a..com are sent again
	// (each rejection is one request), ahrefs.com comes from the cache.
	if h.fake.requests != before+2 {
		t.Fatalf("requests %d -> %d", before, h.fake.requests)
	}
	// Locally rejected targets are never sent.
	before = h.fake.requests
	code, _, errs = h.run("https://", "exa mple.com")
	if code != 2 || h.fake.requests != before || strings.Count(errs, "invalid target") != 2 {
		t.Fatalf("local reject: %d %d %q", code, h.fake.requests-before, errs)
	}
	h.tty, h.width = true, 100
	h.env["NO_COLOR"] = "1"
	_, out, _ = h.run("localhost", "ahrefs.com", "-q")
	if !strings.Contains(out, "invalid") || !strings.Contains(out, "1 invalid") {
		t.Fatalf("card:\n%s", out)
	}
}

func TestIDNTargets(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("xn--e1afmkfd.xn--p1ai", "ПРИМЕР.РФ", "-q")
	if code != 0 || out != "пример.рф\t120\n" {
		t.Fatalf("%d %q", code, out)
	}
}
