// Package cli implements the getdr command line.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/index-pages/getdr/internal/ahrefs"
	"github.com/index-pages/getdr/internal/buildinfo"
	"github.com/index-pages/getdr/internal/cache"
	"github.com/index-pages/getdr/internal/config"
	"github.com/index-pages/getdr/internal/lookup"
	"github.com/index-pages/getdr/internal/output"
	"github.com/index-pages/getdr/internal/remote"
	"github.com/index-pages/getdr/internal/target"
)

// Exit codes.
const (
	exitOK     = 0
	exitFilter = 1
	exitError  = 2
	exitKey    = 3
)

// Env is the process environment the CLI runs in.
type Env struct {
	Stdin                          io.Reader
	Stdout, Stderr                 io.Writer
	Getenv                         func(string) string
	StdinTTY, StdoutTTY, StderrTTY bool
	Width                          int // terminal columns, 0 = unknown
	Now                            func() time.Time
}

// ahrefsEndpoint is the Ahrefs URL; tests point it at a mock.
var ahrefsEndpoint = ahrefs.Endpoint

type runner struct {
	env   *Env
	a     *args
	cfg   *config.Config
	quiet bool
	verb  bool
}

func (r *runner) errf(format string, args ...any) {
	fmt.Fprintf(r.env.Stderr, "getdr: "+format+"\n", args...)
}

func (r *runner) logf(format string, args ...any) {
	if r.verb {
		fmt.Fprintf(r.env.Stderr, "getdr: "+format+"\n", args...)
	}
}

func (r *runner) now() time.Time {
	if r.env.Now != nil {
		return r.env.Now()
	}
	return time.Now()
}

// Run executes getdr with the given arguments and returns the exit code.
func Run(argv []string, env *Env) int {
	if env.Getenv == nil {
		env.Getenv = os.Getenv
	}
	r := &runner{env: env}
	a, err := parseArgs(argv)
	if err != nil {
		r.errf("%v", err)
		return exitError
	}
	r.a = a
	r.quiet, r.verb = a.has("quiet"), a.has("verbose")
	if a.has("help") {
		io.WriteString(env.Stdout, usage)
		return exitOK
	}
	if a.has("V") {
		fmt.Fprintf(env.Stdout, "getdr %s\n", buildinfo.Version)
		return exitOK
	}

	flags := map[string]string{}
	for flag, name := range map[string]string{
		"key": "key", "api": "api", "version": "version", "ttl": "cache.ttl",
		"listen": "serve.listen", "token": "serve.token",
	} {
		if a.has(flag) {
			flags[name] = a.str(flag)
		}
	}
	cfg, err := config.Load(flags, a.str("config"), env.Getenv)
	if err != nil {
		r.errf("%v", err)
		return exitError
	}
	r.cfg = cfg

	cmd := ""
	if len(a.pos) > 0 {
		switch a.pos[0] {
		case "serve", "cache", "config", "key":
			cmd = a.pos[0]
		}
	}
	if cmd != "serve" && a.has("listen") {
		r.errf("--listen is only used by getdr serve")
		return exitError
	}
	switch cmd {
	case "serve":
		if len(a.pos) > 1 {
			r.errf("serve takes no arguments")
			return exitError
		}
		return r.serve()
	case "cache":
		return r.cacheCmd()
	case "config":
		if len(a.pos) != 2 || a.pos[1] != "show" {
			r.errf("usage: getdr config show")
			return exitError
		}
		return r.configShow()
	case "key":
		if len(a.pos) != 2 || a.pos[1] != "check" {
			r.errf("usage: getdr key check")
			return exitError
		}
		return r.keyCheck()
	}
	return r.check()
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func (r *runner) ahrefsClient() *ahrefs.Client {
	c := &ahrefs.Client{
		Endpoint: ahrefsEndpoint,
		Key:      r.cfg.Key,
		Batch:    r.cfg.Batch,
		RPM:      r.cfg.RPM,
		Timeout:  r.cfg.Timeout,
		Retries:  r.cfg.Retries,
		Log:      r.logf,
	}
	return c
}

func (r *runner) keyMessage(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "no key set") {
		return "no Ahrefs API key: set GETDR_KEY or AHREFS_KEY, or pass --key. Get a free key: " + buildinfo.KeyHelp
	}
	msg = strings.TrimPrefix(msg, lookup.ErrKey.Error()+": ")
	if !strings.Contains(msg, buildinfo.KeyHelp) {
		msg += "; see " + buildinfo.KeyHelp
	}
	return msg
}

// readTargets collects targets from arguments, -f and stdin.
func (r *runner) readTargets() ([]string, error) {
	var in []string
	for _, p := range r.a.pos {
		if p == "-" {
			return nil, errors.New(`use -f - to read targets from stdin`)
		}
		in = append(in, p)
	}
	if r.a.has("file") {
		name := r.a.str("file")
		var rd io.Reader
		if name == "-" {
			rd = r.env.Stdin
		} else {
			f, err := os.Open(name)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			rd = f
		}
		list, err := target.ReadList(rd)
		if err != nil {
			return nil, err
		}
		in = append(in, list...)
	} else if len(in) == 0 && !r.env.StdinTTY && r.env.Stdin != nil {
		list, err := target.ReadList(r.env.Stdin)
		if err != nil {
			return nil, err
		}
		in = list
	}
	return in, nil
}

func utf8Locale(getenv func(string) string) bool {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := strings.ToLower(getenv(k)); v != "" {
			return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
		}
	}
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

