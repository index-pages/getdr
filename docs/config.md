# Configuration

## Priority

1. command-line flag
2. environment variable `GETDR_*` (for the key also `AHREFS_KEY`, below `GETDR_KEY`)
3. config file
4. built-in default

Empty environment variables count as unset.

## Config file

The first of these is used:

1. `--config PATH`
2. `$GETDR_CONFIG`
3. `$XDG_CONFIG_HOME/getdr/config.toml`, or `~/.config/getdr/config.toml` when `XDG_CONFIG_HOME` is unset
4. `/etc/getdr/config.toml`

A file given with `--config` or `GETDR_CONFIG` must exist. Unknown settings are an error, so typos do not go
unnoticed. `~/` at the start of a path is expanded.

```toml
key = ""
api = ""
version = "dr3"

[cache]
path = "~/.cache/getdr/cache.db"
ttl = "7d"
ttl_none = "1d"
max_age = "30d"

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

## Settings

| setting | env | flag | default | meaning |
|---|---|---|---|---|
| `key` | `GETDR_KEY`, `AHREFS_KEY` | `--key` | – | Ahrefs API key |
| `api` | `GETDR_API` | `--api` | – | URL of a getdr server; when set, the CLI asks it instead of Ahrefs |
| `version` | `GETDR_VERSION` | `--version` | `dr3` | DR version, `dr3` or `dr4` |
| `cache.path` | `GETDR_CACHE_PATH` | – | `$XDG_CACHE_HOME/getdr/cache.db` or `~/.cache/getdr/cache.db` | SQLite cache file |
| `cache.ttl` | `GETDR_CACHE_TTL` | `--ttl` | `7d` | how long a value stays fresh |
| `cache.ttl_none` | `GETDR_CACHE_TTL_NONE` | – | `1d` | how long "no data" stays fresh |
| `cache.max_age` | `GETDR_CACHE_MAX_AGE` | – | `30d` | trend window; `cache purge` drops older checks |
| `ahrefs.batch` | `GETDR_AHREFS_BATCH` | – | `1000` | targets per Ahrefs request (1–1000) |
| `ahrefs.rpm` | `GETDR_AHREFS_RPM` | – | `40` | Ahrefs requests per minute |
| `ahrefs.timeout` | `GETDR_AHREFS_TIMEOUT` | – | `30s` | timeout of one Ahrefs request |
| `ahrefs.retries` | `GETDR_AHREFS_RETRIES` | – | `3` | retries on 429, 5xx and network errors |
| `serve.listen` | `GETDR_SERVE_LISTEN` | `--listen` | `127.0.0.1:8080` | address of `getdr serve` |
| `serve.token` | `GETDR_SERVE_TOKEN` | `--token` | – | bearer token `getdr serve` requires; the CLI sends it when `api` is set |
| `serve.max_targets` | `GETDR_SERVE_MAX_TARGETS` | – | `10000` | targets per `POST /dr` |

Durations accept days with a `d` suffix (`7d`, `1.5d`) and Go durations (`36h`, `90m`, `30s`).

## Inspect

```sh
getdr config show
```

```
key                 ****1a2b                    env AHREFS_KEY
api                 ""                          default
version             dr3                         default
cache.path          /home/me/.cache/getdr/cache.db   default
cache.ttl           7d                          default
...
config file: none
```

Secrets (`key`, `serve.token`) show only their last 4 characters.

## Other environment variables

- `NO_COLOR` – no colors in the card.
- `TERM=dumb`, or a locale without UTF-8 (`LC_ALL`, `LC_CTYPE`, `LANG`) – plain ASCII card without big digits
  and bars.
- `COLUMNS` – card width when it cannot be read from the terminal.
- `HOME`, `XDG_CONFIG_HOME`, `XDG_CACHE_HOME` – default file locations.
- `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` – the standard proxy variables are honoured; when you use a proxy and
  a getdr server on your LAN as `api`, add that server to `NO_PROXY`.
