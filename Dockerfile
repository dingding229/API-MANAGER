FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/server ./cmd/server
COPY internal ./internal
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/api-manager ./cmd/server && mkdir -p /out/plugins /out/plugin-library /out/observability

# The upstream executables are copied from their official, version-pinned images.
# Alloy's binary needs glibc, so its Ubuntu-based image provides the runtime.
FROM grafana/loki:3.7.8@sha256:1107dd5274e0ada47e42472b7a7e71f3b2a2fe878878108f3e2f9e51528f0193 AS loki
FROM grafana/tempo:2.10.8@sha256:f0561deb1c68ec44d6e6e7e4487f30106c4e5e768642077695b37958b105812a AS tempo
FROM prom/prometheus:v3.15.0@sha256:efd719c99d83b060d9daefdcf00360461adf279f45ef5391f8d111892118753e AS prometheus
FROM prom/alertmanager:v0.34.1@sha256:e9733bafb1bdef9b00e25a21f8f99dc26a22224bf16641ad754d1649f4c3357a AS alertmanager
FROM grafana/grafana:12.4.11@sha256:3ea272e5cab64a4a62240c682e2c62433b25614d956d44c299a10cb6994f6f2e AS grafana
# Keep the Grafana executable once; the application starts /usr/local/bin/grafana.
FROM grafana AS grafana-assets
USER root
RUN rm -f /usr/share/grafana/bin/grafana
FROM grafana/alloy:v1.19.2@sha256:b8ec653c44235fbe910879145dac3597d66b0aaecf60bcbbe82580767771a839
COPY --from=loki /usr/bin/loki /usr/local/bin/loki
COPY --from=tempo /tempo /usr/local/bin/tempo
COPY --from=prometheus /bin/prometheus /usr/local/bin/prometheus
COPY --from=alertmanager /bin/alertmanager /usr/local/bin/alertmanager
COPY --from=grafana-assets /usr/share/grafana /usr/share/grafana
COPY --from=grafana /etc/grafana/grafana.ini /etc/grafana/grafana.ini
COPY --from=grafana /usr/share/grafana/bin/grafana /usr/local/bin/grafana
RUN groupadd -g 65532 api-manager && useradd -u 65532 -g api-manager -M -s /usr/sbin/nologin api-manager \
    && ln -s /bin/alloy /usr/local/bin/alloy
COPY --from=builder --chown=65532:65532 /out/api-manager /api-manager
COPY --from=builder --chown=65532:65532 /out/plugins /data/plugins
COPY --from=builder --chown=65532:65532 /out/plugin-library /data/plugin-library
COPY --from=builder --chown=65532:65532 /out/observability /data/observability
USER 65532:65532
EXPOSE 8080 3000
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s CMD ["/api-manager", "--healthcheck"]
ENTRYPOINT ["/api-manager"]
