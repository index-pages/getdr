// Package config merges flags, environment, config file and defaults.
//
// Priority: flag, then GETDR_* environment variables (and AHREFS_KEY for the
// key, below GETDR_KEY), then the config file, then built-in defaults.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the merged configuration.
type Config struct {
	Key     string
	API     string
	Version string

	CachePath string
	TTL       time.Duration
	TTLNone   time.Duration
	MaxAge    time.Duration

	Batch   int
	RPM     int
	Timeout time.Duration
	Retries int

	Listen     string
	Token      string
	MaxTargets int

	// File is the config file that was read, "" if none.
	File string
	// Sources maps a setting name (as in Names) to where its value came from:
	// "flag", "env NAME", "file" or "default".
	Sources map[string]string
}

// Names lists the settings in display order, using their config file names.
var Names = []string{
	"key", "api", "version",
	"cache.path", "cache.ttl", "cache.ttl_none", "cache.max_age",
	"ahrefs.batch", "ahrefs.rpm", "ahrefs.timeout", "ahrefs.retries",
	"serve.listen", "serve.token", "serve.max_targets",
}

// EnvName returns the environment variable for a setting name.
func EnvName(name string) string {
	return "GETDR_" + strings.ToUpper(strings.ReplaceAll(name, ".", "_"))
}

// Defaults returns the built-in default values as strings.
func Defaults(home, xdgCache string) map[string]string {
	cacheDir := xdgCache
	if cacheDir == "" && home != "" {
		cacheDir = filepath.Join(home, ".cache")
	}
	cachePath := ""
	if cacheDir != "" {
		cachePath = filepath.Join(cacheDir, "getdr", "cache.db")
	}
	return map[string]string{
		"key":               "",
		"api":               "",
		"version":           "dr3",
		"cache.path":        cachePath,
		"cache.ttl":         "7d",
		"cache.ttl_none":    "1d",
		"cache.max_age":     "30d",
		"ahrefs.batch":      "1000",
		"ahrefs.rpm":        "40",
		"ahrefs.timeout":    "30s",
		"ahrefs.retries":    "3",
		"serve.listen":      "127.0.0.1:8080",
		"serve.token":       "",
		"serve.max_targets": "10000",
	}
}

type fileConfig struct {
	Key     *string `toml:"key"`
	API     *string `toml:"api"`
	Version *string `toml:"version"`
	Cache   struct {
		Path    *string `toml:"path"`
		TTL     *string `toml:"ttl"`
		TTLNone *string `toml:"ttl_none"`
		MaxAge  *string `toml:"max_age"`
	} `toml:"cache"`
	Ahrefs struct {
		Batch   *int64  `toml:"batch"`
		RPM     *int64  `toml:"rpm"`
		Timeout *string `toml:"timeout"`
		Retries *int64  `toml:"retries"`
	} `toml:"ahrefs"`
	Serve struct {
		Listen     *string `toml:"listen"`
		Token      *string `toml:"token"`
		MaxTargets *int64  `toml:"max_targets"`
	} `toml:"serve"`
}

func (f *fileConfig) values() map[string]string {
	out := map[string]string{}
	str := func(name string, p *string) {
		if p != nil {
			out[name] = *p
		}
	}
	num := func(name string, p *int64) {
		if p != nil {
			out[name] = strconv.FormatInt(*p, 10)
		}
	}
	str("key", f.Key)
	str("api", f.API)
	str("version", f.Version)
	str("cache.path", f.Cache.Path)
	str("cache.ttl", f.Cache.TTL)
	str("cache.ttl_none", f.Cache.TTLNone)
	str("cache.max_age", f.Cache.MaxAge)
	num("ahrefs.batch", f.Ahrefs.Batch)
	num("ahrefs.rpm", f.Ahrefs.RPM)
	str("ahrefs.timeout", f.Ahrefs.Timeout)
	num("ahrefs.retries", f.Ahrefs.Retries)
	str("serve.listen", f.Serve.Listen)
	str("serve.token", f.Serve.Token)
	num("serve.max_targets", f.Serve.MaxTargets)
	return out
}

