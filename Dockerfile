# syntax=docker/dockerfile:1.7
#
# Multi-stage build for Gopher-Ops. Final image is distroless: no shell, no
# package manager, ~20 MB. Build args BUILDPLATFORM/TARGETPLATFORM are wired
# by `docker buildx build --platform linux/amd64,linux/arm64`.

# ---------- builder ----------
FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS builder

# Cache module downloads in a layer of their own.
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy the rest and build for the target architecture.
COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/gopher-ops ./cmd

# ---------- runtime ----------
FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="gopher-ops"
LABEL org.opencontainers.image.description="AI-driven SRE ChatOps bot for Docker + Kubernetes (Telegram-first)"
LABEL org.opencontainers.image.source="https://github.com/yourusername/gopher-ops"
LABEL org.opencontainers.image.licenses="MIT"

# Run as nonroot (UID 65532). Healthcheck dir + state.json/audit.log live in
# /data which the user should bind-mount.
WORKDIR /data
COPY --from=builder /out/gopher-ops /usr/local/bin/gopher-ops

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/gopher-ops"]
