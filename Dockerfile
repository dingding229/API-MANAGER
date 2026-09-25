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
FROM grafana/loki:3.5.5@sha256:31628519045e7f28692a7ae73b4a3fd293dccb425585ed5d16ceea3b5c9592e6 AS loki
FROM grafana/tempo:2.8.2@sha256:0ef775495967cd5d7a6b2e146b6ea695d624803c8db8349fb8ce4164f719f9b7 AS tempo
FROM prom/prometheus:v3.6.0@sha256:76947e7ef22f8a698fc638f706685909be425dbe09bd7a2cd7aca849f79b5f64 AS prometheus
FROM prom/alertmanager:v0.28.1@sha256:27c475db5fb156cab31d5c18a4251ac7ed567746a2483ff264516437a39b15ba AS alertmanager
FROM grafana/grafana:12.2.0@sha256:74144189b38447facf737dfd0f3906e42e0776212bf575dc3334c3609183adf7 AS grafana
FROM grafana/alloy:v1.10.2@sha256:bcf27f18c4402869af112fb39e35e1db3804a404686f4caa20bdf77814219223
COPY --from=loki /usr/bin/loki /usr/local/bin/loki
COPY --from=tempo /tempo /usr/local/bin/tempo
COPY --from=prometheus /bin/prometheus /usr/local/bin/prometheus
COPY --from=alertmanager /bin/alertmanager /usr/local/bin/alertmanager
COPY --from=grafana /usr/share/grafana /usr/share/grafana
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
