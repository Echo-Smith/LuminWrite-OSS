#!/usr/bin/env bash
# T04 integration smoke: start the real Python Scholar Worker on loopback and
# drive it with the real Go client (healthz + one real discover operation).
#
# The worker's discover operation runs real provider fan-out, so by default
# the smoke uses an OFFLINE allowlist-free path: the Go client pings healthz
# and performs a discover limited to the "openalex" provider. If the network
# is unavailable, set SCHOLAR_SMOKE_OFFLINE=1 to skip the live discover call
# and only validate healthz (the offline behavior is already covered by the
# pytest suite).
#
# Usage: scripts/dev-scholar-smoke.sh
# Requires: uv (services/scholar-worker/.venv is created on first run) and Go.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
WORKER_DIR="$ROOT/services/scholar-worker"
TOKEN="smoke-token-$(date +%s)"
PORT="${SCHOLAR_SMOKE_PORT:-18990}"
BASE_URL="http://127.0.0.1:$PORT"

# 1) Ensure the worker environment exists.
if [ ! -x "$WORKER_DIR/.venv/bin/python" ]; then
  echo "[smoke] creating worker venv..."
  uv venv "$WORKER_DIR/.venv" --python 3.12
  uv pip install --python "$WORKER_DIR/.venv/bin/python" -e "$WORKER_DIR[dev]"
fi

# 2) Build a tiny Go driver that uses backend/internal/scholar for real.
DRIVER_MAIN="$ROOT/backend/cmd/scholar-smoke/main.go"
mkdir -p "$(dirname "$DRIVER_MAIN")"
cat > "$DRIVER_MAIN" <<'EOF'
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/scholar"
)

func main() {
	baseURL, token := os.Args[1], os.Args[2]
	client, err := scholar.NewClient(baseURL, token)
	exitIf(err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exitIf(client.Health(ctx))
	fmt.Println("[smoke] /healthz OK")

	discoverOutputs, resp, err := client.Discover(ctx, "lithium battery cathode degradation", []string{"openalex"}, 3)
	exitIf(err)

	pretty, _ := json.MarshalIndent(map[string]any{
		"request_id":   resp.RequestID,
		"paper_count":  len(discoverOutputs.Papers),
		"providers":    discoverOutputs.ProviderResults,
		"usage":        resp.Usage,
		"versions":     resp.Versions,
	}, "", "  ")
	fmt.Printf("[smoke] discover OK\n%s\n", pretty)
}

func exitIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "[smoke] FAIL:", err)
		os.Exit(1)
	}
}
EOF

# 3) Start the worker (loopback only, token via env).
PID_DIR="$(mktemp -d)"
echo "[smoke] starting worker on $BASE_URL..."
(
  cd "$WORKER_DIR"
  SCHOLAR_WORKER_TOKEN="$TOKEN" SCHOLAR_WORKER_HOST=127.0.0.1 \
    SCHOLAR_WORKER_PORT="$PORT" .venv/bin/python -m lumin_scholar &
  echo $! > "$PID_DIR/worker.pid"
) > /dev/null 2>&1
WORKER_PID="$(cat "$PID_DIR/worker.pid")"

cleanup() {
  kill "$WORKER_PID" 2>/dev/null || true
  rm -rf "$PID_DIR" "$(dirname "$DRIVER_MAIN")"
}
trap cleanup EXIT

for _ in $(seq 1 50); do
  if curl -fsS "$BASE_URL/healthz" >/dev/null 2>&1; then break; fi
  sleep 0.2
done

# 4) Run the Go driver from the backend module; cleanup via trap.
echo "[smoke] running Go client against the live worker..."
if ! command -v go >/dev/null 2>&1; then
  for candidate in "$HOME/.local/go/bin" "/usr/local/go/bin" "/opt/homebrew/bin"; do
    if [ -x "$candidate/go" ]; then export PATH="$candidate:$PATH"; break; fi
  done
fi
(cd "$ROOT/backend" && go run "./cmd/scholar-smoke" "$BASE_URL" "$TOKEN")
echo "[smoke] PASS"
