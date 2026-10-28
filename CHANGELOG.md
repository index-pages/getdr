# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-10-28

### Added

- `getdr` CLI: check Ahrefs Domain Rating for domains or URLs from arguments,
  a file (`-f`) or stdin; target normalization (including punycode to
  Unicode) and de-duplication (`--raw` to skip it).
- Invalid targets are isolated: rejected locally or taken out of a batch that
  Ahrefs refuses, reported on stderr, never cached; the rest is still checked.
- Output formats: `card` (terminal), `tsv`, `csv`, `json` (NDJSON) and `-b`
  (bare values); `--fields`, `--sort`, `--min`/`--max` with exit code 1,
  `--all`.
- DR versions `dr3` (default) and `dr4` via `--version`.
- SQLite cache with full check history (WAL, shared by CLI and server),
  separate TTL for "no data", `cache stats | clear | purge`.
- Batched POST requests to Ahrefs (up to 1000 targets), requests-per-minute
  limit, retries with exponential backoff on 429/5xx, per-request timeout.
- `getdr serve`: local HTTP API (`GET/POST /dr`, `/healthz`, `/stats`) with a
  shared cache, optional bearer token and in-flight request de-duplication.
- `api` setting: point the CLI at a getdr server instead of Ahrefs.
- Config via flags, `GETDR_*` environment variables and a TOML file;
  `config show` prints every value with its source.
- `key check` to verify an Ahrefs API key.
- Dockerfile (`FROM scratch`) for `getdr serve`.

[Unreleased]: https://github.com/index-pages/getdr/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/index-pages/getdr/releases/tag/v0.1.0
