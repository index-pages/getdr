# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-11-19

### Changed

- The default DR version is `dr4`, the new Ahrefs formula; before it was
  `dr3`. This applies to the CLI, the config (`version`), `getdr serve`
  for clients that send no version, and `key check`. To stay on the old
  formula, set `version = "dr3"` in the config or `GETDR_VERSION=dr3`, or
  pass `--version dr3`.
- `dr3` and `dr4` values are cached apart, so after the upgrade the first
  check of a domain is fetched again and its trend starts anew; the `dr3`
  history stays in the cache.
- Docs and examples use `dr4`.

## [0.2.3] - 2026-11-19

### Fixed

- PyPI: the wheels are written without ZIP data descriptors, which PyPI
  rejects; 0.2.1 and 0.2.2 never reached PyPI.

## [0.2.2] - 2026-11-19

### Changed

- npm: the platform packages moved to the `@index-pages` scope
  (`@index-pages/getdr-linux-x64`, `@index-pages/getdr-darwin-arm64`, …).
  The unscoped `getdr-<os>-<cpu>` packages of 0.2.1 are deprecated;
  `getdr` itself keeps its name. No change for `npm install -g getdr`.

## [0.2.1] - 2026-10-30

### Added

- Homebrew: `brew install index-pages/tap/getdr` (macOS and Linux), from the
  [index-pages/homebrew-tap](https://github.com/index-pages/homebrew-tap) tap.
- npm: `npm install -g getdr`. The binary comes from a platform package
  (`getdr-linux-x64`, `getdr-darwin-arm64`, …) picked by `os` and `cpu`;
  no install scripts.
- PyPI: `pipx install getdr`, `uv tool install getdr` or `pip install getdr`.
  The wheels hold only the binary.
- Docker: `ghcr.io/index-pages/getdr` for linux/amd64 and linux/arm64.
- The release workflow publishes all of these; `tools/dist` builds the
  formula, npm packages and wheels from the release archives.

### Changed

- Links to the site use the canonical `https://index-pages.com/getdr/`
  (with the trailing slash) in messages, `-h`, the User-Agent and the
  `getdr serve` index.

## [0.2.0] - 2026-10-29

### Added

- The JSON record of an invalid target has `"error":"invalid target"`
  (`--format json` and `getdr serve`). With `--api`, a target the server's
  Ahrefs refuses is reported as an invalid target instead of "no response".
- A bare `-` argument reads targets from stdin in its place.
- The test mock server takes `-version` for its default DR version.

### Changed

- `--fields` applies to json: only the listed keys, in the order given
  (`domain` is the `target` key). Before, json always printed the full record
  and `--fields` could only add `delta`. Without `--fields` json still prints
  the full record. A field listed twice is an error.

### Removed

- The Ahrefs attribution line is no longer printed on stderr after machine
  formats or `key check`, nor in `-h`. Machine output carries only the data;
  the card footer keeps it.
- The "Draft for …" lines at the top of `docs/*.md`.

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

[Unreleased]: https://github.com/index-pages/getdr/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/index-pages/getdr/compare/v0.2.3...v0.3.0
[0.2.3]: https://github.com/index-pages/getdr/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/index-pages/getdr/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/index-pages/getdr/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/index-pages/getdr/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/index-pages/getdr/releases/tag/v0.1.0
