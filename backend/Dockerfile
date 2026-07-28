# ─── Backend Dockerfile — Multi-stage Go build ───

# Stage 1: Build
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

# Cache module downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags='-s -w -extldflags "-static"' \
    -o /bin/server ./cmd/server/

# ──────────────────────────────────────────────

# Stage 2: Runtime
FROM golang:1.25-alpine

# Install runtime dependencies:
# - ca-certificates, tzdata, wget: basic infra
# - nodejs, npm: required for IMA skill scripts (ima_api.cjs)
# - curl: required for tencent-news-cli setup script
RUN apk --no-cache add ca-certificates tzdata wget nodejs npm curl

ENV TZ=Asia/Shanghai

WORKDIR /app

# Copy binary
COPY --from=builder /bin/server /app/server
COPY --from=builder /build/.env.example /app/.env.example

# Copy CLI init script
COPY scripts/init-cli.sh /app/init-cli.sh
RUN chmod +x /app/init-cli.sh

# ── Install tencent-news-cli (for news search + jiaozhen fact-checking) ──
# The setup script downloads the correct binary for the platform/arch.
RUN curl -fsSL https://mat1.gtimg.com/qqcdn/qqnews/cli/hub/tencent-news/setup.sh | sh || \
    echo "WARN: tencent-news-cli setup failed, CLI features will be disabled"

# Make CLI available on PATH
ENV PATH="${PATH}:/root/.tencent-news-cli/bin"

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1

# Startup: init CLI API key then start server
CMD ["/bin/sh", "-c", "/app/init-cli.sh && /app/server"]
