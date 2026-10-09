FROM --platform=$BUILDPLATFORM node:24-bookworm-slim@sha256:0e0ff40c39bc087845bfb27465a0df4ea419520094bc35842ff83dd8cbe6f9b6 AS public-ui-build
WORKDIR /ui
ENV NEXT_TELEMETRY_DISABLED=1
COPY public-ui/package.json public-ui/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --ignore-scripts --no-audit --no-fund
COPY public-ui ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26.9-alpine@sha256:cdfd4fe2da6b225d8b40c6b7a105736e548e83ff56d5d8f9394446eeb5eb84e0 AS builder
WORKDIR /src
ENV GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/server ./cmd/server
COPY internal ./internal
ARG TARGETOS TARGETARCH
ARG APP_VERSION=0.3.49-dev
ARG APP_REVISION=development
ARG APP_BUILT_AT
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X api-manager/internal/version.Version=${APP_VERSION} -X api-manager/internal/version.Revision=${APP_REVISION} -X api-manager/internal/version.BuiltAt=${APP_BUILT_AT}" -o /out/api-manager ./cmd/server && mkdir -p /out/plugins /out/plugin-library /out/observability /out/database-backups

# Rebuild upstream binaries from immutable source archives with patched Go modules.
# The downloaded tarballs and the Alertmanager UI release asset are SHA-256 checked.
FROM --platform=$BUILDPLATFORM golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61 AS patched-base
ARG TARGETOS TARGETARCH
ENV GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS=-p=2

FROM patched-base AS patched-alertmanager
RUN curl -fsSL --retry 3 https://codeload.github.com/prometheus/alertmanager/tar.gz/73c6bfe7393929211294c1954f30d8ed78e4d0ad -o /tmp/source.tar.gz \
    && echo '5c5c72ec6b4c4746d523f2ed1994dfec4b94f7457e530ee6fb16cfec158e545e  /tmp/source.tar.gz' | sha256sum -c - \
    && mkdir -p /src && tar -xzf /tmp/source.tar.gz --strip-components=1 -C /src \
    && curl -fsSL --retry 3 https://github.com/prometheus/alertmanager/releases/download/v0.34.1/alertmanager-web-ui-0.34.1.tar.gz -o /tmp/ui.tar.gz \
    && echo '881fe0f7beb9573f35be47dd233af6220e72c8db72c03bcd1780402eb8dc1307  /tmp/ui.tar.gz' | sha256sum -c - \
    && mkdir -p /src/ui/app && tar -xzf /tmp/ui.tar.gz -C /src/ui/app
WORKDIR /src
RUN --mount=type=cache,target=/go/pkg/mod go get google.golang.org/grpc@v1.83.2 golang.org/x/net@v0.60.0
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -buildvcs=false -trimpath -ldflags='-s -w' -o /out/alertmanager ./cmd/alertmanager

FROM patched-base AS patched-tempo
COPY --from=patched-alertmanager /out/alertmanager /tmp/build-order/alertmanager
RUN curl -fsSL --retry 3 https://codeload.github.com/grafana/tempo/tar.gz/f0f3ed59197bfe9f54f3b0f8015ccca112f9e544 -o /tmp/source.tar.gz \
    && echo 'c354a7495843161a41ad75d96dd4e8f17aabc9a5f439060cf974a57b4ef2ff85  /tmp/source.tar.gz' | sha256sum -c - \
    && mkdir -p /src && tar -xzf /tmp/source.tar.gz --strip-components=1 -C /src
WORKDIR /src
RUN --mount=type=cache,target=/go/pkg/mod go get google.golang.org/grpc@v1.83.2 golang.org/x/net@v0.60.0 github.com/apache/thrift@v0.24.0 golang.org/x/crypto@v0.55.0
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod GOFLAGS='-p=2 -mod=mod' CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
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
RUN --mount=type=cache,target=/go/pkg/mod set -e; \
    for attempt in 1 2 3; do \
      if go get google.golang.org/grpc@v1.83.2 golang.org/x/net@v0.60.0 && (cd collector && go get google.golang.org/grpc@v1.83.2 golang.org/x/net@v0.60.0); then break; fi; \
      if [ "$attempt" = 3 ]; then exit 1; fi; \
      sleep 5; \
    done
COPY --from=alloy-ui /ui/dist /src/internal/web/ui/dist
WORKDIR /src/collector
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod set -e; \
    for attempt in 1 2 3; do \
      if GOFLAGS='-p=2 -mod=mod' CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -buildvcs=false -trimpath -tags='netgo embedalloyui' -ldflags='-s -w' -o /out/alloy .; then break; fi; \
      if [ "$attempt" = 3 ]; then exit 1; fi; \
      sleep 5; \
    done

# Rebuild the remaining components with the same patched toolchain; never copy an old Go runtime.
FROM patched-base AS patched-loki
COPY --from=patched-alloy /out/alloy /tmp/build-order/alloy
RUN curl -fsSL --retry 3 https://codeload.github.com/grafana/loki/tar.gz/09e6ce2ff1bdc19763a10265b870c86f51c98655 -o /tmp/source.tar.gz \
    && echo '44c1433b8c5e9638708e62644aa43a746f991206acef5889eaec620a895813b8  /tmp/source.tar.gz' | sha256sum -c - \
    && mkdir -p /src && tar -xzf /tmp/source.tar.gz --strip-components=1 -C /src
