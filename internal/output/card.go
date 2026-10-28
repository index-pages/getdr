package output

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/index-pages/getdr/internal/lookup"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"
	ansiGray   = "\x1b[90m"

	maxRows = 200
)

// painter applies colors and picks UTF-8 or ASCII symbols.
type painter struct {
	color, utf8 bool
}

func (p painter) paint(code, s string) string {
	if !p.color || s == "" {
		return s
	}
	return code + s + ansiReset
}

func (p painter) sym(utf, ascii string) string {
	if p.utf8 {
		return utf
	}
	return ascii
}

// band is the color of a DR value: 0–19 gray, 20–39 yellow, 40–69 green, 70+ blue.
func band(v float64) string {
	switch {
	case v >= 70:
		return ansiBlue
	case v >= 40:
		return ansiGreen
	case v >= 20:
		return ansiYellow
	}
	return ansiGray
}

// bar draws cells cells; the fill uses half cells, rounded down.
func (p painter) bar(v float64, cells int) string {
	v = math.Max(0, math.Min(100, v))
	halves := int(math.Floor(v / 100 * float64(cells) * 2))
	full, half := halves/2, halves%2
	fill := strings.Repeat("█", full) + strings.Repeat("▌", half)
	return p.paint(band(v), fill) + p.paint(ansiDim, strings.Repeat("░", cells-full-half))
}

func emptyBar(p painter, cells int) string { return p.paint(ansiDim, strings.Repeat("░", cells)) }

const sparks = "▁▂▃▄▅▆▇█"