// Load merges the configuration. flags holds values given on the command
// line keyed by setting name; configFlag is the --config value ("" if not
// given); getenv reads the environment.
func Load(flags map[string]string, configFlag string, getenv func(string) string) (*Config, error) {
	home := getenv("HOME")
	if home == "" {
		home = getenv("USERPROFILE")
	}
	defaults := Defaults(home, getenv("XDG_CACHE_HOME"))

	path, explicit := configFlag, configFlag != ""
	if !explicit {
		if p := getenv("GETDR_CONFIG"); p != "" {
			path, explicit = p, true
		}
	}
	if !explicit {
		path = ""
		var candidates []string
		if x := getenv("XDG_CONFIG_HOME"); x != "" {
			candidates = append(candidates, filepath.Join(x, "getdr", "config.toml"))
		} else if home != "" {
			candidates = append(candidates, filepath.Join(home, ".config", "getdr", "config.toml"))
		}
		candidates = append(candidates, "/etc/getdr/config.toml")
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				path = c
				break
			}
		}
	}

	fileVals := map[string]string{}
	if path != "" {
		path = expandHome(path, home)
		var fc fileConfig
		md, err := toml.DecodeFile(path, &fc)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("config file %s does not exist", path)
			}
			return nil, fmt.Errorf("config file %s: %v", path, err)
		}
		if und := md.Undecoded(); len(und) > 0 {
			keys := make([]string, len(und))
			for i, k := range und {
				keys[i] = k.String()
			}
			return nil, fmt.Errorf("config file %s: unknown setting %s", path, strings.Join(keys, ", "))
		}
		fileVals = fc.values()
	}

	vals := map[string]string{}
	src := map[string]string{}
	for _, name := range Names {
		if v, ok := flags[name]; ok {
			vals[name], src[name] = v, "flag"
			continue
		}
		env := EnvName(name)
		if v := getenv(env); v != "" {
			vals[name], src[name] = v, "env "+env
			continue
		}
		if name == "key" {
			if v := getenv("AHREFS_KEY"); v != "" {
				vals[name], src[name] = v, "env AHREFS_KEY"
				continue
			}
		}
		if v, ok := fileVals[name]; ok && !(name == "key" && v == "") {
			vals[name], src[name] = v, "file"
			continue
		}
		vals[name], src[name] = defaults[name], "default"
	}

	c := &Config{File: path, Sources: src}
	var errs []string
	bad := func(name string, err error) {
		errs = append(errs, fmt.Sprintf("%s (from %s): %v", name, src[name], err))
	}
	c.Key = strings.TrimSpace(vals["key"])
	c.API = strings.TrimRight(strings.TrimSpace(vals["api"]), "/")
	if c.API != "" && !strings.HasPrefix(c.API, "http://") && !strings.HasPrefix(c.API, "https://") {
		bad("api", errors.New("must start with http:// or https://"))
	}
	c.Version = vals["version"]
	if c.Version != "dr3" && c.Version != "dr4" {
		bad("version", fmt.Errorf("%q is not dr3 or dr4", c.Version))
	}
	c.CachePath = expandHome(vals["cache.path"], home)
	durs := []struct {
		name string
		dst  *time.Duration
		zero bool
	}{
		{"cache.ttl", &c.TTL, true},
		{"cache.ttl_none", &c.TTLNone, true},
		{"cache.max_age", &c.MaxAge, false},
		{"ahrefs.timeout", &c.Timeout, false},
	}
	for _, d := range durs {
		v, err := ParseDuration(vals[d.name])
		if err == nil && v == 0 && !d.zero {
			err = errors.New("must be greater than zero")
		}
		if err != nil {
			bad(d.name, err)
		}
		*d.dst = v
	}
	ints := []struct {
		name     string
		dst      *int
		min, max int
	}{
		{"ahrefs.batch", &c.Batch, 1, 1000},
		{"ahrefs.rpm", &c.RPM, 1, 100000},
		{"ahrefs.retries", &c.Retries, 0, 20},
		{"serve.max_targets", &c.MaxTargets, 1, 10000000},
	}
	for _, n := range ints {
		v, err := strconv.Atoi(strings.TrimSpace(vals[n.name]))
		if err != nil {
			bad(n.name, fmt.Errorf("%q is not a number", vals[n.name]))
			continue
		}
		if v < n.min || v > n.max {
			bad(n.name, fmt.Errorf("%d is out of range %d..%d", v, n.min, n.max))
		}
		*n.dst = v
	}
	c.Listen = vals["serve.listen"]
	c.Token = vals["serve.token"]
	if len(errs) > 0 {
		return nil, errors.New("invalid configuration: " + strings.Join(errs, "; "))
	}
	return c, nil
}

// Display returns the value of a setting formatted for "config show";
// secrets are masked.
func (c *Config) Display(name string) string {
	switch name {
	case "key":
		return Mask(c.Key)
	case "api":
		return c.API
	case "version":
		return c.Version
	case "cache.path":
		return c.CachePath
	case "cache.ttl":
		return FormatDuration(c.TTL)
	case "cache.ttl_none":
		return FormatDuration(c.TTLNone)
	case "cache.max_age":
		return FormatDuration(c.MaxAge)
	case "ahrefs.batch":
		return strconv.Itoa(c.Batch)
	case "ahrefs.rpm":
		return strconv.Itoa(c.RPM)
	case "ahrefs.timeout":
		return FormatDuration(c.Timeout)
	case "ahrefs.retries":
		return strconv.Itoa(c.Retries)
	case "serve.listen":
		return c.Listen
	case "serve.token":
		return Mask(c.Token)
	case "serve.max_targets":
		return strconv.Itoa(c.MaxTargets)
	}
	return ""
}

// Mask hides a secret, keeping only its last 4 characters.
func Mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

func expandHome(p, home string) string {
	if home != "" && (p == "~" || strings.HasPrefix(p, "~/")) {
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

// ParseDuration accepts Go durations ("90m", "36h") and whole or fractional
// days with a "d" suffix ("7d", "1.5d"). Negative values are rejected.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty duration")
	}
	var d time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		f, err := strconv.ParseFloat(days, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		d = time.Duration(f * float64(24*time.Hour))
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("invalid duration %q (use e.g. 7d, 12h, 30m)", s)
		}
	}
	if d < 0 {
		return 0, fmt.Errorf("negative duration %q", s)
	}
	return d, nil
}

// FormatDuration prints whole days as "7d" and everything else as a Go duration.
func FormatDuration(d time.Duration) string {
	day := 24 * time.Hour
	if d > 0 && d%day == 0 {
		return strconv.FormatInt(int64(d/day), 10) + "d"
	}
	return d.String()
}
