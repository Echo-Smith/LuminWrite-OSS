#!/bin/sh
# AR-012 live sidecar acceptance (T10). Prerequisites (docs/releases/
# 2026-09-12-ar012-candidate-acceptance.md):
#   1. sidecar image built from the workspace fork:
#        (cd ../../review-sidecar && PYTHON=python3.12 ./build.sh --image)
#   2. a container from that image running with real AR_REVIEW_LLM_* model
#      credentials (workspace env.secrets.local; never stored in this repo),
#      publishing 8020 on localhost only, bind-mounting AR012_LIVE_EXCHANGE_DIR
#      as its /data (AUTORESEARCH_REVIEW_PROJECTS_DIR=/data):
#        docker rm -f ar012-live-sidecar
#        docker run -d --name ar012-live-sidecar -p 127.0.0.1:8020:8020 \
#          -v "$AR012_LIVE_EXCHANGE_DIR:/data" \
#          -e AUTORESEARCH_REVIEW_PROJECTS_DIR=/data \
#          -e AUTORESEARCH_REVIEW_DB_PATH=/data/var/scientific-review.sqlite3 \
#          -e LLM_PROVIDER=... -e LLM_API_KEY=... -e LLM_BASE_URL=... -e LLM_MODEL=... \
#          lumin-review-sidecar:local
#   3. a throwaway TEST_DATABASE_URL (dbtest creates its own database).
#
# Credentials are injected through the container environment only; this
# script and the test carry no keys.
set -eu

: "${AR012_LIVE_SIDECAR_URL:=http://127.0.0.1:8020}"
: "${AR012_LIVE_EXCHANGE_DIR:?AR012_LIVE_EXCHANGE_DIR is required (same host dir the sidecar mounts as /data)}"
: "${TEST_DATABASE_URL:?TEST_DATABASE_URL is required}"

# Start from a clean exchange root so a stale sidecar ledger cannot mask a
# fresh run; the sidecar's SQLite lives under the mounted volume.
mkdir -p "$AR012_LIVE_EXCHANGE_DIR"
find "$AR012_LIVE_EXCHANGE_DIR" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
docker restart ar012-live-sidecar >/dev/null 2>&1 || true

cd "$(dirname "$0")/../backend"
exec go test ./internal/server \
  -run '^TestArReviewLiveSidecarEndToEnd$' \
  -count=1 -v -timeout 60m
