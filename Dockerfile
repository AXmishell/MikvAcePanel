# syntax=docker/dockerfile:1

# The build stages always run on the native builder platform and cross-compile
# for the requested target (CGO is disabled, so no emulation is needed). For
# multi-arch builds this keeps npm and the Go compiler off QEMU: an arm64 build
# on an amd64 runner is roughly an order of magnitude faster.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS go-build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
WORKDIR /src
# Module and compiler caches are reused across builds on the same builder, so a
# source change only recompiles what actually changed.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
COPY --from=web-build /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 \
    GOOS="${TARGETOS:-$(go env GOOS)}" \
    GOARCH="${TARGETARCH:-$(go env GOARCH)}" \
    go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X github.com/axmishell/mikvacepanel/internal/version.Version=${VERSION} -X github.com/axmishell/mikvacepanel/internal/version.Commit=${COMMIT} -X github.com/axmishell/mikvacepanel/internal/version.BuildDate=${BUILD_DATE}" \
    -o /out/mikvace-server ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata wget
COPY --from=go-build /out/mikvace-server /usr/local/bin/mikvace-server
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/mikvace-server"]
