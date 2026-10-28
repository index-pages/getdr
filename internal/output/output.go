// Package output renders results as card, tsv, csv, json (NDJSON) or bare.
package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/index-pages/getdr/internal/lookup"
)

// Fields that --fields accepts.
var Fields = []string{"domain", "dr", "version", "checked_at", "delta", "cached"}

// ParseFields validates a comma-separated field list.
func ParseFields(s string) ([]string, error) {
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		ok := false
		for _, k := range Fields {
			ok = ok || k == f
		}
		if !ok {
			return nil, fmt.Errorf("unknown field %q (available: %s)", f, strings.Join(Fields, ","))
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty field list")
	}
	return out, nil
}

// Options control rendering.
type Options struct {
	Format string   // card, tsv, csv, json or bare
	Fields []string // tsv/csv columns; for json only "delta" matters
	Color  bool
	UTF8   bool
	Width  int  // terminal columns
	All    bool // card: no 200-row limit
	Above  float64
	MaxAge time.Duration // card: trend window
	Now    time.Time
	Loc    *time.Location
}

// SortByDR orders results by DR, highest first; no data and no response last.
func SortByDR(rs []lookup.Result) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if (a.DR == nil) != (b.DR == nil) {
			return a.DR != nil
		}
		if a.DR == nil {
			return a.Status < b.Status
		}
		return *a.DR > *b.DR
	})
}

// Write renders shown (already filtered and sorted). all is the complete
// result set, used for the card summary.
func Write(w io.Writer, shown, all []lookup.Result, o Options) error {
	switch o.Format {
	case "card":
		return writeCard(w, shown, all, o)
	case "tsv":
		return writeTSV(w, shown, o.Fields)
	case "csv":
		return writeCSV(w, shown, o.Fields)
	case "json":
		return writeJSON(w, shown, o.Fields)
	case "bare":
		return writeBare(w, shown)
	}
	return fmt.Errorf("unknown format %q", o.Format)
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func field(r lookup.Result, name string) string {
	switch name {
	case "domain":
		return r.Target
	case "dr":
		if r.DR != nil {
			return num(*r.DR)
		}
	case "version":
		return r.Version
	case "checked_at":
		if !r.CheckedAt.IsZero() {
			return r.CheckedAt.UTC().Format(time.RFC3339)
		}
	case "delta":
		if d := r.Delta(); d != nil {
			return num(*d)
		}
	case "cached":
		return strconv.FormatBool(r.Cached)
	}
	return ""
}

var tsvClean = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ")

func writeTSV(w io.Writer, rs []lookup.Result, fields []string) error {
	var b strings.Builder
	for _, r := range rs {
		for i, f := range fields {
			if i > 0 {
				b.WriteByte('\t')
			}
			b.WriteString(tsvClean.Replace(field(r, f)))
		}
		b.WriteByte('\n')
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeCSV(w io.Writer, rs []lookup.Result, fields []string) error {
	cw := csv.NewWriter(w)
	cw.Write(fields)
	row := make([]string, len(fields))
	for _, r := range rs {
		for i, f := range fields {
			row[i] = field(r, f)
		}
		cw.Write(row)
	}
	cw.Flush()
	return cw.Error()
}

func writeJSON(w io.Writer, rs []lookup.Result, fields []string) error {
	withDelta := false
	for _, f := range fields {
		withDelta = withDelta || f == "delta"
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, r := range rs {
		var v any = r.Record()
		if withDelta {
			v = lookup.RecordDelta{Record: r.Record(), Delta: r.Delta()}
		}
		if err := enc.Encode(v); err != nil {
			return err
		}
	}
	return nil
}

func writeBare(w io.Writer, rs []lookup.Result) error {
	var b strings.Builder
	for _, r := range rs {
		b.WriteString(field(r, "dr"))
		b.WriteByte('\n')
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// short formats a DR value for people: at most one decimal, and only when
// it is not zero (49, 61.3).
func short(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}
