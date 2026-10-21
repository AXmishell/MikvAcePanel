# syntax=docker/dockerfile:1

FROM node:22-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS go-build
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X github.com/axmishell/mikvacepanel/internal/version.Version=${VERSION} -X github.com/axmishell/mikvacepanel/internal/version.Commit=${COMMIT} -X github.com/axmishell/mikvacepanel/internal/version.BuildDate=${BUILD_DATE}" \
    -o /out/mikvace-server ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata wget
COPY --from=go-build /out/mikvace-server /usr/local/bin/mikvace-server
ENV ADDR=:8080 \
    DATA_DIR=/data \
    PANEL_PUBLIC_URL=http://localhost:8080
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/mikvace-server"]
