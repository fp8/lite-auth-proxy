# -- Build stage --
# Debian 13 (trixie) to match the distroless static-debian13 runtime base
FROM golang:1.24-trixie AS builder

# Build arguments
ARG VERSION=0.0.1

# Install dependencies
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy go mod files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Cross-compilation target — populated automatically by BuildKit per platform
# (e.g. linux/amd64, linux/arm64). Declared WITHOUT defaults: a default value
# shadows the auto-injected platform value and would mislabel the binary.
ARG TARGETOS
ARG TARGETARCH

# Build the flex binary (all plugins)
# CGO_ENABLED=0: Pure Go binary (no C dependencies)
# GOOS/GOARCH: target OS/arch from buildx (TARGETOS/TARGETARCH)
# -ldflags: Strip debug info + inject version
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /flex-auth-proxy ./cmd/flex

# -- Runtime stage --
# Use distroless base image for minimal attack surface
# No shell, no package manager, only the binary and required files
FROM gcr.io/distroless/static-debian13:nonroot

# Build arguments (re-declared for this stage)
ARG VERSION=0.0.1

# Image metadata
LABEL org.opencontainers.image.version=${VERSION}

# Copy the compiled binary from builder
COPY --from=builder /flex-auth-proxy /flex-auth-proxy

# Copy default configuration (optional; can be overridden via mount/env)
COPY config/config-flex.toml /config/config.toml

# Expose the default listening port
EXPOSE 8888

# Use JSON entrypoint format to avoid shell interpretation
ENTRYPOINT ["/flex-auth-proxy"]
