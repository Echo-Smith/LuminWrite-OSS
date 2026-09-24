#!/usr/bin/env bash
# Scholar in-process smoke: drive backend/internal/scholar exactly the way
# the governed runtime wires it (SCHOLAR_WORKER_URL as the enablement
# signal, SCHOLAR_WORKER_TOKEN as the required guard) and exercise the
# bounded operations for real — there is no separate worker service:
#
#   1. construction — NewClient must fail closed on an empty token
#   2. health (wiring-compat no-op)  — always
#   3. parse (deterministic TXT)     — always (no network, no model)
#   4. rank — the FAIL-CLOSED error path: no SCHOLAR_LLM_* is configured, so
#      the executor must answer a typed error (never silent all-zero scores)
#   5. discover (real provider fan-out, openalex) — unless
#      SCHOLAR_SMOKE_OFFLINE=1; a network failure here is reported as
#      DISCOVER=SKIPPED
#
# PDF parsing is delegated to the docreader sidecar in production; this smoke
# uses plain text and covers the in-process path only.
#
# Usage: scripts/dev-scholar-smoke.sh
# Requires: Go.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"

# 1) Build a tiny Go driver that uses backend/internal/scholar for real.
DRIVER_DIR="$(mktemp -d "$ROOT/backend/cmd/scholar-inproc-smoke.XXXXXX")"
cat > "$DRIVER_DIR/main.go" <<'EOF'
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
	offline := len(os.Args) > 1 && os.Args[1] == "offline"

	// The governed runtime constructs the client only when SCHOLAR_WORKER_URL
	// is set and fails closed on an empty token — mirror both here.
	if _, err := scholar.NewClient("", ""); err == nil {
		fmt.Fprintln(os.Stderr, "[smoke] FAIL: NewClient accepted an empty token — required-guard contract broken")
		os.Exit(1)
	}
	fmt.Println("[smoke] empty-token construction fails closed OK")

	client, err := scholar.NewClient("in-process-smoke", "smoke-token")
	exitIf(err)

	exitIf(client.Health(context.Background()))
	fmt.Println("[smoke] health OK")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	// ── parse: deterministic full loop (in-process, no network, no model).
	document := []byte("LuminScholar smoke document.\n\nSecond paragraph: hash-verified blocks follow.")
	parse, resp, err := (writingruntime.ScholarParseRead{Client: client}).ParseDocument(ctx, document, "text/plain", "parser/1")
	exitIf(err)
	parsePretty, _ := json.MarshalIndent(map[string]any{
		"request_id": resp.RequestID, "blocks": len(parse.Blocks),
		"coverage": parse.Coverage,
	}, "", "  ")
	fmt.Printf("[smoke] parse OK\n%s\n", parsePretty)

	// ── rank: fail-closed without SCHOLAR_LLM_*. The executor MUST answer a
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
		"request_id":  resp.RequestID,
		"paper_count": len(discoverOutputs.Papers),
		"providers":   discoverOutputs.ProviderResults,
		"usage":       resp.Usage,
		"versions":    resp.Versions,
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

cleanup() { rm -rf "$DRIVER_DIR"; }
trap cleanup EXIT

# 2) Run the driver from the backend module.
if ! command -v go >/dev/null 2>&1; then
  for candidate in "$HOME/.local/go/bin" "/usr/local/go/bin" "/opt/homebrew/bin"; do
    if [ -x "$candidate/go" ]; then export PATH="$candidate:$PATH"; break; fi
  done
fi
OFFLINE_FLAG=online
if [ "${SCHOLAR_SMOKE_OFFLINE:-0}" = "1" ]; then OFFLINE_FLAG=offline; fi
(cd "$ROOT/backend" && go run "./cmd/$(basename "$DRIVER_DIR")" "$OFFLINE_FLAG")
echo "[smoke] PASS"
