#!/usr/bin/env bash
# ============================================================================
# Parade 全面测试套件
# 运行所有 Go 单元测试 + 集群集成测试 + Merkle 同步专项验证
# ============================================================================
set -uo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PARADE_BIN="${PARADE_BIN:-${ROOT_DIR}/parade}"
PASS=0
FAIL=0
SKIP=0

info()  { printf "  [INFO]  %s\n" "$*"; }
pass()  { printf "  [PASS]  %s\n" "$*"; ((PASS++)); }
fail()  { printf "  [FAIL]  %s\n" "$*"; ((FAIL++)); }
skip()  { printf "  [SKIP]  %s\n" "$*"; ((SKIP++)); }
header(){ printf "\n━━━ %s ━━━\n" "$*"; }

# ────────────────────────────────────────────────────────────────────────────
# Phase 0: Cleanup stale artifacts from project root
# ────────────────────────────────────────────────────────────────────────────
header "Phase 0: Cleanup Stale Artifacts"
info "removing stale files from project root..."
rm -f "${ROOT_DIR}/.parade_identity" "${ROOT_DIR}/.parade_data.db"* "${ROOT_DIR}/.parade.lock" "${ROOT_DIR}/test.db" "${ROOT_DIR}/test.id" 2>/dev/null || true
pass "stale artifacts cleaned"

# ────────────────────────────────────────────────────────────────────────────
# Phase 1: Build
# ────────────────────────────────────────────────────────────────────────────
header "Phase 1: Build"
if go build -o "$PARADE_BIN" ./cmd/parade/ 2>&1; then
    pass "parade binary built"
else
    fail "parade binary build failed"
    exit 1
fi

# ────────────────────────────────────────────────────────────────────────────
# Phase 2: Go Unit Tests (all packages)
# ────────────────────────────────────────────────────────────────────────────
header "Phase 2: Go Unit Tests"

GO_TEST_OUTPUT=$(cd "$ROOT_DIR" && go test ./... -count=1 -timeout=120s 2>&1)
GO_TEST_EXIT=$?

if [ "$GO_TEST_EXIT" -eq 0 ]; then
    pass "go test ./... — all packages pass"
else
    fail "go test ./... — some packages FAIL"
    echo "$GO_TEST_OUTPUT" | grep -E '(FAIL|--- FAIL)' | sed 's/^/    /'
fi

# Extract individual package results
while IFS= read -r line; do
    if [[ "$line" =~ ^ok[[:space:]]+(parade/.*)[[:space:]] ]]; then
        pass "  ${BASH_REMATCH[1]}"
    elif [[ "$line" =~ ^FAIL[[:space:]]+(parade/.*) ]]; then
        fail "  ${BASH_REMATCH[1]}"
    fi
done <<< "$GO_TEST_OUTPUT"

# ────────────────────────────────────────────────────────────────────────────
# Phase 3: Sync Package Benchmarks
# ────────────────────────────────────────────────────────────────────────────
header "Phase 3: Sync Performance Benchmarks"

BENCH_OUTPUT=$(cd "$ROOT_DIR" && go test ./internal/core/sync/... -bench=. -benchmem -count=1 -timeout=120s 2>&1)
BENCH_EXIT=$?

if [ "$BENCH_EXIT" -eq 0 ]; then
    pass "benchmarks completed"
    echo ""
    echo "$BENCH_OUTPUT" | grep -E '^Benchmark' | while IFS= read -r line; do
        echo "    $line"
    done
else
    fail "benchmarks FAILED"
    echo "$BENCH_OUTPUT" | tail -20 | sed 's/^/    /'
fi

# ────────────────────────────────────────────────────────────────────────────
# Phase 4: Sync Correctness Summary
# ────────────────────────────────────────────────────────────────────────────
header "Phase 4: Sync Correctness Test Summary"

SYNC_TEST_OUTPUT=$(cd "$ROOT_DIR" && go test ./internal/core/sync/... -v -count=1 -timeout=120s -run 'Test3Node|Test5Node|TestEmpty|TestSingleMessage|TestSyncWithFrozen|TestSync_Cross|TestSync_Deterministic|TestSync_Content|TestLargeDataset|TestConcurrent' 2>&1)
SYNC_TEST_EXIT=$?

if [ "$SYNC_TEST_EXIT" -eq 0 ]; then
    pass "all sync correctness tests pass"
else
    fail "some sync correctness tests FAIL"
fi

# Count passes and failures
PASS_COUNT=$(echo "$SYNC_TEST_OUTPUT" | grep -c '^--- PASS:')
FAIL_COUNT=$(echo "$SYNC_TEST_OUTPUT" | grep -c '^--- FAIL:')
echo "    Correctness tests: $PASS_COUNT passed, $FAIL_COUNT failed"

