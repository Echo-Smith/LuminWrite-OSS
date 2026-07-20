#!/bin/sh
# init-cli.sh — Initialize tencent-news-cli API key from environment variable
# This script runs before the main server starts.
# It sets the API key if TENCENT_NEWS_API_KEY is provided and the CLI is installed.

CLI_PATH="${TENCENT_NEWS_CLI_PATH:-$(command -v tencent-news-cli 2>/dev/null)}"
CLI_PATH="${CLI_PATH:-$HOME/.tencent-news-cli/bin/tencent-news-cli}"

if [ -z "$TENCENT_NEWS_API_KEY" ]; then
    echo "[init-cli] TENCENT_NEWS_API_KEY not set, skipping CLI API key configuration"
    exit 0
fi

if [ ! -x "$CLI_PATH" ]; then
    echo "[init-cli] tencent-news-cli not found, skipping"
    exit 0
fi

# Check if API key is already set (look for absence of "未设置" / "not set")
CURRENT=$("$CLI_PATH" apikey-get 2>&1)
if echo "$CURRENT" | grep -qE "未设置|not set"; then
    # Key is NOT set, so set it now
    echo "[init-cli] Setting tencent-news-cli API key..."
    RESULT=$("$CLI_PATH" apikey-set "$TENCENT_NEWS_API_KEY" 2>&1)
    if [ $? -eq 0 ]; then
        echo "[init-cli] API key set successfully"
    else
        echo "[init-cli] WARN: Failed to set API key: $RESULT"
    fi
else
    echo "[init-cli] API key already configured"
fi
