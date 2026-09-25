FROM --platform=$BUILDPLATFORM golang:1.26.7-alpine@sha256:28d89ee9cc0ff9fec75c82ca201e6bf7fdf9a679d4b7b24dfa04f2bb766bb468 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/server ./cmd/server
COPY internal ./internal
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/api-manager ./cmd/server && mkdir -p /out/plugins /out/plugin-library /out/observability

# Rebuild upstream binaries from immutable source archives with patched Go modules.
# The downloaded tarballs and the Alertmanager UI release asset are SHA-256 checked.
FROM --platform=$BUILDPLATFORM golang:1.26.7-bookworm@sha256:e8c859f5632dcfde7b32d2012b4351728f6437930887c2f6a91ea242459e5514 AS patched-base
ARG TARGETOS TARGETARCH

FROM patched-base AS patched-alertmanager
RUN curl -fsSL --retry 3 https://codeload.github.com/prometheus/alertmanager/tar.gz/73c6bfe7393929211294c1954f30d8ed78e4d0ad -o /tmp/source.tar.gz \
    && echo '5c5c72ec6b4c4746d523f2ed1994dfec4b94f7457e530ee6fb16cfec158e545e  /tmp/source.tar.gz' | sha256sum -c - \
    && mkdir -p /src && tar -xzf /tmp/source.tar.gz --strip-components=1 -C /src \
    && curl -fsSL --retry 3 https://github.com/prometheus/alertmanager/releases/download/v0.34.1/alertmanager-web-ui-0.34.1.tar.gz -o /tmp/ui.tar.gz \
    && echo '881fe0f7beb9573f35be47dd233af6220e72c8db72c03bcd1780402eb8dc1307  /tmp/ui.tar.gz' | sha256sum -c - \
    && mkdir -p /src/ui/app && tar -xzf /tmp/ui.tar.gz -C /src/ui/app
WORKDIR /src
RUN --mount=type=cache,target=/go/pkg/mod go get google.golang.org/grpc@v1.83.2
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -buildvcs=false -trimpath -ldflags='-s -w' -o /out/alertmanager ./cmd/alertmanager

FROM patched-base AS patched-tempo
COPY --from=patched-alertmanager /out/alertmanager /tmp/build-order/alertmanager
RUN curl -fsSL --retry 3 https://codeload.github.com/grafana/tempo/tar.gz/f0f3ed59197bfe9f54f3b0f8015ccca112f9e544 -o /tmp/source.tar.gz \
    && echo 'c354a7495843161a41ad75d96dd4e8f17aabc9a5f439060cf974a57b4ef2ff85  /tmp/source.tar.gz' | sha256sum -c - \
    && mkdir -p /src && tar -xzf /tmp/source.tar.gz --strip-components=1 -C /src
WORKDIR /src
RUN --mount=type=cache,target=/go/pkg/mod go get google.golang.org/grpc@v1.83.2 github.com/apache/thrift@v0.24.0 golang.org/x/crypto@v0.55.0
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod GOFLAGS=-mod=mod CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -buildvcs=false -trimpath -ldflags='-s -w' -o /out/tempo ./cmd/tempo

FROM patched-base AS alloy-source
COPY --from=patched-tempo /out/tempo /tmp/build-order/tempo
RUN curl -fsSL --retry 3 https://codeload.github.com/grafana/alloy/tar.gz/becfd489a7bb459c0496893b555fb87a003296b1 -o /tmp/source.tar.gz \
    && echo '69efdb87a91bb538323f5ccb6bcd86240cee3a78ee97632bd1005c434344c7f1  /tmp/source.tar.gz' | sha256sum -c - \
    && mkdir -p /src && tar -xzf /tmp/source.tar.gz --strip-components=1 -C /src
FROM --platform=$BUILDPLATFORM node:24-bookworm-slim@sha256:0e0ff40c39bc087845bfb27465a0df4ea419520094bc35842ff83dd8cbe6f9b6 AS alloy-ui
COPY --from=alloy-source /src/internal/web/ui /ui
WORKDIR /ui
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund && npm run build

