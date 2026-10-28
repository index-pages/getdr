package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/index-pages/getdr/internal/buildinfo"
	"github.com/index-pages/getdr/internal/cache"
	"github.com/index-pages/getdr/internal/config"
	"github.com/index-pages/getdr/internal/lookup"
	"github.com/index-pages/getdr/internal/server"
)

func (r *runner) serve() int {
	cfg := r.cfg
	db, err := cache.Open(cfg.CachePath)
	if err != nil {
		r.errf("%v", err)
		return exitError
	}
	defer db.Close()
	client := r.ahrefsClient()
	eng := &lookup.Engine{Cache: db, Fetcher: client, TTL: cfg.TTL, TTLNone: cfg.TTLNone, Now: r.env.Now, Log: r.logf,
		Warn: func(format string, args ...any) { r.errf("serve: "+format, args...) }}
	s := &server.Server{
		Engine:         eng,
		DefaultVersion: cfg.Version,
		HasKey:         cfg.Key != "",
		Token:          cfg.Token,
		MaxTargets:     cfg.MaxTargets,
		Cache:          db,
		Requests:       client.RequestsLastHour,
		Log:            r.logf,
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		r.errf("serve: %v", err)
		return exitError
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	r.errf("serve: listening on http://%s (cache %s)", ln.Addr(), db.Path())
	if cfg.Key == "" {
		r.errf("serve: no Ahrefs API key set, /dr will answer 503; see %s", buildinfo.KeyHelp)
	}
	if cfg.Token == "" {
		if host, _, _ := net.SplitHostPort(cfg.Listen); host != "127.0.0.1" && host != "localhost" && host != "::1" {
			r.errf("serve: no token set, anyone who can reach %s can use it", cfg.Listen)
		}
	}
	if cfg.API != "" {
		r.errf("serve: the api setting is ignored, serve always talks to Ahrefs")
	}

	ctx, cancel := signalContext()
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		r.errf("serve: %v", err)
		return exitError
	case <-ctx.Done():
	}
	shut, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	srv.Shutdown(shut)
	r.errf("serve: stopped")
	return exitOK
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func (r *runner) cacheCmd() int {
	a, cfg, out := r.a, r.cfg, r.env.Stdout
	if len(a.pos) != 2 {
		r.errf("usage: getdr cache stats | clear | purge")
		return exitError
	}
	sub := a.pos[1]
	switch sub {
	case "stats", "clear", "purge":
	default:
		r.errf("usage: getdr cache stats | clear | purge")
		return exitError
	}
	if _, err := os.Stat(cfg.CachePath); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(out, "cache %s does not exist yet\n", cfg.CachePath)
		return exitOK
	}
	db, err := cache.Open(cfg.CachePath)
	if err != nil {
		r.errf("%v", err)
		return exitError
	}
	defer db.Close()
	switch sub {
	case "stats":
		st, err := db.Stats()
		if err != nil {
			r.errf("%v", err)
			return exitError
		}
		tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
		fmt.Fprintf(tw, "path\t%s\n", st.Path)
		fmt.Fprintf(tw, "size\t%s\n", humanBytes(st.SizeBytes))
		fmt.Fprintf(tw, "checks\t%d\n", st.Rows)
		var vs []string
		for _, v := range []string{"dr3", "dr4"} {
			if n, ok := st.Versions[v]; ok {
				vs = append(vs, fmt.Sprintf("%s %d", v, n))
			}
		}
		dom := fmt.Sprintf("%d", st.Domains)
		if len(vs) > 0 {
			dom += " (" + strings.Join(vs, ", ") + ")"
		}
		fmt.Fprintf(tw, "domains\t%s\n", dom)
		if st.Oldest != nil {
			fmt.Fprintf(tw, "oldest\t%s\n", st.Oldest.Format(time.RFC3339))
			fmt.Fprintf(tw, "newest\t%s\n", st.Newest.Format(time.RFC3339))
		}
		fmt.Fprintf(tw, "ttl\t%s (no data: %s), max_age %s\n",
			config.FormatDuration(cfg.TTL), config.FormatDuration(cfg.TTLNone), config.FormatDuration(cfg.MaxAge))
		tw.Flush()
	case "clear":
		n, err := db.Clear()
		if err != nil {
			r.errf("%v", err)
			return exitError
		}
		fmt.Fprintf(out, "deleted %d checks\n", n)
	case "purge":
		n, err := db.Purge(cfg.MaxAge, r.now())
		if err != nil {
			r.errf("%v", err)
			return exitError
		}
		fmt.Fprintf(out, "deleted %d checks older than %s (the latest check of every domain is kept)\n", n, config.FormatDuration(cfg.MaxAge))
	}
	return exitOK
}

func (r *runner) configShow() int {
	cfg := r.cfg
	tw := tabwriter.NewWriter(r.env.Stdout, 0, 0, 3, ' ', 0)
	for _, name := range config.Names {
		v := cfg.Display(name)
		if v == "" {
			v = `""`
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", name, v, cfg.Sources[name])
	}
	tw.Flush()
	file := cfg.File
	if file == "" {
		file = "none"
	}
	fmt.Fprintf(r.env.Stdout, "\nconfig file: %s\n", file)
	return exitOK
}

func (r *runner) keyCheck() int {
	c := r.ahrefsClient()
	ctx, cancel := signalContext()
	defer cancel()
	out := c.Fetch(ctx, []string{"ahrefs.com"}, r.cfg.Version, false)["ahrefs.com"]
	if out.NoResponse {
		if errors.Is(out.Err, lookup.ErrKey) {
			r.errf("%s", r.keyMessage(out.Err))
			return exitKey
		}
		r.errf("could not reach Ahrefs: %v", out.Err)
		return exitError
	}
	dr := "no data"
	if out.DR != nil {
		dr = "DR " + fmt.Sprint(*out.DR)
	}
	fmt.Fprintf(r.env.Stdout, "key ok (%s): Ahrefs answered, ahrefs.com %s (%s)\n", r.cfg.Sources["key"], dr, r.cfg.Version)
	if !r.quiet {
		fmt.Fprintln(r.env.Stderr, buildinfo.Attribution)
	}
	return exitOK
}
