# ─── Frontend Dockerfile — Multi-stage build (Vite + Nginx) ───
# Build:  docker build -t luminbuddy-v2-frontend ./frontend
# Run:    docker run -p 3000:8080 luminbuddy-v2-frontend

# ── Stage 1: Build (Node + Vite) ───────────────────────
FROM --platform=${BUILDPLATFORM} node:22-alpine AS builder

WORKDIR /build

# Cache npm install (only re-runs when package.json changes)
# 使用淘宝 npm 镜像加速国内构建
COPY --link package.json package-lock.json* ./
RUN npm config set registry https://registry.npmmirror.com && \
    (npm ci --ignore-scripts --legacy-peer-deps || npm install --legacy-peer-deps)

# Copy source and build
COPY --link . .
RUN npm run build

# ── Stage 2: Runtime (Nginx) ──────────────────────────
# 使用标准 nginx:alpine 镜像
# PID 放在 /var/run/（root 可写），监听 8080 非特权端口
FROM nginx:alpine

# Remove default nginx static assets and config
RUN rm -rf /usr/share/nginx/html/* /etc/nginx/conf.d/default.conf

# Copy built assets from builder (root-path deployment)
COPY --link --from=builder /build/dist /usr/share/nginx/html

# Copy custom nginx main config (replaces default, sets PID to /var/run/)
COPY --link nginx-main.conf /etc/nginx/nginx.conf

# Copy custom server config (must listen on 8080)
COPY --link nginx.conf /etc/nginx/conf.d/default.conf

# Ensure nginx owns the static files
RUN chown -R nginx:nginx /usr/share/nginx/html /var/cache/nginx

# Validate nginx config syntax at build time (fail fast)
RUN nginx -t

# OCI labels
LABEL org.opencontainers.image.title="luminbuddy-v2-frontend" \
      org.opencontainers.image.description="LuminBuddy V2 — Frontend (React + Nginx)" \
      org.opencontainers.image.source="https://github.com/luminbuddy/luminbuddy-v2" \
      org.opencontainers.image.licenses="MIT"

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=3s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/health || exit 1

CMD ["nginx", "-g", "daemon off;"]
