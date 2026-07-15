#!/usr/bin/env bash
# ============================================================================
# Parade Backend Fixes Integration Test
# Validates 5 backend fixes:
#   1. push_messages persistence (HandlePushMessages call, no _ = msg)
#   2. deleteSubtree pruning (calls DeleteMerkleNodesByParent)
#   3. port/listen config (uses a.networkPort instead of hardcoded)
#   4. mDNS fix (no empty PeerUUID publish)
#   5. MerkleSyncComplete event (const + subscription)
# ============================================================================
set -uo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PARADE_BIN="${PARADE_BIN:-${ROOT_DIR}/parade}"
PASS=0
FAIL=0

info()  { printf "  [INFO]  %s\n" "$*"; }
pass()  { printf "  [PASS]  %s\n" "$*"; ((PASS++)); }
fail()  { printf "  [FAIL]  %s\n" "$*"; ((FAIL++)); }
header(){ printf "\n━━━ %s ━━━\n" "$*"; }

echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║      Parade Backend Fixes Integration Test Suite           ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# Clean stale project root artifacts
info "cleaning stale artifacts from project root..."
rm -f "${ROOT_DIR}/.parade_identity" "${ROOT_DIR}/.parade_data.db"* "${ROOT_DIR}/.parade.lock" "${ROOT_DIR}/test.db" "${ROOT_DIR}/test.id" 2>/dev/null || true

# ────────────────────────────────────────────────────────────────────────────
# Phase 1: Build
# ────────────────────────────────────────────────────────────────────────────
header "Phase 1: Build"

if cd "$ROOT_DIR" && go build -o "$PARADE_BIN" ./cmd/parade/ 2>&1; then
    pass "parade binary built"
else
    fail "parade binary build failed"
    exit 1
fi

if cd "$ROOT_DIR" && go build ./... 2>&1; then
    pass "go build ./... — all packages compile"
else
    fail "go build ./... — compilation failed"
    exit 1
fi

# ────────────────────────────────────────────────────────────────────────────
# Phase 2: Source-Level Fix Verification
# ────────────────────────────────────────────────────────────────────────────
header "Phase 2: Source-Level Fix Verification"

# Fix 1: push_messages handler no longer has no-op loop
if grep -q 'HandlePushMessages' "$ROOT_DIR/internal/network/libp2p_merklesync.go"; then
    if ! grep -q '_ = msg' "$ROOT_DIR/internal/network/libp2p_merklesync.go"; then
        pass "push_messages: HandlePushMessages called (no _ = msg no-op)"
    else
        fail "push_messages: found _ = msg (no-op pattern still present)"
    fi
else
    fail "push_messages: HandlePushMessages NOT found in libp2p_merklesync.go"
fi

# Fix 2: deleteSubtree is implemented (calls DeleteMerkleNodesByParent)
if grep -q 'DeleteMerkleNodesByParent' "$ROOT_DIR/internal/core/sync/freeze.go"; then
    pass "deleteSubtree: DeleteMerkleNodesByParent called (not just return nil)"
else
    fail "deleteSubtree: DeleteMerkleNodesByParent NOT found in freeze.go"
fi

# Fix 3: mDNS fix — HandlePeerFound does not publish empty UUID
if grep -qE 'PeerUUID:\s*""' "$ROOT_DIR/internal/network/mdns.go"; then
    fail "mDNS: empty PeerUUID publish still present in mdns.go"
else
    pass "mDNS: no empty PeerUUID publish in HandlePeerFound"
fi

# Fix 4: MerkleSyncComplete event — constant defined + subscribed
if grep -q 'TopicMerkleSyncComplete' "$ROOT_DIR/internal/core/eventbus/topics.go"; then
    pass "MerkleSyncComplete: TopicMerkleSyncComplete const defined in topics.go"
else
    fail "MerkleSyncComplete: TopicMerkleSyncComplete MISSING in topics.go"
fi

if grep -q 'TopicMerkleSyncComplete' "$ROOT_DIR/internal/app/app.go"; then
    pass "MerkleSyncComplete: app.go subscribes to TopicMerkleSyncComplete"
else
    fail "MerkleSyncComplete: app.go does NOT subscribe to TopicMerkleSyncComplete"
fi

# Fix 5: port config — startNetwork uses a.networkPort instead of hardcoded 4327
if grep -qE 'port\s*:=\s*a\.networkPort' "$ROOT_DIR/internal/app/app.go"; then
    pass "port: startNetwork uses a.networkPort (not hardcoded)"
else
    fail "port: startNetwork does NOT use a.networkPort"
fi

