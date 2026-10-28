# Install getdr

Draft for https://index-pages.com/getdr#install

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
tar -xzf getdr_0.1.0_linux_amd64.tar.gz
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
docker build -t getdr .
docker run -d -p 8080:8080 -e GETDR_KEY=your-key -v getdr-data:/data getdr
```

The image is `FROM scratch` with only the binary and a CA bundle, runs as an unprivileged user and keeps
its cache in `/data`.

## Planned

Homebrew, npm and PyPI packages are planned and not available yet.

## Next

- [Get a free Ahrefs API key](key.md)
- [Configuration](config.md)
- [HTTP API](api.md)
