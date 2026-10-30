# getdr serve in a scratch image, published as ghcr.io/index-pages/getdr.
#   docker build -t getdr .
#   docker run -p 8080:8080 -e GETDR_KEY=... -v getdr-data:/data getdr
# The builder runs on the build host and cross-compiles for the target
# platform, so a multi-platform build needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.24.4-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=0.2.1
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/index-pages/getdr/internal/buildinfo.Version=${VERSION}" \
      -o /out/getdr . \
 && mkdir -p /out/data

FROM scratch
LABEL org.opencontainers.image.title="getdr" \
      org.opencontainers.image.description="Ahrefs Domain Rating checker – getdr serve HTTP API" \
      org.opencontainers.image.vendor="Index Pages" \
      org.opencontainers.image.url="https://index-pages.com/getdr/" \
      org.opencontainers.image.source="https://github.com/index-pages/getdr" \
      org.opencontainers.image.licenses="MIT"
# scratch has no CA bundle; HTTPS to api.ahrefs.com needs one.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/getdr /getdr
COPY --from=build --chown=65534:65534 /out/data /data
USER 65534:65534
ENV GETDR_CACHE_PATH=/data/cache.db \
    GETDR_SERVE_LISTEN=0.0.0.0:8080
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/getdr"]
CMD ["serve"]