# ────────────────────────────────────────────────────────────────────────────
# Phase 3: Go Unit Tests for Sync Package
# ────────────────────────────────────────────────────────────────────────────
header "Phase 3: Sync Package Unit Tests"

if cd "$ROOT_DIR" && go test -count=1 -v -run 'TestPushMessages|TestDeleteSubtree|TestPruneOld' ./internal/core/sync/... 2>&1; then
    pass "go test sync (targeted fixes) — passed"
else
    fail "go test sync (targeted fixes) — FAILED"
fi

SYNC_OUTPUT=$(cd "$ROOT_DIR" && go test -count=1 ./internal/core/sync/... 2>&1)
SYNC_EXIT=$?

if [ "$SYNC_EXIT" -eq 0 ]; then
    TEST_COUNT=$(echo "$SYNC_OUTPUT" | grep -oP '^\w+.*?\.\w+\s' | head -1 || true)
    pass "go test sync (full suite) — all tests passed"
else
    fail "go test sync (full suite) — FAILED"
    echo "$SYNC_OUTPUT" | grep -E '(FAIL|--- FAIL)' | sed 's/^/    /'
fi

# ────────────────────────────────────────────────────────────────────────────
# Phase 4: CLI Port Config Smoke Test
# ────────────────────────────────────────────────────────────────────────────
header "Phase 4: CLI Port Config Smoke Test"

TEST_DIR="/tmp/parade-test-port-$$"
trap 'rm -rf "$TEST_DIR"' EXIT
mkdir -p "$TEST_DIR"

info "starting daemon with --port 19999 --listen 127.0.0.1 --headless --debug"
"$PARADE_BIN" daemon \
    --port 19999 --listen 127.0.0.1 --headless --debug \
    --data-dir "$TEST_DIR" \
    > "$TEST_DIR/daemon.log" 2>&1 &
DAEMON_PID=$!

# Wait up to 10 seconds for daemon to start
STARTED=0
for i in $(seq 1 20); do
    if grep -q 'Parade.*started' "$TEST_DIR/daemon.log" 2>/dev/null; then
        STARTED=1
        break
    fi
    sleep 0.5
done

if [ "$STARTED" -eq 1 ]; then
    if grep -q 'p2p=127.0.0.1:19999' "$TEST_DIR/daemon.log"; then
        pass "port smoke test: p2p=127.0.0.1:19999 found in log"
    else
        fail "port smoke test: p2p=127.0.0.1:19999 NOT found in log"
        info "log tail:"; tail -5 "$TEST_DIR/daemon.log" | sed 's/^/      /'
    fi
else
    fail "port smoke test: daemon failed to start"
    info "log tail:"; tail -10 "$TEST_DIR/daemon.log" | sed 's/^/      /'
fi

# Kill daemon
kill "$DAEMON_PID" 2>/dev/null || true
wait "$DAEMON_PID" 2>/dev/null || true
rm -rf "$TEST_DIR"
trap - EXIT

# ────────────────────────────────────────────────────────────────────────────
# Phase 5: Full Regression Test Suite
# ────────────────────────────────────────────────────────────────────────────
header "Phase 5: Full Regression Tests"

FULL_OUTPUT=$(cd "$ROOT_DIR" && go test -count=1 ./... 2>&1)
FULL_EXIT=$?

if [ "$FULL_EXIT" -eq 0 ]; then
    pass "go test ./... — all packages pass"
else
    fail "go test ./... — some packages FAIL"
    echo "$FULL_OUTPUT" | grep -E '(FAIL|--- FAIL)' | sed 's/^/    /'
fi

# Per-package results
while IFS= read -r line; do
    if [[ "$line" =~ ^ok[[:space:]]+(parade/.*)[[:space:]] ]]; then
        pass "  ${BASH_REMATCH[1]}"
    elif [[ "$line" =~ ^FAIL[[:space:]]+(parade/.*) ]]; then
        fail "  ${BASH_REMATCH[1]}"
    fi
done <<< "$FULL_OUTPUT"

# ────────────────────────────────────────────────────────────────────────────
# Summary
# ────────────────────────────────────────────────────────────────────────────
TOTAL=$((PASS + FAIL))
echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║           Backend Fixes Test Results                        ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo "  Total:   $TOTAL"
echo "  Passed:  $PASS"
echo "  Failed:  $FAIL"
echo ""

if [ "$FAIL" -eq 0 ]; then
    echo "  ALL CHECKS PASSED"
    echo ""
    exit 0
else
    echo "  SOME CHECKS FAILED"
    echo ""
    exit 1
fi
