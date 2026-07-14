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
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata wget

ENV TZ=Asia/Shanghai

WORKDIR /app

COPY --from=builder /bin/server /app/server
COPY --from=builder /build/.env.example /app/.env.example

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1

CMD ["/app/server"]