# ────────────────────────────────────────────────────────────────────────────
# Phase 5: Cluster Integration Test (3-node)
# ────────────────────────────────────────────────────────────────────────────
header "Phase 5: Cluster Integration Test (5-node)"

CLUSTER_OUTPUT=$(PARADE_BIN="$PARADE_BIN" bash "$ROOT_DIR/tests/test_cluster.sh" 2>&1)
CLUSTER_EXIT=$?

if [ "$CLUSTER_EXIT" -eq 0 ]; then
    pass "cluster integration test PASSED"
else
    fail "cluster integration test FAILED"
fi

echo "$CLUSTER_OUTPUT" | grep -E '\[PASS\]|\[FAIL\]' | sed 's/^/    /'

# ────────────────────────────────────────────────────────────────────────────
# Phase 6: Merkle Sync Integration Verification
# ────────────────────────────────────────────────────────────────────────────
header "Phase 6: Merkle Sync Integration Verification"

# Verify Merkle sync protocol constant in source
if grep -q "protocolMerkleSync" "$ROOT_DIR/internal/network/libp2p_merklesync.go" && \
   grep -q "merklesync/1.0.0" "$ROOT_DIR/internal/network/libp2p_merklesync.go"; then
    pass "Merkle sync protocol defined in source"
else
    fail "Merkle sync protocol MISSING in source"
fi

# Verify old sync protocol constant still exists
if grep -q "protocolConvSync" "$ROOT_DIR/internal/network/libp2p_sync.go" && \
   grep -q "sync/1.0.0" "$ROOT_DIR/internal/network/libp2p_sync.go"; then
    pass "Legacy sync protocol still present (fallback)"
else
    fail "Legacy sync protocol MISSING"
fi

# Verify migration v11 exists in source
if grep -q "merkle_tree_nodes" "$ROOT_DIR/internal/core/db/sqlite.go"; then
    pass "DB migration v11 (merkle_tree_nodes) in source"
else
    fail "DB migration v11 MISSING in source"
fi

if grep -q "merkle_freeze_state" "$ROOT_DIR/internal/core/db/sqlite.go"; then
    pass "DB migration v11 (merkle_freeze_state) in source"
else
    fail "DB migration v11 (merkle_freeze_state) MISSING in source"
fi

# Verify BLAKE3 import in merkle.go
if grep -q "blake3" "$ROOT_DIR/internal/core/sync/merkle.go"; then
    pass "BLAKE3 imported in merkle.go"
else
    fail "BLAKE3 NOT imported in merkle.go"
fi

# Verify go vet passes
if cd "$ROOT_DIR" && go vet ./... 2>&1; then
    pass "go vet ./... — all packages pass"
else
    fail "go vet ./... — issues found"
fi

# ────────────────────────────────────────────────────────────────────────────
# Phase 7: Architecture Verification
# ────────────────────────────────────────────────────────────────────────────
header "Phase 7: Architecture Verification"

# Verify all expected files exist
EXPECTED_FILES=(
    "internal/core/sync/timebucket.go"
    "internal/core/sync/merkle.go"
    "internal/core/sync/freeze.go"
    "internal/core/sync/sync.go"
    "internal/core/sync/timebucket_test.go"
    "internal/core/sync/merkle_test.go"
    "internal/core/sync/sync_test.go"
    "internal/core/sync/testdata.go"
    "internal/network/libp2p_merklesync.go"
)

for f in "${EXPECTED_FILES[@]}"; do
    if [ -f "$ROOT_DIR/$f" ]; then
        pass "file exists: $f"
    else
        fail "file MISSING: $f"
    fi
done

# Verify DB models exist
if grep -q "type MerkleNode struct" "$ROOT_DIR/internal/core/db/models.go"; then
    pass "db.MerkleNode model defined"
else
    fail "db.MerkleNode model MISSING"
fi

if grep -q "type FreezeState struct" "$ROOT_DIR/internal/core/db/models.go"; then
    pass "db.FreezeState model defined"
else
    fail "db.FreezeState model MISSING"
fi

# Verify event bus topic
if grep -q "TopicMerkleSyncComplete" "$ROOT_DIR/internal/core/eventbus/topics.go"; then
    pass "TopicMerkleSyncComplete event defined"
else
    fail "TopicMerkleSyncComplete event MISSING"
fi

# ────────────────────────────────────────────────────────────────────────────
# Summary
# ────────────────────────────────────────────────────────────────────────────
TOTAL=$((PASS + FAIL))
echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║              Parade Complete Test Results                   ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo "  Total:   $TOTAL"
echo "  Passed:  $PASS"
echo "  Failed:  $FAIL"
echo "  Skipped: $SKIP"
echo ""

if [ "$FAIL" -eq 0 ]; then
    echo "  ALL TESTS PASSED"
    echo ""
    exit 0
else
    echo "  SOME TESTS FAILED"
    echo ""
    exit 1
fi
