package output

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/index-pages/getdr/internal/cache"
	"github.com/index-pages/getdr/internal/lookup"
)

var now = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func fp(v float64) *float64 { return &v }

func res(target string, dr *float64, ago time.Duration, cached bool, hist ...float64) lookup.Result {
	r := lookup.Result{Target: target, Version: "dr3", DR: dr, CheckedAt: now.Add(-ago), Cached: cached, Status: lookup.OK}
	if dr == nil {
		r.Status = lookup.NoData
	}
	r.History = []cache.Check{}
	for i, v := range hist {
		r.History = append(r.History, cache.Check{DR: fp(v), CheckedAt: now.Add(-time.Duration(len(hist)-i) * 24 * time.Hour)})
	}
	if hist != nil || dr == nil {
		r.History = append(r.History, cache.Check{DR: dr, CheckedAt: r.CheckedAt})
	}
	return r
}

func opts(format string) Options {
	return Options{Format: format, Fields: []string{"domain", "dr"}, UTF8: true, Width: 120, Above: 50,
		MaxAge: 60 * 24 * time.Hour, Now: now, Loc: time.UTC}
}

func render(t *testing.T, shown, all []lookup.Result, o Options) string {
	t.Helper()
	var b strings.Builder
	if err := Write(&b, shown, all, o); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func sample() []lookup.Result {
	none := lookup.Result{Target: "down.example", Version: "dr3", Status: lookup.NoResponse}
	return []lookup.Result{
		res("saas.com", fp(49), 12*time.Minute, true, 45, 47),
		res("typo.example", nil, time.Hour, true),
		res("somedevblog.io", fp(61.3), 0, false, 52),
		none,
	}
}

func TestMachineFormats(t *testing.T) {
	rs := sample()
	got := render(t, rs, rs, opts("tsv"))
	want := "saas.com\t49\ntypo.example\t\nsomedevblog.io\t61.3\ndown.example\t\n"
	if got != want {
		t.Errorf("tsv:\n%q\nwant\n%q", got, want)
	}
	o := opts("csv")
	o.Fields = []string{"domain", "dr", "version", "checked_at", "delta", "cached"}
	got = render(t, rs[:2], rs, o)
	want = "domain,dr,version,checked_at,delta,cached\n" +
		"saas.com,49,dr3,2026-10-28T11:48:00Z,2,true\n" +
		"typo.example,,dr3,2026-10-28T11:00:00Z,,true\n"
	if got != want {
		t.Errorf("csv:\n%s\nwant\n%s", got, want)
	}
	got = render(t, rs, rs, opts("json"))
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 4 ||
		lines[0] != `{"target":"saas.com","dr":49,"version":"dr3","checked_at":"2026-10-28T11:48:00Z","cached":true}` ||
		lines[1] != `{"target":"typo.example","dr":null,"version":"dr3","checked_at":"2026-10-28T11:00:00Z","cached":true}` ||
		lines[3] != `{"target":"down.example","dr":null,"version":"dr3","checked_at":null,"cached":false}` {
		t.Errorf("json:\n%s", got)
	}
	o = opts("json")
	o.Fields = []string{"delta"}
	got = render(t, rs[:2], rs, o)
	if !strings.Contains(got, `"cached":true,"delta":2}`) || !strings.Contains(got, `"delta":null}`) {
		t.Errorf("json delta:\n%s", got)
	}
	if got = render(t, rs, rs, opts("bare")); got != "49\n\n61.3\n\n" {
		t.Errorf("bare %q", got)
	}
	for _, f := range []string{"tsv", "csv", "json", "bare"} {
		if s := render(t, rs, rs, opts(f)); strings.Contains(s, "Ahrefs") || strings.Contains(s, "\x1b") {
			t.Errorf("%s output must hold only data: %q", f, s)
		}
	}
}

func TestCardSingle(t *testing.T) {
	r := res("saas.com", fp(49), 12*time.Minute, true, 45, 47)
	r.History[0].CheckedAt = time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	got := render(t, []lookup.Result{r}, []lookup.Result{r}, opts("card"))
	for _, want := range []string{
		"domain     saas.com", "rating     49 ▐█████████▌░░░░░░░░░░  /100",
		"trend      ▁▅█  +4 since Sep 17", "checked    2026-10-28 · 12 min ago, cached",
		"version    dr3", "█",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b") {
		t.Error("no color requested")
	}

	r = res("index-pages.com", fp(61.3), 3*24*time.Hour, true)
	got = render(t, []lookup.Result{r}, []lookup.Result{r}, opts("card"))
	for _, want := range []string{"that's us 👋", "61.3 ▐", "first check, run again later to see history", "3 days ago, cached (--fresh to refetch)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	r = res("ahrefs.com", fp(91), 0, false)
	got = render(t, []lookup.Result{r}, []lookup.Result{r}, opts("card"))
	if !strings.Contains(got, "thanks for the free API") || !strings.Contains(got, "· just now") {
		t.Errorf("ahrefs card:\n%s", got)
	}
	r = res("other.com", fp(10), 0, false)
	if got := render(t, []lookup.Result{r}, []lookup.Result{r}, opts("csv")); strings.Contains(got, "thanks") {
		t.Error("easter eggs only in card")
	}
}

func TestCardColor(t *testing.T) {
	o := opts("card")
	o.Color = true
	for v, code := range map[float64]string{5: ansiGray, 25: ansiYellow, 55: ansiGreen, 85: ansiBlue} {
		r := res("x.com", fp(v), 0, false)
		if got := render(t, []lookup.Result{r}, []lookup.Result{r}, o); !strings.Contains(got, code) {
			t.Errorf("DR %v must use %q", v, code)
		}
	}
}

func TestCardASCII(t *testing.T) {
	rs := sample()
	o := opts("card")
	o.UTF8 = false
	for _, shown := range [][]lookup.Result{rs[:1], rs} {
		got := render(t, shown, shown, o)
		for _, r := range got {
			if r > 0x7e && r != '\n' {
				t.Fatalf("non-ASCII %q in\n%s", r, got)
			}
		}
	}
}

func TestCardList(t *testing.T) {
	rs := sample()
	shown := append([]lookup.Result(nil), rs...)
	SortByDR(shown)
	if shown[0].Target != "somedevblog.io" || shown[2].Target != "typo.example" || shown[3].Target != "down.example" {
		t.Fatalf("sort %v", shown)
	}
	got := render(t, shown, rs, opts("card"))
	for _, want := range []string{
		"DR", "domain", "trend",
		"61.3 ██████████████▌░░░░░░░░░  somedevblog.io",
		"no data", "no response",
		"4 domains · median 55.2 · 1 above 50 · 1 no data · 1 no response · 1 fetched, 2 cached",
		"+9.3", "+4",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	o := opts("card")
	o.Width = 80
	if got := render(t, shown, rs, o); !strings.Contains(got, "61.3 ██████░░░░  somedevblog.io") {
		t.Errorf("narrow bar:\n%s", got)
	}
	o.Width = 50
	if got := render(t, shown, rs, o); strings.Contains(got, "░") {
		t.Errorf("no bar under 60 columns:\n%s", got)
	}
}

func TestCardLimit(t *testing.T) {
	var rs []lookup.Result
	for i := 0; i < 250; i++ {
		rs = append(rs, res(fmt.Sprintf("d%03d.com", i), fp(float64(i%100)), 0, false))
	}
	got := render(t, rs, rs, opts("card"))
	if !strings.Contains(got, "… 50 more, use --all") || strings.Count(got, ".com") != 200 {
		t.Errorf("limit: %d rows", strings.Count(got, ".com"))
	}
	o := opts("card")
	o.All = true
	if got := render(t, rs, rs, o); strings.Count(got, ".com") != 250 || strings.Contains(got, "more, use --all") {
		t.Error("--all")
	}
}

func TestHelpers(t *testing.T) {
	p := painter{utf8: true}
	if got := p.bar(49, 20); got != "█████████▌░░░░░░░░░░" {
		t.Errorf("bar 49: %q", got)
	}
	if got := p.bar(78, 24); got != "██████████████████▌░░░░░" {
		t.Errorf("bar 78: %q", got)
	}
	if got := p.bar(100, 10); got != "██████████" {
		t.Errorf("bar 100: %q", got)
	}
	if got := truncMiddle("verylongsubdomain.domain.com", 19, "…"); got != "verylong…domain.com" {
		t.Errorf("truncMiddle %q", got)
	}
	if got := truncMiddle("short.com", 19, "…"); got != "short.com" {
		t.Errorf("truncMiddle %q", got)
	}
	cases := map[time.Duration]string{
		10 * time.Second: "just now", 12 * time.Minute: "12 min ago", 90 * time.Minute: "1 hour ago",
		5 * time.Hour: "5 hours ago", 30 * time.Hour: "1 day ago", 72 * time.Hour: "3 days ago",
	}
	for d, want := range cases {
		if got := ago(d); got != want {
			t.Errorf("ago(%v) = %q", d, got)
		}
	}
	if sparkline([]float64{3, 3, 3}, 8) != "▃▃▃" || sparkline([]float64{1, 2, 9}, 8) != "▁▂█" || sparkline([]float64{5}, 8) != "▁" {
		t.Error("sparkline")
	}
	if short(61.33) != "61.3" || short(49) != "49" || short(49.04) != "49" {
		t.Error("short")
	}
	if _, err := ParseFields("domain,dr,nope"); err == nil {
		t.Error("ParseFields must reject unknown fields")
	}
}
