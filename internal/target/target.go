// Package target turns user input into the list of targets to check.
package target

import (
	"bufio"
	"io"
	"net"
	"net/url"
	"strings"
)

// Normalize reduces a domain or URL to its bare host: the scheme, userinfo,
// port, path, query and fragment are dropped, a leading "www." and a trailing
// dot are removed and the result is lower-cased. It returns "" for input that
// holds no host.
func Normalize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "://"); i >= 0 {
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			s = u.Host
		} else {
			s = s[i+3:]
		}
	} else if strings.HasPrefix(s, "//") {
		s = s[2:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	s = strings.TrimSuffix(strings.ToLower(s), ".")
	s = strings.TrimPrefix(s, "www.")
	return s
}

// Prepare normalizes (unless raw) and de-duplicates targets, keeping the
// order of first appearance. Empty results are dropped.
func Prepare(in []string, raw bool) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if raw {
			s = strings.TrimSpace(s)
		} else {
			s = Normalize(s)
		}
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// ReadList reads one target per line. Blank lines and lines starting with #
// are ignored.
func ReadList(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}
