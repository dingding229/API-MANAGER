FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/server ./cmd/server
COPY internal ./internal
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/api-manager ./cmd/server && mkdir -p /out/plugins /out/plugin-library

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=builder --chown=65532:65532 /out/api-manager /api-manager
COPY --from=builder --chown=65532:65532 /out/plugins /data/plugins
COPY --from=builder --chown=65532:65532 /out/plugin-library /data/plugin-library
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s CMD ["/api-manager", "--healthcheck"]
ENTRYPOINT ["/api-manager"]
