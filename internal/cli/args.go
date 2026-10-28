package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// flagSpec describes one command-line flag.
type flagSpec struct {
	long, short string
	value       bool // takes an argument
}

var flagSpecs = []flagSpec{
	{"file", "f", true},
	{"out", "o", true},
	{"format", "", true},
	{"bare", "b", false},
	{"fields", "", true},
	{"sort", "", false},
	{"all", "", false},
	{"min", "", true},
	{"max", "", true},
	{"raw", "", false},
	{"version", "", true},
	{"fresh", "", false},
	{"no-cache", "", false},
	{"ttl", "", true},
	{"api", "", true},
	{"key", "", true},
	{"config", "", true},
	{"listen", "", true},
	{"token", "", true},
	{"quiet", "q", false},
	{"verbose", "v", false},
	{"", "V", false},
	{"help", "h", false},
}

// args is the parsed command line. Flags may appear before, between or
// after positional arguments; "--" ends flag parsing.
type args struct {
	vals map[string]string // value flags by long name
	set  map[string]bool   // every flag given, by long name ("V" for -V)
	pos  []string
}

func (a *args) has(name string) bool   { return a.set[name] }
func (a *args) str(name string) string { return a.vals[name] }

func (a *args) float(name string) (float64, bool, error) {
	if !a.set[name] {
		return 0, false, nil
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(a.vals[name]), 64)
	if err != nil {
		return 0, true, fmt.Errorf("--%s: %q is not a number", name, a.vals[name])
	}
	return v, true, nil
}

func parseArgs(in []string) (*args, error) {
	a := &args{vals: map[string]string{}, set: map[string]bool{}}
	byLong := map[string]flagSpec{}
	byShort := map[string]flagSpec{}
	for _, f := range flagSpecs {
		if f.long != "" {
			byLong[f.long] = f
		}
		if f.short != "" {
			byShort[f.short] = f
		}
	}
	key := func(f flagSpec) string {
		if f.long != "" {
			return f.long
		}
		return f.short
	}
	for i := 0; i < len(in); i++ {
		s := in[i]
		switch {
		case s == "--":
			a.pos = append(a.pos, in[i+1:]...)
			return a, nil
		case strings.HasPrefix(s, "--"):
			name, val, hasVal := strings.Cut(s[2:], "=")
			f, ok := byLong[name]
			if !ok {
				return nil, fmt.Errorf("unknown flag --%s (see getdr --help)", name)
			}
			if !f.value {
				if hasVal {
					return nil, fmt.Errorf("--%s takes no value", name)
				}
				a.set[name] = true
				continue
			}
			if !hasVal {
				if i+1 >= len(in) {
					if name == "version" {
						return nil, fmt.Errorf("--version needs dr3 or dr4 (the DR version); -V prints the getdr version")
					}
					return nil, fmt.Errorf("--%s needs a value", name)
				}
				i++
				val = in[i]
			}
			a.set[name], a.vals[name] = true, val
		case strings.HasPrefix(s, "-") && len(s) > 1:
			// Short flags: -q, -qv, -f FILE, -fFILE.
			for j := 1; j < len(s); j++ {
				f, ok := byShort[s[j:j+1]]
				if !ok {
					return nil, fmt.Errorf("unknown flag -%s (see getdr --help)", s[j:j+1])
				}
				k := key(f)
				if !f.value {
					a.set[k] = true
					continue
				}
				val := s[j+1:]
				if val == "" {
					if i+1 >= len(in) {
						return nil, fmt.Errorf("-%s needs a value", f.short)
					}
					i++
					val = in[i]
				}
				a.set[k], a.vals[k] = true, val
				break
			}
		default:
			a.pos = append(a.pos, s)
		}
	}
	return a, nil
}

const usage = `getdr – check Ahrefs Domain Rating (DR) for domains.

Usage:
  getdr [flags] <domain|url>...
  getdr [flags] -f FILE          one target per line, # comments; "-" = stdin
  cat list.txt | getdr [flags]
  getdr serve [--listen ADDR] [--token T]
  getdr cache stats | clear | purge
  getdr config show
  getdr key check

Flags:
  -f, --file FILE        read targets from FILE ("-" for stdin)
  -o, --out FILE         write the result to FILE instead of stdout
      --format F         card, tsv, csv or json (NDJSON); default: card on a
                         terminal, tsv otherwise
  -b, --bare             only the DR values, one per line, empty for no data
      --fields LIST      tsv/csv columns: domain,dr,version,checked_at,delta,cached
                         (default domain,dr); json always has the full record
                         and adds delta when listed
      --sort             sort by DR, highest first (card always sorts)
      --all              card: show all rows, not only the top 200
      --min N, --max N   keep only DR >= N / <= N; exit 1 if any target fails
      --raw              send targets as given, without normalization
      --version V        DR version: dr3 (default) or dr4
      --fresh            ignore the cache, refetch and update it
      --no-cache         neither read nor write the cache
      --ttl DUR          cache TTL for this run (7d, 12h, 30m)
      --api URL          use a getdr server instead of Ahrefs
      --key KEY          Ahrefs API key (default: GETDR_KEY, AHREFS_KEY)
      --config PATH      config file
      --listen ADDR      serve: listen address (default 127.0.0.1:8080)
      --token T          serve: required bearer token; with --api: token to send
  -q, --quiet            no progress, attribution or warnings on stderr
  -v, --verbose          show cache hits, API calls and batches on stderr
  -V                     print the getdr version
  -h, --help             show this help

Exit codes: 0 ok, 1 a target failed --min/--max, 2 error or targets without
a response, 3 Ahrefs API key missing or rejected.

Domain Rating by Ahrefs – https://ahrefs.com/
Docs: https://index-pages.com/getdr
`
