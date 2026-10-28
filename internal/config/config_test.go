package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	home := t.TempDir()
	c, err := Load(nil, "", envFrom(map[string]string{"HOME": home}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != "dr3" || c.TTL != 7*24*time.Hour || c.TTLNone != 24*time.Hour || c.MaxAge != 30*24*time.Hour {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.Batch != 1000 || c.RPM != 40 || c.Retries != 3 || c.Timeout != 30*time.Second || c.MaxTargets != 10000 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if want := filepath.Join(home, ".cache", "getdr", "cache.db"); c.CachePath != want {
		t.Fatalf("cache path %q, want %q", c.CachePath, want)
	}
	if c.Listen != "127.0.0.1:8080" || c.File != "" {
		t.Fatalf("listen %q file %q", c.Listen, c.File)
	}
	for _, n := range Names {
		if c.Sources[n] != "default" {
			t.Errorf("%s source %q", n, c.Sources[n])
		}
	}
}

func TestPriority(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.toml")
	err := os.WriteFile(file, []byte(`
key = "file-key-123456789"
version = "dr4"
[cache]
path = "~/cache.db"
ttl = "2d"
[ahrefs]
rpm = 10
[serve]
max_targets = 5
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"HOME":              dir,
		"GETDR_CONFIG":      file,
		"AHREFS_KEY":        "ahrefs-env-key-1",
		"GETDR_AHREFS_RPM":  "20",
		"GETDR_CACHE_TTL":   "36h",
		"XDG_CONFIG_HOME":   "/nonexistent",
		"GETDR_SERVE_TOKEN": "",
	}
	c, err := Load(map[string]string{"cache.ttl": "3d"}, "", envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct{ name, val, src string }{
		{"key", "****ey-1", "env AHREFS_KEY"},
		{"version", "dr4", "file"},
		{"cache.path", filepath.Join(dir, "cache.db"), "file"},
		{"cache.ttl", "3d", "flag"},
		{"ahrefs.rpm", "20", "env GETDR_AHREFS_RPM"},
		{"serve.max_targets", "5", "file"},
		{"serve.token", "", "default"},
	}
	for _, ch := range checks {
		if got := c.Display(ch.name); got != ch.val {
			t.Errorf("%s = %q, want %q", ch.name, got, ch.val)
		}
		if c.Sources[ch.name] != ch.src {
			t.Errorf("%s source %q, want %q", ch.name, c.Sources[ch.name], ch.src)
		}
	}
	if c.File != file {
		t.Errorf("file %q", c.File)
	}

	env["GETDR_KEY"] = "getdr-env-key-2"
	c, err = Load(nil, file, envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if c.Key != "getdr-env-key-2" || c.Sources["key"] != "env GETDR_KEY" {
		t.Errorf("GETDR_KEY must beat AHREFS_KEY: %q %q", c.Key, c.Sources["key"])
	}
	c, err = Load(map[string]string{"key": "flag-key"}, file, envFrom(env))
	if err != nil {
		t.Fatal(err)
	}
	if c.Key != "flag-key" || c.Sources["key"] != "flag" {
		t.Errorf("flag must win: %q %q", c.Key, c.Sources["key"])
	}
}

func TestXDGConfigFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "getdr"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "getdr", "config.toml"), []byte(`api = "http://lan:8080/"`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(nil, "", envFrom(map[string]string{"XDG_CONFIG_HOME": dir, "HOME": dir}))
	if err != nil {
		t.Fatal(err)
	}
	if c.API != "http://lan:8080" || c.Sources["api"] != "file" {
		t.Fatalf("api %q source %q", c.API, c.Sources["api"])
	}
}

func TestErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(nil, filepath.Join(dir, "missing.toml"), envFrom(nil)); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing explicit config: %v", err)
	}
	bad := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(bad, []byte("[cache]\nttll = \"1d\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(nil, bad, envFrom(nil)); err == nil || !strings.Contains(err.Error(), "cache.ttll") {
		t.Errorf("unknown key: %v", err)
	}
	for name, val := range map[string]string{
		"version": "dr5", "cache.ttl": "soon", "ahrefs.batch": "2000",
		"ahrefs.rpm": "0", "api": "lan:8080", "ahrefs.timeout": "0s",
	} {
		if _, err := Load(map[string]string{name: val}, "", envFrom(nil)); err == nil {
			t.Errorf("%s=%q accepted", name, val)
		}
	}
}

func TestParseDuration(t *testing.T) {
	good := map[string]time.Duration{
		"7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "1.5d": 36 * time.Hour,
		"12h": 12 * time.Hour, "90m": 90 * time.Minute, "0": 0, "0d": 0,
	}
	for in, want := range good {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "d", "7days", "-1d", "-5m", "abc"} {
		if _, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) accepted", in)
		}
	}
	if FormatDuration(7*24*time.Hour) != "7d" || FormatDuration(36*time.Hour) != "36h0m0s" {
		t.Error("FormatDuration")
	}
}

func TestMask(t *testing.T) {
	if Mask("") != "" || Mask("short") != "****" || Mask("abcdefghijkl") != "****ijkl" {
		t.Error("Mask")
	}
}
