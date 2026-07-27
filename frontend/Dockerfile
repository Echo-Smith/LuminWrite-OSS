# ─── Frontend Dockerfile — Multi-stage build (Vite + Nginx) ───

# Stage 1: Build
FROM node:22-alpine AS builder

WORKDIR /build

# Cache npm install
COPY package.json package-lock.json* ./
RUN npm ci --ignore-scripts --legacy-peer-deps || npm install --legacy-peer-deps

# Copy source and build
COPY . .
RUN npm run build

# ──────────────────────────────────────────────

# Stage 2: Runtime (Nginx)
FROM nginx:alpine

# Remove default nginx static assets
RUN rm -rf /usr/share/nginx/html/*

# Copy built assets
COPY --from=builder /build/dist /usr/share/nginx/html

# Copy nginx config
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

HEALTHCHECK --interval=10s --timeout=3s --start-period=3s --retries=3 \
    CMD wget -qO- http://127.0.0.1/health || exit 1

CMD ["nginx", "-g", "daemon off;"]