func (r *runner) check() int {
	a, env, cfg := r.a, r.env, r.cfg
	raw := a.has("raw")
	in, err := r.readTargets()
	if err != nil {
		r.errf("%v", err)
		return exitError
	}
	if len(in) == 0 {
		if len(a.pos) == 0 && !a.has("file") && env.StdinTTY {
			io.WriteString(env.Stderr, usage)
		} else {
			r.errf("no targets")
		}
		return exitError
	}
	targets := target.Prepare(in, raw)
	if len(targets) == 0 {
		r.errf("no valid targets")
		return exitError
	}

	// Output destination and format.
	out, outTTY := env.Stdout, env.StdoutTTY
	if a.has("out") && a.str("out") != "-" {
		f, err := os.Create(a.str("out"))
		if err != nil {
			r.errf("%v", err)
			return exitError
		}
		defer f.Close()
		out, outTTY = f, false
	}
	format := "tsv"
	if outTTY {
		format = "card"
	}
	if a.has("format") {
		format = a.str("format")
		switch format {
		case "card", "tsv", "csv", "json":
		default:
			r.errf("--format must be card, tsv, csv or json")
			return exitError
		}
		if a.has("bare") {
			r.errf("use either --format or -b")
			return exitError
		}
	}
	if a.has("bare") {
		format = "bare"
	}
	fields := []string{"domain", "dr"}
	if a.has("fields") {
		if fields, err = output.ParseFields(a.str("fields")); err != nil {
			r.errf("--fields: %v", err)
			return exitError
		}
	}
	minV, hasMin, err := a.float("min")
	if err != nil {
		r.errf("%v", err)
		return exitError
	}
	maxV, hasMax, err := a.float("max")
	if err != nil {
		r.errf("%v", err)
		return exitError
	}

	// Engine.
	warned := false
	eng := &lookup.Engine{TTL: cfg.TTL, TTLNone: cfg.TTLNone, Now: env.Now, Log: r.logf,
		Warn: func(format string, args ...any) {
			if !warned && !r.quiet {
				warned = true
				r.errf(format, args...)
			}
		}}
	if !a.has("no-cache") {
		db, err := cache.Open(cfg.CachePath)
		if err != nil {
			r.errf("%v (use --no-cache to run without it)", err)
			return exitError
		}
		defer db.Close()
		eng.Cache = db
	}
	if cfg.API != "" {
		batches := (len(targets) + 999) / 1000
		eng.Fetcher = &remote.Client{
			URL: cfg.API, Token: cfg.Token, Raw: raw, Log: r.logf,
			Timeout: cfg.Timeout*time.Duration((cfg.Retries+1)*batches) + 30*time.Second,
		}
	} else {
		c := r.ahrefsClient()
		if env.StderrTTY && !r.quiet && !r.verb {
			drawn := false
			c.Progress = func(batch, total int) {
				if total > 1 {
					drawn = true
					fmt.Fprintf(env.Stderr, "\r\x1b[Kgetdr: fetching batch %d/%d…", batch, total)
				}
			}
			defer func() {
				if drawn {
					fmt.Fprint(env.Stderr, "\r\x1b[K")
				}
			}()
		}
		eng.Fetcher = c
	}

	ctx, cancel := signalContext()
	defer cancel()
	results, err := eng.Lookup(ctx, targets, cfg.Version, a.has("fresh"))
	if err != nil {
		if errors.Is(err, lookup.ErrKey) {
			r.errf("%s", r.keyMessage(err))
			return exitKey
		}
		r.errf("%v", err)
		return exitError
	}

	// Report targets without a response, grouped by reason.
	code := exitOK
	var reasons []string
	byReason := map[string][]string{}
	for _, res := range results {
		if res.Status == lookup.Invalid {
			code = exitError
			r.errf("invalid target: %s", res.Target)
			if res.Err != nil && res.Err != lookup.ErrInvalid {
				r.logf("%s: %v", res.Target, res.Err)
			}
			continue
		}
		if res.Status != lookup.NoResponse {
			continue
		}
		code = exitError
		msg := "no response"
		if res.Err != nil {
			msg = res.Err.Error()
		}
		if _, ok := byReason[msg]; !ok {
			reasons = append(reasons, msg)
		}
		byReason[msg] = append(byReason[msg], res.Target)
	}
	for _, msg := range reasons {
		ts := byReason[msg]
		list := strings.Join(ts, ", ")
		if len(ts) > 10 {
			list = strings.Join(ts[:10], ", ") + fmt.Sprintf(" and %d more", len(ts)-10)
		}
		r.errf("no response for %d target(s): %s: %s", len(ts), msg, list)
	}

	shown := results
	if hasMin || hasMax {
		shown = nil
		for _, res := range results {
			ok := res.DR != nil && (!hasMin || *res.DR >= minV) && (!hasMax || *res.DR <= maxV)
			if ok {
				shown = append(shown, res)
			} else if code == exitOK {
				code = exitFilter
			}
		}
	}
	if format == "card" || a.has("sort") {
		shown = append([]lookup.Result(nil), shown...)
		output.SortByDR(shown)
	}
	above := 50.0
	if hasMin {
		above = minV
	}
	width := env.Width
	if width <= 0 {
		width, _ = strconv.Atoi(env.Getenv("COLUMNS"))
	}
	if width <= 0 {
		width = 80
	}
	term := env.Getenv("TERM")
	opt := output.Options{
		Format: format,
		Fields: fields,
		Color:  outTTY && env.Getenv("NO_COLOR") == "" && term != "dumb",
		UTF8:   term != "dumb" && utf8Locale(env.Getenv),
		Width:  width,
		All:    a.has("all"),
		Above:  above,
		MaxAge: cfg.MaxAge,
		Now:    r.now(),
	}
	if err := output.Write(out, shown, results, opt); err != nil {
		r.errf("%v", err)
		return exitError
	}
	if format != "card" && !r.quiet {
		fmt.Fprintln(env.Stderr, buildinfo.Attribution)
	}
	return code
}
