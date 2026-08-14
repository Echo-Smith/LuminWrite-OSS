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

# ── Stage 2: Runtime (Nginx Unprivileged) ─────────────
# nginxinc/nginx-unprivileged runs as non-root (UID 101) for better security.
# Listens on port 8080 by default (not 80).
FROM nginxinc/nginx-unprivileged:alpine

# Switch to root to remove default files and copy new ones
USER root

# Remove default nginx static assets and config
RUN rm -rf /usr/share/nginx/html/* /etc/nginx/conf.d/default.conf

# Copy built assets from builder (root-path deployment)
COPY --link --from=builder /build/dist /usr/share/nginx/html

# Copy custom nginx config (must listen on 8080 for unprivileged image)
COPY --link nginx.conf /etc/nginx/conf.d/default.conf

# Ensure non-root user owns the files
RUN chown -R 101:101 /usr/share/nginx/html /etc/nginx/conf.d/default.conf

# Switch back to non-root user (UID 101)
USER 101

# OCI labels
LABEL org.opencontainers.image.title="luminbuddy-v2-frontend" \
      org.opencontainers.image.description="LuminBuddy V2 — Frontend (React + Nginx)" \
      org.opencontainers.image.source="https://github.com/luminbuddy/luminbuddy-v2" \
      org.opencontainers.image.licenses="MIT"

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=3s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/health || exit 1

CMD ["nginx", "-g", "daemon off;"]
