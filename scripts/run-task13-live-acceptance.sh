#!/bin/sh
set -eu

: "${TASK13_LLM_API_KEY:?TASK13_LLM_API_KEY is required}"
: "${TASK13_LLM_BASE_URL:?TASK13_LLM_BASE_URL is required}"
: "${TASK13_LLM_MODEL:?TASK13_LLM_MODEL is required}"
: "${TEST_DATABASE_URL:?TEST_DATABASE_URL is required}"

if [ "$TASK13_LLM_MODEL" != "deepseek-v4-flash" ]; then
  echo "Task13 acceptance is pinned to deepseek-v4-flash" >&2
  exit 2
fi

cd "$(dirname "$0")/../backend"
exec go test ./internal/writingruntime \
  -run '^TestTask13LiveModelVerticalAcceptance$' \
  -count=1 -v -timeout 20m
