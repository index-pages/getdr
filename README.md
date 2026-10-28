# getdr

`getdr` is a small open-source command-line tool by [Index Pages](https://index-pages.com) that checks
Ahrefs Domain Rating (DR) for one domain, a list, a file or stdin. It uses only the official free Ahrefs
endpoint, keeps a local history of every check in SQLite, and can run as a tiny HTTP API (`getdr serve`)
so every machine and agent on your network shares one cache and one key. Docs:
[index-pages.com/getdr](https://index-pages.com/getdr).

```
  █  █ ▄▀▀▄     domain     saas.com
  █  █ █  █     rating     49 ▐█████████▌░░░░░░░░░░  /100
  ▀▀▀█  ▀▀█     trend      ▁▅█  +4 since Sep 17
     █    █     checked    2026-10-28 · 12 min ago, cached
     █  ▄▄▀     version    dr3

  Domain Rating by Ahrefs · getdr by Index Pages
```

## Install

```sh
go install -trimpath github.com/index-pages/getdr@latest
```

Or download a binary for Linux, macOS or Windows (amd64, arm64) from the
[Releases](https://github.com/index-pages/getdr/releases) page and put it on your `PATH`.

<!-- TODO: brew install index-pages/tap/getdr -->
<!-- TODO: npm install -g getdr -->
<!-- TODO: pip install getdr -->

Homebrew, npm and PyPI packages are planned. More in [docs/install.md](docs/install.md).

## Get a free Ahrefs API key

Requests to the Domain Rating endpoint are free, but need an API key: sign up for a free
[Ahrefs](https://ahrefs.com/) account, open Account settings → API keys, create a key and export it:

```sh
export AHREFS_KEY=your-key
getdr key check
```

Details: [docs/key.md](docs/key.md).

## Quick start

```sh
getdr saas.com                          # card on a terminal
getdr https://www.example.com/page      # URLs are reduced to the host
getdr a.com b.com c.com                 # several targets, sorted by DR
getdr -f domains.txt                    # one per line, # comments allowed
cat domains.txt | getdr > dr.tsv        # pipe: tab-separated domain and DR
getdr -f domains.txt --format json      # NDJSON, one object per line
getdr -b a.com b.com                    # only the values
getdr -f domains.txt --min 30 -q        # keep DR >= 30, exit 1 if any failed
getdr saas.com --version dr4            # the new DR formula
getdr saas.com --fresh                  # ignore the cache
```

Targets are normalized by default: a URL becomes its host, `www.` is dropped, everything is
lower-cased, punycode labels (`xn--…`) are decoded to Unicode – so `xn--e1afmkfd.xn--p1ai` and `ПРИМЕР.РФ`
are both `пример.рф` – and duplicates are removed. `--raw` sends targets as given.

Invalid targets don't spoil the rest of the list. Input with no host, whitespace or control characters is
rejected before anything is sent; a target Ahrefs refuses (such as `localhost` or `a..com`) is dropped from
its batch and the batch is resent. Each one gets `getdr: invalid target: X` on stderr, an empty result and
exit code 2, and is never cached.

## Output

On a terminal the default is `card`; when stdout is a pipe or a file it is `tsv`.
`--format` and `-b` always win.

| format | what you get |
|---|---|
| `card` | big number, bar, trend and summary for people; colors follow DR (0–19 gray, 20–39 yellow, 40–69 green, 70+ blue) |
| `tsv` | `domain<TAB>dr`, no header; empty DR when there is no data |
| `csv` | same columns with a header row |
| `json` | NDJSON: `{"target":"x.com","dr":49,"version":"dr3","checked_at":"2026-10-28T12:00:00Z","cached":true}` |
| `-b` | DR values only, one per line, empty line for no data |

`--fields` picks tsv/csv columns from `domain,dr,version,checked_at,delta,cached` (default `domain,dr`);
for json the record is always complete and `delta` is added when listed. Ahrefs answers `0` for domains it
does not know, so `dr` is `null` almost only when the target was invalid or got no response (then
`checked_at` is `null` too); a `null` with a `checked_at` would mean Ahrefs explicitly had no data.

The card respects `NO_COLOR`, and falls back to plain ASCII without big digits and bars on `TERM=dumb`
or a non-UTF-8 locale. Lists over 200 rows show the top 200; `--all` shows everything.

## Flags

```
-f, --file FILE        read targets from FILE ("-" for stdin)
-o, --out FILE         write the result to FILE instead of stdout
    --format F         card, tsv, csv or json
-b, --bare             only the DR values
    --fields LIST      domain,dr,version,checked_at,delta,cached
    --sort             sort by DR (card always sorts)
    --all              card: show all rows
    --min N, --max N   filter by DR; exit 1 if any target fails
    --raw              no target normalization
    --version V        dr3 (default) or dr4
    --fresh            refetch and update the cache
    --no-cache         neither read nor write the cache
    --ttl DUR          cache TTL for this run (7d, 12h, 30m)
    --api URL          use a getdr server instead of Ahrefs
    --key KEY          Ahrefs API key
    --config PATH      config file
    --listen ADDR      serve: listen address
    --token T          serve: required token; with --api: token to send
-q, --quiet            no progress, attribution or warnings on stderr
-v, --verbose          cache hits, API calls and batches on stderr
-V                     print the getdr version
```

Commands: `getdr serve`, `getdr cache stats | clear | purge`, `getdr config show`, `getdr key check`.

Exit codes: `0` ok, `1` a target failed `--min`/`--max`, `2` error, invalid targets or targets without a
response (timeout, rate limit), `3` Ahrefs API key missing or rejected.

## Configuration

Priority: flag → `GETDR_*` environment variable → config file → default. The key is also read from
`AHREFS_KEY` (below `GETDR_KEY`). The file is `--config`, else `$GETDR_CONFIG`, else
`$XDG_CONFIG_HOME/getdr/config.toml` (`~/.config/getdr/config.toml`), else `/etc/getdr/config.toml`.

```toml
key = ""                 # empty = from the environment
api = ""                 # URL of your getdr serve; when set, the CLI asks it instead of Ahrefs
version = "dr3"

[cache]
path = "~/.cache/getdr/cache.db"
ttl = "7d"
ttl_none = "1d"          # for "no data"
max_age = "30d"          # cache purge drops older checks, keeping the latest one per domain

[ahrefs]
batch = 1000
rpm = 40
timeout = "30s"
retries = 3

[serve]
listen = "127.0.0.1:8080"
token = ""
max_targets = 10000
```

`getdr config show` prints the merged values and where each came from; the key is masked.
Every setting and its environment variable: [docs/config.md](docs/config.md).

## Cache

Every check is stored as a row (domain, version, dr, checked_at), so the card can show a trend and `delta`
works. A value is fresh while the latest check is younger than `ttl` (`ttl_none` for "no data"); otherwise it
is fetched again. `dr3` and `dr4` are never mixed. Targets that got no response are not cached.
`getdr cache purge` removes checks older than `max_age` but always keeps the latest one per domain.

## serve

```sh
GETDR_KEY=your-key getdr serve --listen 0.0.0.0:8080 --token s3cret
curl -H 'Authorization: Bearer s3cret' 'http://lan-host:8080/dr?target=saas.com'
getdr --api http://lan-host:8080 --token s3cret -f domains.txt
```

| endpoint | |
|---|---|
| `GET /dr?target=x.com[&version=dr3][&fresh=1][&raw=1]` | one result object |
| `POST /dr` `{"targets":[…],"version":"dr3","fresh":false,"raw":false}` | `{"results":[…]}` |
| `GET /healthz` | `ok` |
| `GET /stats` | cache size, hit rate, Ahrefs requests in the last hour |
| `GET /` | name, version, links |

The server batches requests to Ahrefs, shares its cache with concurrent clients, and sends one Ahrefs request
when several clients ask for the same domain at once. Clients control only `fresh`, never the TTL. Without an
Ahrefs key `/dr` answers 503. Full reference: [docs/api.md](docs/api.md).

Docker (a `FROM scratch` image with the binary and a CA bundle):

```sh
docker build -t getdr .
docker run -d -p 8080:8080 -e GETDR_KEY=your-key -e GETDR_SERVE_TOKEN=s3cret -v getdr-data:/data getdr
```

## Attribution

Domain Rating data comes from [Ahrefs](https://ahrefs.com/) and is subject to the
[Domain Rating License](https://ahrefs.com/legal/domain-rating-license), which requires the attribution
"Domain Rating by Ahrefs". getdr prints it in the card footer, and once per run on stderr for machine
formats (never on stdout; `-q` silences it). Keep it when you publish the numbers.

getdr is not affiliated with Ahrefs.

## No telemetry

getdr connects to exactly two places: `api.ahrefs.com`, and the getdr server you set in `api`. No telemetry,
no update checks, no analytics, no pings. Every request carries the User-Agent
`getdr/<version> (+https://index-pages.com/getdr)`. You can verify this in the source: all outgoing requests
are made in `internal/ahrefs` and `internal/remote`, and redirects to any other host are refused.
The binary honours the standard `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` environment variables; if you use
a proxy and a getdr server on your LAN, add the server to `NO_PROXY`.

## License

MIT, see [LICENSE](LICENSE). Made by [Index Pages](https://index-pages.com).
