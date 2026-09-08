#!/usr/bin/env bash
# ============================================================================
#  Qwen-Free-API — one-command launcher (Linux / macOS)
#
#  First run:   chmod +x start.sh && ./start.sh
#  Next runs:   ./start.sh
#
#  What it does:
#    1. Creates .env from .env.example on first run (and tells you where
#       to paste your chat.qwen.ai token)
#    2. Finds a prebuilt binary (qwen-api), builds one if Go is installed,
#       or downloads the latest release from GitHub
#    3. Starts the server and prints the URLs
# ============================================================================
set -euo pipefail
cd "$(dirname "$0")"

GOLD='\033[33m'; GREEN='\033[32m'; RED='\033[31m'; DIM='\033[2m'; OFF='\033[0m'
say()  { printf "${GREEN}▸${OFF} %s\n" "$1"; }
warn() { printf "${GOLD}!${OFF} %s\n" "$1"; }
die()  { printf "${RED}✗ %s${OFF}\n" "$1"; exit 1; }

# ── 1. First-run setup ──────────────────────────────────────────────────────
if [ ! -f .env ]; then
    cp .env.example .env
    printf "\n${GOLD}⚠ ── First-run setup ──────────────────────────────────────${OFF}\n"
    say "A starter .env was created from .env.example."
    say "Open it and paste your chat.qwen.ai token(s) into QWEN_TOKENS:"
    printf "${DIM}       nano .env${OFF}\n"
    printf "${DIM}       (token: chat.qwen.ai → F12 → Application → Cookies → token)${OFF}\n"
    printf "${DIM}       Guest mode works with no token, but datacenter IPs may${OFF}\n"
    printf "${DIM}       hit Aliyun's captcha — a real token is the reliable path.${OFF}\n"
    printf "${GOLD}───────────────────────────────────────────────────────────${OFF}\n\n"
fi

# surface .env for this run (values already merge into the binary too)
set -a; . ./.env; set +a

# ── 2. Find or build the binary ─────────────────────────────────────────────
BIN=""
if [ -x ./qwen-api ]; then
    BIN=./qwen-api
elif command -v go >/dev/null 2>&1; then
    say "Building qwen-api with Go $(go version | awk '{print $3}')…"
    go build -trimpath -ldflags="-s -w" -o qwen-api . && BIN=./qwen-api
else
    warn "No prebuilt binary and no Go toolchain found."
    say "Fastest fix — install Go, then re-run:"
    printf "${DIM}       https://go.dev/dl/   (or: apt install golang / brew install go)${OFF}\n"
    say "Or grab a release binary: https://github.com/Godde3s/qwen-free-api/releases"
    exit 1
fi

# ── 3. Launch ───────────────────────────────────────────────────────────────
PORT="${PORT:-8080}"
say "Starting Qwen-Free-API on port ${PORT}…"
printf "\n"
exec "$BIN"
