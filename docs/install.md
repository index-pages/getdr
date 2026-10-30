# Install getdr

Every channel ships the same prebuilt binary as the GitHub release, and none of them runs anything at
install time.

## Homebrew (macOS, Linux)

```sh
brew install index-pages/tap/getdr
```

The formula lives in [index-pages/homebrew-tap](https://github.com/index-pages/homebrew-tap) and installs
the release archive for your platform, checked by sha256; Go is not needed.

## npm

```sh
npm install -g getdr
npx getdr saas.com
```

The `getdr` package lists one package per platform as optional dependencies: `getdr-linux-x64`,
`getdr-linux-arm64`, `getdr-darwin-x64`, `getdr-darwin-arm64`, `getdr-win32-x64`, `getdr-win32-arm64`.
npm installs only the one that matches your `os` and `cpu`, and the `getdr` command is a small Node script
that runs its binary. There are no install scripts, so it works with `ignore-scripts=true`; it does not
work with `--omit=optional`. Node 18 or newer.

## PyPI

```sh
pipx install getdr
uv tool install getdr
pip install getdr
```

The wheels (Linux glibc and musl, macOS 11+, Windows; x86_64 and arm64) hold only the binary, which lands
in the environment's `bin` (`Scripts` on Windows). There is no sdist and no Python code, so no build step
runs.

## Go

With Go 1.24 or newer:

```sh
go install -trimpath github.com/index-pages/getdr@latest
```

The binary lands in `$(go env GOPATH)/bin`; make sure it is on your `PATH`.

## Prebuilt binaries

Every release on [GitHub Releases](https://github.com/index-pages/getdr/releases) has archives for
Linux, macOS and Windows on amd64 and arm64, plus `checksums.txt`.

```sh
tar -xzf getdr_0.2.1_linux_amd64.tar.gz
sha256sum -c --ignore-missing checksums.txt
sudo install getdr /usr/local/bin/
getdr -V
```

The binary is static (no cgo, SQLite is compiled in) and has no runtime dependencies.

## Build from source

```sh
git clone https://github.com/index-pages/getdr
cd getdr
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o getdr .
```

## Docker (for getdr serve)

```sh
docker run -d -p 8080:8080 -e GETDR_KEY=your-key -v getdr-data:/data ghcr.io/index-pages/getdr
```

`ghcr.io/index-pages/getdr` is built for linux/amd64 and linux/arm64 and tagged with the version
(`0.2.1`), the minor version (`0.2`) and `latest`. It is `FROM scratch` with only the binary and a CA
bundle, runs as an unprivileged user and keeps its cache in `/data`. To build it yourself from a clone:
`docker build -t getdr .`.

## Next

- [Get a free Ahrefs API key](key.md)
- [Configuration](config.md)
- [HTTP API](api.md)
