# HTTP API (getdr serve)

`getdr serve` is a small HTTP API in front of Ahrefs with a shared cache. Run it once with an Ahrefs key,
then point CLIs, scripts and agents at it: they need no key, and repeated checks come from the cache.

```sh
GETDR_KEY=your-key getdr serve --listen 0.0.0.0:8080 --token s3cret
```

Every response has the header `Server: getdr/<version> (index-pages.com)`. Errors are JSON:
`{"error":"…"}`.

## Authorization

When `token` is set, `/dr` and `/stats` need `Authorization: Bearer <token>`; otherwise they answer
`401 {"error":"missing or invalid bearer token"}`. `/`, `/healthz` and `/teapot` are always open.

## Result object

The same object the CLI prints with `--format json`:

```json
{"target":"saas.com","dr":49,"version":"dr3","checked_at":"2026-10-28T12:00:00Z","cached":true}
```

| field | |
|---|---|
| `target` | the normalized target (or as given with `raw`) |
| `dr` | Domain Rating 0–100; `null` when the target is invalid, Ahrefs did not respond, or (rarely) has no data |
| `version` | `dr3` or `dr4` |
| `checked_at` | when the value was measured, RFC 3339 UTC; `null` for invalid targets and no response |
| `cached` | `true` when served from the server cache |
| `error` | `"invalid target"` for an invalid target; absent on every other record |

Ahrefs answers `0` for domains it does not know, so `0` is a normal value. `dr: null` with `checked_at: null`
means the target was invalid (then `error` says so) or there was no response this time – neither is cached. `dr: null` with a
`checked_at` means Ahrefs explicitly had no data (cached for `ttl_none`).

Invalid targets never fail a request: input without a host or with whitespace or control characters is
rejected locally, and a target Ahrefs refuses with HTTP 400 (`{"error":"bad 'targets': invalid domain or url:
'localhost'"}`) is taken out of its batch while the rest is resent. Both come back as `dr: null`,
`checked_at: null`, `"error":"invalid target"` inside a normal `200`.

## GET /dr

```
GET /dr?target=saas.com[&version=dr3][&fresh=1][&raw=1]
```

`200` with one result object. `version` defaults to the server's `version` setting; `fresh=1` skips the
cache; `raw=1` disables normalization.

## POST /dr

```json
{"targets":["saas.com","https://www.example.com/page"],"version":"dr3","fresh":false,"raw":false}
```

`200`:

```json
{"results":[
  {"target":"saas.com","dr":49,"version":"dr3","checked_at":"2026-10-28T12:00:00Z","cached":true},
  {"target":"example.com","dr":93,"version":"dr3","checked_at":"2026-10-28T12:03:11Z","cached":false}
]}
```

Targets are normalized (punycode labels decoded to Unicode) and de-duplicated; results follow their order. The server splits the work into
Ahrefs batches of up to 1000 and respects its rate limit. Only `targets`, `version`, `fresh` and `raw` are
accepted – clients cannot change the server's TTL.

## Errors

| status | when |
|---|---|
| 400 | missing `target`, invalid `version`, invalid JSON, unknown field or data after the JSON object, no valid targets |
| 401 | token required and missing or wrong |
| 404 | unknown path |
| 405 | wrong method (also `HEAD /dr`, which never triggers a lookup) |
| 413 | more than `max_targets` targets (default 10000) |
| 503 | the server has no Ahrefs key, or Ahrefs rejected it |

## GET /healthz

`200 ok` (plain text).

## GET /stats

```json
{
  "cache": {"size_bytes":45328,"rows":1200,"domains":950,
            "versions":{"dr3":900,"dr4":50},"oldest":"2026-10-01T09:00:00Z","newest":"2026-10-28T12:03:11Z"},
  "lookups": {"hits":1830,"misses":120,"hit_rate":0.9385},
  "ahrefs_requests_last_hour": 3
}
```

`lookups` counts targets since the server started; `rows` are stored checks (history), `domains` distinct
domains.

## GET /

```json
{"name":"getdr","version":"0.2.1","homepage":"https://index-pages.com/getdr/","docs":"https://index-pages.com/getdr/#api"}
```

## Concurrency

Concurrent requests for the same domain and version share one Ahrefs request. The CLI and the server can use
the same cache file at the same time.

## Using it from the CLI

```sh
getdr --api http://lan-host:8080 --token s3cret -f domains.txt
```

or set `api` and `serve.token` in the config file, or `GETDR_API` and `GETDR_SERVE_TOKEN`. The CLI sends one
`POST /dr` for the targets its own cache does not cover and forwards `--fresh`. It always sends its own DR
version (`--version`, or `version` in its config, `dr3` by default), so the server's `version` setting
applies only to clients that send none. Proxy variables
(`HTTP_PROXY`, `HTTPS_PROXY`) apply to this request too; add a LAN server to `NO_PROXY`.
