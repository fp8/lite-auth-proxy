# -- Build stage --
FROM golang:1.24-alpine AS builder

# Build arguments
ARG VERSION=0.0.1

# Install dependencies
RUN apk add --no-cache ca-certificates git

WORKDIR /app

# Copy go mod files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
# CGO_ENABLED=0: Pure Go binary (no C dependencies)
# GOOS=linux GOARCH=amd64: Linux x86-64 target
# -ldflags: Strip debug info + inject version
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /lite-auth-proxy ./cmd/proxy

# -- Runtime stage --
# Use distroless base image for minimal attack surface
# No shell, no package manager, only the binary and required files
FROM gcr.io/distroless/static-debian12:nonroot

# Copy the compiled binary from builder
COPY --from=builder /lite-auth-proxy /lite-auth-proxy

# Copy default configuration (optional; can be overridden via mount/env)
COPY configs/config.toml /configs/config.toml

# Expose the default listening port
EXPOSE 8888

# Use JSON entrypoint format to avoid shell interpretation
ENTRYPOINT ["/lite-auth-proxy"]

# Default runtime arguments
CMD ["-config", "/configs/config.toml"]
