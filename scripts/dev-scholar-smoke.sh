#!/usr/bin/env bash
# T03 integration smoke: start the real Python Scholar Worker on loopback and
# drive it with the real Go client (healthz + one mock discover operation).
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
# The driver lives under backend/cmd so it may import the internal package.
DRIVER_DIR="$(mktemp -d)"
DRIVER_MAIN="$ROOT/backend/cmd/scholar-smoke/main.go"
trap 'kill "$WORKER_PID" 2>/dev/null || true; rm -rf "$DRIVER_DIR"' EXIT
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exitIf(client.Health(ctx))
	fmt.Println("[smoke] /healthz OK")

	payload := map[string]any{"query": "smoke check", "limit": 2}
	hash, err := scholar.HashPayload(payload)
	exitIf(err)

	resp, err := client.Call(ctx, scholar.OpDiscover, hash, payload)
	exitIf(err)

	var outputs struct {
		Records []json.RawMessage `json:"records"`
	}
	exitIf(json.Unmarshal(resp.Outputs, &outputs))

	pretty, _ := json.MarshalIndent(map[string]any{
		"request_id":   resp.RequestID,
		"record_count": len(outputs.Records),
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
echo "[smoke] starting worker on $BASE_URL..."
(
  cd "$WORKER_DIR"
  SCHOLAR_WORKER_TOKEN="$TOKEN" SCHOLAR_WORKER_HOST=127.0.0.1 \
    SCHOLAR_WORKER_PORT="$PORT" .venv/bin/python -m lumin_scholar &
  echo $! > "$DRIVER_DIR/worker.pid"
) > /dev/null 2>&1
WORKER_PID="$(cat "$DRIVER_DIR/worker.pid")"

for _ in $(seq 1 50); do
  if curl -fsS "$BASE_URL/healthz" >/dev/null 2>&1; then break; fi
  kill -0 "$WORKER_PID" 2>/dev/null || { echo "[smoke] worker exited early"; exit 1; }
  sleep 0.2
done

# 4) Run the Go driver from the backend module, then clean it up.
echo "[smoke] running Go client against the live worker..."
(cd "$ROOT/backend" && go run "./cmd/scholar-smoke" "$BASE_URL" "$TOKEN")
rm -rf "$(dirname "$DRIVER_MAIN")"
echo "[smoke] PASS"