FROM alloy-source AS patched-alloy
WORKDIR /src
RUN --mount=type=cache,target=/go/pkg/mod go get google.golang.org/grpc@v1.83.2 && cd collector && go get google.golang.org/grpc@v1.83.2
COPY --from=alloy-ui /ui/dist /src/internal/web/ui/dist
WORKDIR /src/collector
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod GOFLAGS=-mod=mod CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -buildvcs=false -trimpath -tags='netgo embedalloyui' -ldflags='-s -w' -o /out/alloy .

FROM patched-base AS patched-grafana
COPY --from=patched-alloy /out/alloy /tmp/build-order/alloy
RUN curl -fsSL --retry 3 https://codeload.github.com/grafana/grafana/tar.gz/05757e789657299d00314f8f96d49d1aca569f33 -o /tmp/source.tar.gz \
    && echo '07622e9c2b67eded2a9c2ad52d8d8e26cb6f17e594152046f447302aed975ef1  /tmp/source.tar.gz' | sha256sum -c - \
    && mkdir -p /src && tar -xzf /tmp/source.tar.gz --strip-components=1 -C /src
WORKDIR /src
# Tempo v2.10.8 fixes CVE-2026-21728 and CVE-2026-28377. Go records its
# pseudo-version (v1.5.1-0...) as a dependency; see security/tempo-vex.json.
RUN --mount=type=cache,target=/go/pkg/mod set -e; \
    for attempt in 1 2 3; do \
      if go get github.com/grafana/tempo@f0f3ed59197bfe9f54f3b0f8015ccca112f9e544 github.com/apache/thrift@v0.24.0; then break; fi; \
      if [ "$attempt" = 3 ]; then exit 1; fi; \
      sleep 5; \
    done
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -buildvcs=false -trimpath -ldflags='-s -w -X main.version=12.4.11' -o /out/grafana ./pkg/cmd/grafana

# Official images remain sources for Loki, Prometheus, Grafana static assets,
# and Alloy's glibc runtime. The rebuilt binaries above replace vulnerable ones.
FROM grafana/loki:3.7.8@sha256:1107dd5274e0ada47e42472b7a7e71f3b2a2fe878878108f3e2f9e51528f0193 AS loki
FROM prom/prometheus:v3.15.0@sha256:efd719c99d83b060d9daefdcf00360461adf279f45ef5391f8d111892118753e AS prometheus
FROM grafana/grafana:12.4.11@sha256:3ea272e5cab64a4a62240c682e2c62433b25614d956d44c299a10cb6994f6f2e AS grafana
# Keep the Grafana executable once; the application starts /usr/local/bin/grafana.
FROM grafana AS grafana-assets
USER root
RUN rm -f /usr/share/grafana/bin/grafana
FROM grafana/alloy:v1.19.2@sha256:b8ec653c44235fbe910879145dac3597d66b0aaecf60bcbbe82580767771a839
COPY --from=loki /usr/bin/loki /usr/local/bin/loki
COPY --from=patched-tempo /out/tempo /usr/local/bin/tempo
COPY --from=prometheus /bin/prometheus /usr/local/bin/prometheus
COPY --from=patched-alertmanager /out/alertmanager /usr/local/bin/alertmanager
COPY --from=grafana-assets /usr/share/grafana /usr/share/grafana
COPY --from=grafana /etc/grafana/grafana.ini /etc/grafana/grafana.ini
COPY --from=patched-grafana /out/grafana /usr/local/bin/grafana
RUN groupadd -g 65532 api-manager && useradd -u 65532 -g api-manager -M -s /usr/sbin/nologin api-manager \
    && ln -s /bin/alloy /usr/local/bin/alloy
COPY --from=patched-alloy /out/alloy /bin/alloy
COPY --from=builder --chown=65532:65532 /out/api-manager /api-manager
COPY --from=builder --chown=65532:65532 /out/plugins /data/plugins
COPY --from=builder --chown=65532:65532 /out/plugin-library /data/plugin-library
COPY --from=builder --chown=65532:65532 /out/observability /data/observability
USER 65532:65532
EXPOSE 8080 3000
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s CMD ["/api-manager", "--healthcheck"]
ENTRYPOINT ["/api-manager"]