func sparkline(vals []float64, n int) string {
	if len(vals) > n {
		vals = vals[len(vals)-n:]
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range vals {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	levels := []rune(sparks)
	if len(vals) == 1 {
		return string(levels[0])
	}
	var b strings.Builder
	for _, v := range vals {
		i := 2
		if hi > lo {
			i = int(math.Round((v - lo) / (hi - lo) * 7))
		}
		b.WriteRune(levels[i])
	}
	return b.String()
}

// trend holds the values of the checks inside the max_age window.
type trend struct {
	vals   []float64
	since  time.Time
	checks int // all stored checks, inside the window or not
}

func trendOf(r lookup.Result, o Options) trend {
	t := trend{checks: len(r.History)}
	cut := o.Now.Add(-o.MaxAge)
	for _, c := range r.History {
		if c.DR == nil || c.CheckedAt.Before(cut) {
			continue
		}
		if len(t.vals) == 0 {
			t.since = c.CheckedAt
		}
		t.vals = append(t.vals, *c.DR)
	}
	return t
}

func (p painter) delta(d float64) string {
	switch {
	case math.Abs(d) < 0.05:
		return p.sym("±0", "+-0")
	case d > 0:
		return p.paint(ansiGreen, "+"+short(d))
	}
	return p.paint(ansiRed, "-"+short(-d))
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return plural(int(d/time.Hour), "hour") + " ago"
	}
	return plural(int(d/(24*time.Hour)), "day") + " ago"
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func width(s string) int { return utf8.RuneCountInString(s) }

func pad(s string, n int) string {
	if w := width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

// truncMiddle shortens s to n runes, keeping the end (the TLD) longer.
func truncMiddle(s string, n int, ell string) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	el := utf8.RuneCountInString(ell)
	if n <= el+1 {
		return string(r[:n])
	}
	head := (n - el) * 4 / 9
	tail := n - el - head
	return string(r[:head]) + ell + string(r[len(r)-tail:])
}

func egg(target string, p painter) string {
	switch target {
	case "index-pages.com":
		return "that's us" + p.sym(" 👋", "")
	case "ahrefs.com":
		return "thanks for the free API"
	}
	return ""
}

func footer(p painter) string {
	return "  " + p.paint(ansiDim, "Domain Rating by Ahrefs "+p.sym("·", "-")+" getdr by Index Pages") + "\n"
}

func writeCard(w io.Writer, shown, all []lookup.Result, o Options) error {
	p := painter{color: o.Color, utf8: o.UTF8}
	if o.Loc == nil {
		o.Loc = time.Local
	}
	var b strings.Builder
	switch {
	case len(shown) == 0:
		b.WriteString("\n  " + p.paint(ansiDim, "no domains match --min/--max") + "\n\n")
	case len(all) == 1:
		single(&b, shown[0], o, p)
	default:
		list(&b, shown, all, o, p)
	}
	b.WriteString(footer(p))
	_, err := io.WriteString(w, b.String())
	return err
}

func single(b *strings.Builder, r lookup.Result, o Options, p painter) {
	type line struct{ label, value string }
	var lines []line
	lines = append(lines, line{"domain", p.paint(ansiBold, r.Target)})
	if e := egg(r.Target, p); e != "" {
		lines = append(lines, line{"", p.paint(ansiDim, e)})
	}
	var big []string
	switch r.Status {
	case lookup.OK:
		v := *r.DR
		rating := p.paint(ansiBold+band(v), short(v))
		if o.UTF8 {
			rating += " ▐" + p.bar(v, 20) + p.paint(ansiDim, "  /100")
			big = bigText(short(v))
		} else {
			rating += p.paint(ansiDim, " /100")
		}
		lines = append(lines, line{"rating", rating})
	case lookup.NoData:
		lines = append(lines, line{"rating", p.paint(ansiDim, "no data")})
		if o.UTF8 {
			big = bigText("-")
		}
	case lookup.Invalid:
		lines = append(lines, line{"rating", p.paint(ansiRed, "invalid target")})
	default:
		lines = append(lines, line{"rating", p.paint(ansiRed, "no response")})
	}
	if r.History != nil && r.Status != lookup.NoResponse && r.Status != lookup.Invalid {
		tr := trendOf(r, o)
		var s string
		switch {
		case tr.checks <= 1:
			s = p.paint(ansiDim, "first check, run again later to see history")
		case len(tr.vals) > 0 && r.DR != nil:
			since := tr.since.In(o.Loc).Format("Jan 2")
			if tr.since.In(o.Loc).Year() != o.Now.In(o.Loc).Year() {
				since = tr.since.In(o.Loc).Format("Jan 2 2006")
			}
			if o.UTF8 {
				s = sparkline(tr.vals, 20) + "  "
			}
			s += p.delta(*r.DR-tr.vals[0]) + " since " + since
		}
		if s != "" {
			lines = append(lines, line{"trend", s})
		}
	}
	if !r.CheckedAt.IsZero() {
		s := r.CheckedAt.In(o.Loc).Format("2006-01-02") + " " + p.sym("·", "-") + " "
		age := o.Now.Sub(r.CheckedAt)
		if r.Cached || age >= time.Minute {
			s += ago(age)
			if r.Cached {
				s += ", cached"
				if age >= 24*time.Hour {
					s += p.paint(ansiDim, " (--fresh to refetch)")
				}
			}
		} else {
			s += "just now"
		}
		lines = append(lines, line{"checked", s})
	}
	lines = append(lines, line{"version", r.Version})

	if o.Width < 60 {
		big = nil
	}
	bigW := 0
	for _, row := range big {
		bigW = max(bigW, width(row))
	}
	color := ansiDim
	if r.DR != nil {
		color = band(*r.DR)
	}
	b.WriteByte('\n')
	for i := 0; i < max(len(lines), len(big)); i++ {
		b.WriteString("  ")
		if bigW > 0 {
			row := ""
			if i < len(big) {
				row = big[i]
			}
			b.WriteString(p.paint(color, pad(row, bigW)) + "     ")
		}
		if i < len(lines) {
			if lines[i].label == "" {
				b.WriteString(strings.Repeat(" ", 11))
			} else {
				b.WriteString(p.paint(ansiDim, pad(lines[i].label, 11)))
			}
			b.WriteString(lines[i].value)
		}
		b.WriteString("\n")
	}
	b.WriteByte('\n')
}

func list(b *strings.Builder, shown, all []lookup.Result, o Options, p painter) {
	barCells := 0
	if o.UTF8 {
		switch {
		case o.Width >= 100:
			barCells = 24
		case o.Width >= 60:
			barCells = 10
		}
	}
	withTrend := false
	for _, r := range all {
		withTrend = withTrend || r.History != nil
	}
	rows := shown
	more := 0
	if !o.All && len(rows) > maxRows {
		more = len(rows) - maxRows
		rows = rows[:maxRows]
	}

	const sparkW = 8
	trendW := 0
	if withTrend {
		trendW = sparkW + 2 + 6
		if !o.UTF8 {
			trendW = 8
		}
	}
	fixed := 2 + 5
	if barCells > 0 {
		fixed += barCells + 2
	}
	if trendW > 0 {
		fixed += trendW + 2
	}
	domW := 6
	for _, r := range rows {
		domW = max(domW, width(r.Target))
	}
	if avail := o.Width - fixed; domW > avail {
		domW = max(avail, 12)
	}
	ell := p.sym("…", "...")

	b.WriteByte('\n')
	head := "  " + pad("DR", 5)
	if barCells > 0 {
		head += strings.Repeat(" ", barCells+2)
	}
	head += pad("domain", domW)
	if withTrend {
		head += "  trend"
	}
	b.WriteString(p.paint(ansiDim, strings.TrimRight(head, " ")) + "\n")

	for _, r := range rows {
		b.WriteString("  ")
		if r.DR != nil {
			b.WriteString(p.paint(band(*r.DR), pad(short(*r.DR), 5)))
		} else {
			b.WriteString(p.paint(ansiDim, pad(p.sym(" –", " -"), 5)))
		}
		if barCells > 0 {
			if r.DR != nil {
				b.WriteString(p.bar(*r.DR, barCells))
			} else {
				b.WriteString(emptyBar(p, barCells))
			}
			b.WriteString("  ")
		}
		name := pad(truncMiddle(r.Target, domW, ell), domW)
		if !withTrend {
			b.WriteString(strings.TrimRight(name, " ") + "\n")
			continue
		}
		b.WriteString(name + "  ")
		switch {
		case r.Status == lookup.NoResponse:
			b.WriteString(p.paint(ansiRed, "no response"))
		case r.Status == lookup.Invalid:
			b.WriteString(p.paint(ansiRed, "invalid"))
		case r.Status == lookup.NoData:
			b.WriteString(p.paint(ansiDim, "no data"))
		default:
			tr := trendOf(r, o)
			spark := ""
			if o.UTF8 && len(tr.vals) > 0 {
				spark = sparkline(tr.vals, sparkW)
			}
			label := p.paint(ansiDim, "new")
			if tr.checks > 1 && len(tr.vals) > 0 {
				label = p.delta(*r.DR - tr.vals[0])
			}
			if o.UTF8 {
				b.WriteString(pad(spark, sparkW) + "  ")
			}
			b.WriteString(label)
		}
		b.WriteString("\n")
	}
	if more > 0 {
		b.WriteString("  " + p.paint(ansiDim, fmt.Sprintf("%s %d more, use --all", ell, more)) + "\n")
	}
	b.WriteString("\n  " + summary(all, o, p) + "\n")
}

func summary(all []lookup.Result, o Options, p painter) string {
	var vals []float64
	noData, noResp, invalid, fetched, cached := 0, 0, 0, 0, 0
	above := 0
	for _, r := range all {
		switch r.Status {
		case lookup.OK:
			vals = append(vals, *r.DR)
			if *r.DR >= o.Above {
				above++
			}
		case lookup.NoData:
			noData++
		case lookup.NoResponse:
			noResp++
		case lookup.Invalid:
			invalid++
		}
		switch {
		case r.Status == lookup.NoResponse || r.Status == lookup.Invalid:
		case r.Cached:
			cached++
		default:
			fetched++
		}
	}
	dot := " " + p.sym("·", "-") + " "
	parts := []string{plural(len(all), "domain")}
	if len(vals) > 0 {
		sort.Float64s(vals)
		m := vals[len(vals)/2]
		if len(vals)%2 == 0 {
			m = (vals[len(vals)/2-1] + vals[len(vals)/2]) / 2
		}
		parts = append(parts, "median "+short(m), fmt.Sprintf("%d above %s", above, short(o.Above)))
	}
	if noData > 0 {
		parts = append(parts, fmt.Sprintf("%d no data", noData))
	}
	if noResp > 0 {
		parts = append(parts, p.paint(ansiRed, fmt.Sprintf("%d no response", noResp)))
	}
	if invalid > 0 {
		parts = append(parts, p.paint(ansiRed, fmt.Sprintf("%d invalid", invalid)))
	}
	parts = append(parts, fmt.Sprintf("%d fetched, %d cached", fetched, cached))
	return strings.Join(parts, dot)
}
