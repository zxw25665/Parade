#!/usr/bin/env bash
# One-command dev launch: Go daemon + Tauri frontend
# Usage: ./scripts/dev.sh   or   pixi run dev
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

cd "$ROOT_DIR"

# Build the Go daemon if not already built
if [ ! -f parade ]; then
    echo "[dev] Building daemon..."
    go build -o parade ./cmd/parade/
fi

# Start daemon in background with debug mode
echo "[dev] Starting daemon (debug mode)..."
./parade daemon --debug &
DAEMON_PID=$!

# Cleanup on exit — ensure daemon is killed when Tauri stops
cleanup() {
    echo "[dev] Stopping daemon (pid=$DAEMON_PID)..."
    kill $DAEMON_PID 2>/dev/null || true
    wait $DAEMON_PID 2>/dev/null || true
    echo "[dev] Daemon stopped."
}
trap cleanup EXIT INT TERM

# Give the daemon a moment to create the IPC pipe
sleep 1

# Start Tauri in dev mode (compiles Rust + opens WebView)
echo "[dev] Starting Tauri frontend..."
cd frontend && exec pnpm run tauri dev
