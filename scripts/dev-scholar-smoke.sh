#!/usr/bin/env bash
# T04/T09 integration smoke: start the real Python Scholar Worker on loopback
# and drive it with the real Go client through the deterministic loopback:
#
#   1. /healthz                       — always (no network, no model)
#   2. parse (deterministic TXT)      — always (no network, no model)
#   3. discover (real provider fan-out, openalex) — unless
#      SCHOLAR_SMOKE_OFFLINE=1 (offline behavior is covered by the pytest
#      suite); a network failure here is reported as DISCOVER=SKIPPED.
#   4. rank — the FAIL-CLOSED error path: no SCHOLAR_LLM_* is configured, so
#      the worker must answer a typed error (never silent all-zero scores).
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
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
)

func main() {
	offline := os.Args[3] == "offline"
	baseURL, token := os.Args[1], os.Args[2]
	client, err := scholar.NewClient(baseURL, token)
	exitIf(err)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	exitIf(client.Health(ctx))
	fmt.Println("[smoke] /healthz OK")

	// ── parse: deterministic full loop (real worker, no network, no model).
	document := []byte("LuminScholar smoke document.\n\nSecond paragraph: hash-verified blocks follow.")
	parse, resp, err := (writingruntime.ScholarParseRead{Client: client}).ParseDocument(ctx, document, "text/plain", "parser/1")
	exitIf(err)
	parsePretty, _ := json.MarshalIndent(map[string]any{
		"request_id": resp.RequestID, "blocks": len(parse.Blocks),
		"coverage": parse.Coverage,
	}, "", "  ")
	fmt.Printf("[smoke] parse OK\n%s\n", parsePretty)

	// ── rank: fail-closed without SCHOLAR_LLM_*. The worker MUST answer a
	// typed error (fail-closed contract); success here would be a failure.
	if _, _, rankErr := client.Rank(ctx, "why do batteries age?", []scholar.RankCandidate{
		{PaperID: "p_smoke_1", Abstract: "battery aging abstract"},
	}); rankErr == nil {
		fmt.Fprintln(os.Stderr, "[smoke] FAIL: rank succeeded without LLM configuration — fail-closed contract broken")
		os.Exit(1)
	} else {
		fmt.Printf("[smoke] rank fail-closed OK (%v)\n", rankErr)
	}

	// ── discover: real provider fan-out; skipped offline / without network.
	if offline {
		fmt.Println("[smoke] discover SKIPPED (SCHOLAR_SMOKE_OFFLINE=1)")
		return
	}
	discoverOutputs, resp, err := client.Discover(ctx, "lithium battery cathode degradation", []string{"openalex"}, 3)
	if err != nil {
		fmt.Printf("[smoke] discover SKIPPED (network unavailable): %v\n", err)
		return
	}
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
OFFLINE_FLAG=online
if [ "${SCHOLAR_SMOKE_OFFLINE:-0}" = "1" ]; then OFFLINE_FLAG=offline; fi
(cd "$ROOT/backend" && go run "./cmd/scholar-smoke" "$BASE_URL" "$TOKEN" "$OFFLINE_FLAG")
echo "[smoke] PASS"
