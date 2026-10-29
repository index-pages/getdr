# Ahrefs API key

getdr reads Domain Rating from the official free Ahrefs endpoint. Requests to it cost nothing and use no
API units, but every request needs an API key. The key is free.

## Get a key

1. Sign up for a free account at [ahrefs.com](https://ahrefs.com/) (skip if you have one).
2. Open [Account settings → API keys](https://app.ahrefs.com/account/api-keys).
3. Create a key and copy it.

## Give it to getdr

Any of these works; the first one found wins:

| where | example |
|---|---|
| flag | `getdr --key KEY saas.com` |
| `GETDR_KEY` | `export GETDR_KEY=KEY` |
| `AHREFS_KEY` | `export AHREFS_KEY=KEY` |
| config file | `key = "KEY"` in `~/.config/getdr/config.toml` |

Check it:

```sh
getdr key check
```

`getdr config show` shows which source the key came from; only its last 4 characters are printed.

## Errors

- **Exit code 3, "no Ahrefs API key"** – none of the sources above has a key.
- **Exit code 3, "rejected by Ahrefs (HTTP 401)"** – the key is wrong or was revoked; create a new one.
- **Exit code 3 with `--api`** – the getdr server has no key or Ahrefs rejected it; fix the key on the server.

A key is only needed when something has to be fetched: values still fresh in the cache are shown without one.

## Sharing one key

Run `getdr serve` on one machine with the key and point other machines at it with `api` (or `--api`,
`GETDR_API`). Clients then need no key at all. See [the HTTP API](api.md).

## Limits

Ahrefs publishes no official rate limit for this endpoint; in practice about 40–60 requests per minute work.
getdr sends up to 1000 targets per request, limits itself to `rpm` requests per minute (default 40) and
retries 429 and 5xx answers with exponential backoff.
