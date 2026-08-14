# ─── Backend Dockerfile — Multi-stage Go build ───
# Build:  docker build -t luminbuddy-v2-backend ./backend
# Run:    docker run -p 8080:8080 --env-file .env.docker luminbuddy-v2-backend

# ── Stage 1: Build ──────────────────────────────────────
FROM --platform=${BUILDPLATFORM} golang:1.25-alpine AS builder

# 使用阿里云 Alpine 镜像加速 apk（国内构建从 ~30s 降至 ~5s）
RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories && \
    apk add --no-cache git ca-certificates tzdata

# Use Chinese Go module proxy for reliable downloads behind GFW
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=off

WORKDIR /build

# Cache module downloads (only re-runs when go.mod/go.sum change)
COPY --link go.mod go.sum ./
RUN go mod download

# Copy source
COPY --link . .

# Build static binary (CGO_ENABLED=0 → no libc dependency → scratch/alpine compatible)
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags='-s -w -extldflags "-static"' \
    -trimpath \
    -o /bin/server ./cmd/server/

# ── Stage 2: Runtime (minimal Alpine) ──────────────────
# golang:1.25-alpine is ~350MB; alpine:3.20 is ~7MB.
# The static binary doesn't need the Go toolchain at runtime.
FROM alpine:3.20

# 使用阿里云 Alpine 镜像加速 apk 安装
# 分两步安装：先装小包（ca-certificates tzdata wget curl），再装大包（nodejs npm）
# 这样即使 nodejs 层缓存失效，小包层仍可命中缓存，大幅加速重建
RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories && \
    apk add --no-cache ca-certificates tzdata wget curl && \
    apk add --no-cache nodejs npm && \
    rm -rf /var/cache/apk/*

ENV TZ=Asia/Shanghai

WORKDIR /app

# Copy binary from builder
COPY --link --from=builder /bin/server /app/server
COPY --link --from=builder /build/.env.example /app/.env.example

# Copy CLI init script
COPY --link scripts/init-cli.sh /app/init-cli.sh
RUN chmod +x /app/init-cli.sh

# ── Install tencent-news-cli (for news search + jiaozhen fact-checking) ──
RUN curl -fsSL https://mat1.gtimg.com/qqcdn/qqnews/cli/hub/tencent-news/setup.sh | sh || \
    echo "WARN: tencent-news-cli setup failed, CLI features will be disabled"

ENV PATH="${PATH}:/root/.tencent-news-cli/bin"

# OCI labels for image metadata
LABEL org.opencontainers.image.title="luminbuddy-v2-backend" \
      org.opencontainers.image.description="LuminBuddy V2 — Backend (Go)" \
      org.opencontainers.image.source="https://github.com/luminbuddy/luminbuddy-v2" \
      org.opencontainers.image.licenses="MIT"

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1

# Startup: init CLI API key → start server
CMD ["/bin/sh", "-c", "/app/init-cli.sh && /app/server"]