WORKDIR /src
RUN --mount=type=cache,target=/go/pkg/mod go get golang.org/x/net@v0.60.0
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod GOFLAGS='-p=2 -mod=mod' CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -buildvcs=false -trimpath -tags=netgo -ldflags='-s -w -X github.com/grafana/loki/v3/pkg/util/build.Version=3.7.8 -X github.com/grafana/loki/v3/pkg/util/build.Revision=09e6ce2ff1bdc19763a10265b870c86f51c98655' -o /out/loki ./cmd/loki

FROM patched-base AS patched-prometheus
COPY --from=patched-loki /out/loki /tmp/build-order/loki
RUN curl -fsSL --retry 3 https://codeload.github.com/prometheus/prometheus/tar.gz/5241a27fe3c6983549fccc32f6e65917408c63cd -o /tmp/source.tar.gz \
    && echo 'eee7ec029ffdba7d9ee59171cad5ddaff10e71514b0bae1a6fde6dc1ae99e181  /tmp/source.tar.gz' | sha256sum -c - \
    && mkdir -p /src && tar -xzf /tmp/source.tar.gz --strip-components=1 -C /src \
    && curl -fsSL --retry 3 https://github.com/prometheus/prometheus/releases/download/v3.15.0/prometheus-web-ui-3.15.0.tar.gz -o /tmp/ui.tar.gz \
    && echo 'fa8c918ed05e3f89e232e9b69eccd6ed074602cd9e8a2cab7a92bec9651974ae  /tmp/ui.tar.gz' | sha256sum -c - \
    && tar -xzf /tmp/ui.tar.gz -C /src/web/ui
WORKDIR /src
RUN --mount=type=cache,target=/go/pkg/mod go get golang.org/x/net@v0.60.0
RUN scripts/compress_assets.sh
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -buildvcs=false -trimpath -tags=netgo,builtinassets -ldflags='-s -w -X github.com/prometheus/common/version.Version=3.15.0 -X github.com/prometheus/common/version.Revision=5241a27fe3c6983549fccc32f6e65917408c63cd' -o /out/prometheus ./cmd/prometheus

# A fresh final filesystem: only the five monitoring binaries are copied in.
FROM ubuntu:24.04@sha256:008173c23f95b170204355c12626cb5a965d779a7e1283b09e9cffbb1bf33ca3 AS runtime-base
RUN apt-get -o Acquire::Retries=3 update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata passwd \
    && apt-get clean \
    && find /var/lib/apt/lists -type f -delete \
    && groupadd -g 65532 api-manager \
    && useradd -u 65532 -g api-manager -M -s /usr/sbin/nologin api-manager \
    && rm -rf /usr/share/doc /usr/share/man /usr/share/info /usr/share/lintian \
       /usr/lib/apt /var/cache/apt /var/log/* \
    && rm -f /usr/bin/apt /usr/bin/apt-* /usr/sbin/useradd /usr/sbin/userdel /usr/sbin/usermod \
       /usr/sbin/groupadd /usr/sbin/groupdel /usr/sbin/groupmod \
    && find /usr /bin /sbin -xdev -type f -perm /6000 -exec chmod a-s {} +

# Flatten the prepared root filesystem so removed base packages do not remain in lower layers.
# Preserve glibc, the POSIX shell, tar, CA roots and all timezones for monitoring/backup/Helm.
FROM scratch AS runtime
COPY --from=runtime-base / /
ENV PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
COPY --from=patched-loki /out/loki /usr/local/bin/loki
COPY --from=patched-tempo /out/tempo /usr/local/bin/tempo
COPY --from=patched-prometheus /out/prometheus /usr/local/bin/prometheus
COPY --from=patched-alertmanager /out/alertmanager /usr/local/bin/alertmanager
COPY --from=patched-alloy /out/alloy /usr/local/bin/alloy
COPY --from=builder --chown=65532:65532 /out/api-manager /api-manager
COPY --from=builder --chown=65532:65532 /out/plugins /data/plugins
COPY --from=builder --chown=65532:65532 /out/plugin-library /data/plugin-library
COPY --from=builder --chown=65532:65532 /out/observability /data/observability
COPY --from=builder --chown=65532:65532 /out/database-backups /data/database-backups
COPY --from=public-ui-build /ui/out /usr/share/api-manager/public-ui
COPY public-ui/LICENSE.fumadocs /usr/share/api-manager/LICENSE.fumadocs
COPY public-ui/LICENSE.octicons /usr/share/api-manager/LICENSE.octicons
COPY public-ui/LICENSE.oauth-marks /usr/share/api-manager/LICENSE.oauth-marks
ENV PUBLIC_UI_DIR=/usr/share/api-manager/public-ui
ENV HTTP_ADDR=:8080
ENV ADMIN_PATH=/admin
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s CMD ["/api-manager", "--healthcheck"]
ENTRYPOINT ["/api-manager"]
