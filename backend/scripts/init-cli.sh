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
    EXIT_CODE=$?
    if [ $EXIT_CODE -eq 0 ] && ! echo "$RESULT" | grep -qiE "error|无效|invalid|失败"; then
        echo "[init-cli] API key set successfully"
    else
        echo "[init-cli] ERROR: Failed to set tencent-news-cli API key!"
        echo "[init-cli] The API key may be invalid or expired."
        echo "[init-cli] Get a valid key from: https://news.qq.com/exchange?scene=appkey"
        echo "[init-cli] Detail: $RESULT"
    fi
else
    echo "[init-cli] API key already configured"
fi
