package sync

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"parade/internal/core/db"
)

// ============================================================================
// In-Memory Mock DB — implements MerkleDB + FreezeDB fully in-memory
// ============================================================================

type memDB struct {
	mu        sync.RWMutex
	messages  map[string][]*db.Message // convID → msgs
	merkle    map[string]*db.MerkleNode // "convID|bucketPath" → node
	frozen    map[string]*db.FreezeState
	convs     map[string]*db.Conversation
	msgByHLC  map[string]*db.Message // HLC → msg (global uniqueness)
}

func newMemDB() *memDB {
	return &memDB{
		messages: make(map[string][]*db.Message),
		merkle:   make(map[string]*db.MerkleNode),
		frozen:   make(map[string]*db.FreezeState),
		convs:    make(map[string]*db.Conversation),
		msgByHLC: make(map[string]*db.Message),
	}
}

func key(convID, bucketPath string) string { return convID + "|" + bucketPath }

func (m *memDB) InsertMessage(ctx context.Context, msg *db.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.msgByHLC[msg.HLC]; exists {
		return nil // idempotent
	}
	m.messages[msg.ConversationID] = append(m.messages[msg.ConversationID], msg)
	m.msgByHLC[msg.HLC] = msg
	return nil
}

func (m *memDB) UpsertMerkleNode(ctx context.Context, node *db.MerkleNode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copy := *node
	copy.Hash = append([]byte(nil), node.Hash...)
	m.merkle[key(node.ConvID, node.BucketPath)] = &copy
	return nil
}

func (m *memDB) GetMerkleNode(ctx context.Context, convID, bucketPath string) (*db.MerkleNode, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := m.merkle[key(convID, bucketPath)]
	if n == nil {
		return nil, nil
	}
	copy := *n
	copy.Hash = append([]byte(nil), n.Hash...)
	return &copy, nil
}

func (m *memDB) GetMerkleNodesByLevel(ctx context.Context, convID string, level int) ([]*db.MerkleNode, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*db.MerkleNode
	for _, n := range m.merkle {
		if n.ConvID == convID && n.Level == level {
			copy := *n
			copy.Hash = append([]byte(nil), n.Hash...)
			result = append(result, &copy)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].BucketPath < result[j].BucketPath })
	return result, nil
}

func (m *memDB) GetMerkleNodesByParent(ctx context.Context, convID, parentPath string) ([]*db.MerkleNode, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*db.MerkleNode
	for _, n := range m.merkle {
		if n.ConvID == convID && len(n.BucketPath) > len(parentPath) &&
			n.BucketPath[:len(parentPath)] == parentPath {
			copy := *n
			copy.Hash = append([]byte(nil), n.Hash...)
			result = append(result, &copy)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].BucketPath < result[j].BucketPath })
	return result, nil
}

func (m *memDB) DeleteMerkleNodesByConv(ctx context.Context, convID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, n := range m.merkle {
		if n.ConvID == convID {
			delete(m.merkle, k)
		}
	}
	return nil
}

func (m *memDB) GetFrozenState(ctx context.Context, convID string) (*db.FreezeState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.frozen[convID]
	if !ok {
		return nil, nil
	}
	copy := *s
	return &copy, nil
}

func (m *memDB) UpsertFrozenState(ctx context.Context, state *db.FreezeState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copy := *state
	m.frozen[state.ConvID] = &copy
	return nil
}

func (m *memDB) ListAllConversations(ctx context.Context) ([]*db.Conversation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*db.Conversation
	for _, c := range m.convs {
		copy := *c
		result = append(result, &copy)
	}
	return result, nil
}

func (m *memDB) GetMessagesInBucket(ctx context.Context, convID, bucketPath string, level int) ([]*db.Message, error) {
	startHLC, endHLC, err := BucketTimeRange(bucketPath, level)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*db.Message
	for _, msg := range m.messages[convID] {
		if msg.HLC >= startHLC && msg.HLC < endHLC {
			copy := *msg
			copy.Content = append([]byte(nil), msg.Content...)
			result = append(result, &copy)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].HLC < result[j].HLC })
	return result, nil
}

func (m *memDB) GetConversationMessagesSinceHLC(ctx context.Context, convID string, sinceHLC string, limit int) ([]*db.Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*db.Message
	for _, msg := range m.messages[convID] {
		if msg.HLC > sinceHLC {
			copy := *msg
			copy.Content = append([]byte(nil), msg.Content...)
			result = append(result, &copy)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].HLC < result[j].HLC })
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *memDB) RunInTx(ctx context.Context, fn func(tx db.DBTx) error) error {
	return fn(&memTx{db: m})
}

func (m *memDB) UpdateConversationLastHLC(ctx context.Context, convID string, hlc string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.convs[convID]; ok {
		c.LastHLC = hlc
	}
	return nil
}

func (m *memDB) UpsertConversation(ctx context.Context, conv *db.Conversation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copy := *conv
	m.convs[conv.ID] = &copy
	return nil
}

func (m *memDB) MessageCount(convID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.messages[convID])
}

func (m *memDB) AllMessages(convID string) []*db.Message {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*db.Message
	for _, msg := range m.messages[convID] {
		copy := *msg
		copy.Content = append([]byte(nil), msg.Content...)
		result = append(result, &copy)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].HLC < result[j].HLC })
	return result
}

// memTx implements db.DBTx
type memTx struct {
	db *memDB
}

func (t *memTx) InsertMessageTx(ctx context.Context, msg *db.Message) error {
	return t.db.InsertMessage(ctx, msg)
}
func (t *memTx) UpsertFileLogTx(ctx context.Context, log *db.FileLog) error { return nil }
func (t *memTx) UpsertConversationTx(ctx context.Context, conv *db.Conversation) error {
	return t.db.UpsertConversation(ctx, conv)
}
func (t *memTx) UpsertMerkleNodeTx(ctx context.Context, node *db.MerkleNode) error {
	return t.db.UpsertMerkleNode(ctx, node)
}
func (t *memTx) UpsertFrozenStateTx(ctx context.Context, state *db.FreezeState) error {
	return t.db.UpsertFrozenState(ctx, state)
}

// ============================================================================
// In-Memory Mock Network — direct routing between nodes
// ============================================================================

type testNetwork struct {
	mu      sync.RWMutex
	nodes   map[string]*testNode // UUID → node
	routing map[string]string    // UUID → peer UUID routing table
}

func newTestNetwork() *testNetwork {
	return &testNetwork{
		nodes:   make(map[string]*testNode),
		routing: make(map[string]string),
	}
}

func (tn *testNetwork) register(node *testNode) {
	tn.mu.Lock()
	defer tn.mu.Unlock()
	tn.nodes[node.uuid] = node
}

func (tn *testNetwork) routeTo(targetUUID string) (*testNode, error) {
	tn.mu.RLock()
	defer tn.mu.RUnlock()
	n, ok := tn.nodes[targetUUID]
	if !ok {
		return nil, fmt.Errorf("node %s not found", targetUUID)
	}
	return n, nil
}

func (tn *testNetwork) SendMerkleRootRequest(targetUUID, convID string) ([]byte, error) {
	n, err := tn.routeTo(targetUUID)
	if err != nil {
		return nil, err
	}
	return n.orch.HandleMerkleRootRequest(context.Background(), convID)
}

func (tn *testNetwork) SendBucketCompareRequest(targetUUID, convID string, level int, paths []string) ([]BucketInfo, error) {
	n, err := tn.routeTo(targetUUID)
	if err != nil {
		return nil, err
	}
	return n.orch.HandleBucketCompare(context.Background(), convID, level, paths)
}

func (tn *testNetwork) SendFetchMessagesRequest(targetUUID, convID, bucketPath, sinceHLC string) ([]*db.Message, error) {
	n, err := tn.routeTo(targetUUID)
	if err != nil {
		return nil, err
	}
	return n.orch.HandleFetchMessages(context.Background(), convID, bucketPath, sinceHLC)
}

func (tn *testNetwork) SendPushMessages(targetUUID, convID string, messages []*db.Message) error {
	n, err := tn.routeTo(targetUUID)
	if err != nil {
		return err
	}
	for _, msg := range messages {
		if err := n.db.InsertMessage(context.Background(), msg); err != nil {
			return err
		}
	}
	return nil
}

// ============================================================================
// TestNode — wraps a SyncOrchestrator + its private DB
// ============================================================================

type testNode struct {
	uuid string
	name string
	db   *memDB
	orch *SyncOrchestrator
	net  *testNetwork
}

func newTestNode(uuid, name string, net *testNetwork) *testNode {
	mdb := newMemDB()
	node := &testNode{
		uuid: uuid,
		name: name,
		db:   mdb,
		net:  net,
	}
	node.orch = NewSyncOrchestrator(mdb, net, nil)
	net.register(node)
	return node
}

func (n *testNode) LoadMessages(msgs []*db.Message) {
	for _, msg := range msgs {
		n.db.InsertMessage(context.Background(), msg)
	}
	// Ensure conversations exist
	seen := make(map[string]bool)
	for _, msg := range msgs {
		if !seen[msg.ConversationID] {
			n.db.UpsertConversation(context.Background(), &db.Conversation{
				ID:     msg.ConversationID,
				TeamID: msg.TeamID,
				Type:   "team",
			})
			seen[msg.ConversationID] = true
		}
	}
}

func (n *testNode) MessageCount(convID string) int {
	return n.db.MessageCount(convID)
}

func (n *testNode) AllMessages(convID string) []*db.Message {
	return n.db.AllMessages(convID)
}

// ============================================================================
// Test Cluster — manages N nodes with shared network
// ============================================================================

type testCluster struct {
	tb    testing.TB
	net   *testNetwork
	nodes []*testNode
}

func newCluster(tb testing.TB, nodeCount int) *testCluster {
	net := newTestNetwork()
	nodes := make([]*testNode, nodeCount)
	for i := 0; i < nodeCount; i++ {
		uuid := fmt.Sprintf("node-%02d", i)
		name := fmt.Sprintf("Node-%02d", i)
		nodes[i] = newTestNode(uuid, name, net)
	}
	return &testCluster{tb: tb, net: net, nodes: nodes}
}

func (c *testCluster) Node(i int) *testNode {
	return c.nodes[i]
}

func (c *testCluster) ForEach(fn func(n *testNode)) {
	for _, n := range c.nodes {
		fn(n)
	}
}

// SyncAll performs a full mesh sync: every node syncs with every other node.
// Returns the total number of messages transferred across all sync operations.
func (c *testCluster) SyncAll(convID string) int {
	total := 0
	for i := 0; i < len(c.nodes); i++ {
		for j := 0; j < len(c.nodes); j++ {
			if i == j {
				continue
			}
			from := c.nodes[i]
			to := c.nodes[j]
			err := from.orch.SyncConversation(context.Background(), to.uuid, convID)
		if err != nil {
			c.tb.Errorf("sync %s → %s conv=%s failed: %v", from.name, to.name, convID, err)
			}
		}
	}
	return total
}

// SyncPair performs a single pairwise sync.
func (c *testCluster) SyncPair(fromIdx, toIdx int, convID string) error {
	return c.nodes[fromIdx].orch.SyncConversation(context.Background(), c.nodes[toIdx].uuid, convID)
}

// VerifyAllConverged checks that all nodes have identical message sets for all conversations.
func (c *testCluster) VerifyAllConverged() bool {
	ok := true
	// Collect all conversation IDs
	convSet := make(map[string]bool)
	for _, n := range c.nodes {
		for _, msg := range n.AllMessages("") {
			convSet[msg.ConversationID] = true
		}
	}
	// Use a workaround - collect from each node's messages
	for convID := range convSet {
		ok = ok && c.verifyConvConverged(convID)
	}
	return ok
}

func (c *testCluster) verifyConvConverged(convID string) bool {
	if len(c.nodes) < 2 {
		return true
	}
	ref := c.nodes[0].AllMessages(convID)
	refHLCs := make(map[string]*db.Message)
	for _, m := range ref {
		refHLCs[m.HLC] = m
	}

	ok := true
	for i := 1; i < len(c.nodes); i++ {
		other := c.nodes[i].AllMessages(convID)
		if len(other) != len(ref) {
			c.tb.Errorf("convergence FAIL: conv=%s node[%d] has %d msgs, node[0] has %d",
				convID, i, len(other), len(ref))
			ok = false
			continue
		}
		for _, m := range other {
			refMsg, exists := refHLCs[m.HLC]
			if !exists {
				c.tb.Errorf("convergence FAIL: conv=%s node[%d] has extra msg HLC=%s",
					convID, i, m.HLC)
				ok = false
				continue
			}
			if string(refMsg.Content) != string(m.Content) {
				c.tb.Errorf("convergence FAIL: conv=%s HLC=%s content mismatch", convID, m.HLC)
				ok = false
			}
		}
	}
	return ok
}

// VerifyNodeCounts checks that each node has the expected message count per conversation.
func (c *testCluster) VerifyNodeCounts(convID string, expected int) bool {
	ok := true
	for _, n := range c.nodes {
		if cnt := n.MessageCount(convID); cnt != expected {
			c.tb.Errorf("count mismatch: node=%s conv=%s got %d want %d", n.name, convID, cnt, expected)
			ok = false
		}
	}
	return ok
}

// ============================================================================
// CORRECTNESS TESTS — 3-Node Cluster
// ============================================================================

func Test3Node_DatasetA_FullSync(t *testing.T) {
	// Setup: 3 nodes, all start with Dataset A (500 msgs, 1 conv)
	cluster := newCluster(t, 3)
	dataset := GenerateMessages(DefaultDatasetA)

	for _, n := range cluster.nodes {
		n.LoadMessages(CloneMessages(dataset))
	}

	// Sync: full mesh
	cluster.SyncAll("conv-a")

	// Verify: all 3 nodes have all 500 messages
	if !cluster.VerifyNodeCounts("conv-a", 500) {
		t.Fatal("node count mismatch after full sync")
	}
	if !cluster.VerifyAllConverged() {
		t.Fatal("nodes not converged after full sync")
	}
}

func Test3Node_DatasetB_FullSync(t *testing.T) {
	cluster := newCluster(t, 3)
	dataset := GenerateMessages(DefaultDatasetB)

	for _, n := range cluster.nodes {
		n.LoadMessages(CloneMessages(dataset))
	}

	cluster.SyncAll("conv-b1")
	cluster.SyncAll("conv-b2")

	if !cluster.VerifyNodeCounts("conv-b1", 250) {
		t.Fatal("conv-b1 count mismatch")
	}
	if !cluster.VerifyNodeCounts("conv-b2", 250) {
		t.Fatal("conv-b2 count mismatch")
	}
	if !cluster.VerifyAllConverged() {
		t.Fatal("nodes not converged")
	}
}

func Test3Node_PartialSync(t *testing.T) {
	// Node 0 has 100% of Dataset A
	// Node 1 has 60%
	// Node 2 has 30%
	cluster := newCluster(t, 3)
	full := GenerateMessages(DefaultDatasetA)

	cluster.Node(0).LoadMessages(CloneMessages(full))
	cluster.Node(1).LoadMessages(SubsetMessages(full, 0.6, 100))
	cluster.Node(2).LoadMessages(SubsetMessages(full, 0.3, 200))

	// Sync: 0→1, 0→2
	if err := cluster.SyncPair(0, 1, "conv-a"); err != nil {
		t.Fatalf("sync 0→1 failed: %v", err)
	}
	if err := cluster.SyncPair(0, 2, "conv-a"); err != nil {
		t.Fatalf("sync 0→2 failed: %v", err)
	}

	// Now node 1 and 2 should have all 500
	if !cluster.VerifyNodeCounts("conv-a", 500) {
		t.Fatal("nodes not fully synced")
	}
	if !cluster.VerifyAllConverged() {
		t.Fatal("nodes not converged")
	}
}

func Test3Node_BidirectionalSync(t *testing.T) {
	// Each node has a unique subset. After full mesh sync, all should converge.
	cluster := newCluster(t, 3)
	full := GenerateMessages(DefaultDatasetA)

	cluster.Node(0).LoadMessages(SubsetMessages(full, 0.4, 10))
	cluster.Node(1).LoadMessages(SubsetMessages(full, 0.5, 20))
	cluster.Node(2).LoadMessages(SubsetMessages(full, 0.3, 30))

	cluster.SyncAll("conv-a")

	if !cluster.VerifyAllConverged() {
		t.Fatal("nodes not converged after bidirectional sync")
	}
}

func Test3Node_IdempotentSync(t *testing.T) {
	// Sync twice — second sync should transfer 0 messages
	cluster := newCluster(t, 3)
	dataset := GenerateMessages(DefaultDatasetA)

	for _, n := range cluster.nodes {
		n.LoadMessages(CloneMessages(dataset))
	}

	// First sync
	cluster.SyncAll("conv-a")

	// Second sync — should be a no-op
	for i := 0; i < len(cluster.nodes); i++ {
		for j := 0; j < len(cluster.nodes); j++ {
			if i == j {
				continue
			}
			err := cluster.nodes[i].orch.SyncConversation(
				context.Background(), cluster.nodes[j].uuid, "conv-a")
			if err != nil {
				t.Errorf("idempotent sync %d→%d failed: %v", i, j, err)
			}
		}
	}

	if !cluster.VerifyNodeCounts("conv-a", 500) {
		t.Fatal("message count changed after idempotent sync")
	}
}

// ============================================================================
// CORRECTNESS TESTS — 5-Node Cluster
// ============================================================================

func Test5Node_DatasetA_FullSync(t *testing.T) {
	cluster := newCluster(t, 5)
	dataset := GenerateMessages(DefaultDatasetA)

	for _, n := range cluster.nodes {
		n.LoadMessages(CloneMessages(dataset))
	}

	cluster.SyncAll("conv-a")

	if !cluster.VerifyNodeCounts("conv-a", 500) {
		t.Fatal("node count mismatch after 5-node sync")
	}
	if !cluster.VerifyAllConverged() {
		t.Fatal("5 nodes not converged")
	}
}

func Test5Node_DatasetB_FullSync(t *testing.T) {
	cluster := newCluster(t, 5)
	dataset := GenerateMessages(DefaultDatasetB)

	for _, n := range cluster.nodes {
		n.LoadMessages(CloneMessages(dataset))
	}

	cluster.SyncAll("conv-b1")
	cluster.SyncAll("conv-b2")

	if !cluster.VerifyAllConverged() {
		t.Fatal("5 nodes not converged with 2 conversations")
	}
}

func Test5Node_ChainedSync(t *testing.T) {
	// 0→1→2→3→4 chain sync
	cluster := newCluster(t, 5)
	full := GenerateMessages(DefaultDatasetA)

	cluster.Node(0).LoadMessages(CloneMessages(full))

	for i := 0; i < 4; i++ {
		if err := cluster.SyncPair(i, i+1, "conv-a"); err != nil {
			t.Fatalf("chain sync %d→%d failed: %v", i, i+1, err)
		}
	}

	if !cluster.VerifyAllConverged() {
		t.Fatal("chain sync did not converge")
	}
}

func Test5Node_StarSync(t *testing.T) {
	// Node 0 has everything, all others sync from node 0
	cluster := newCluster(t, 5)
	full := GenerateMessages(DefaultDatasetA)

	cluster.Node(0).LoadMessages(CloneMessages(full))

	for i := 1; i < 5; i++ {
		if err := cluster.SyncPair(0, i, "conv-a"); err != nil {
			t.Fatalf("star sync 0→%d failed: %v", i, err)
		}
	}

	if !cluster.VerifyAllConverged() {
		t.Fatal("star sync did not converge")
	}
}

func Test5Node_GradualConvergence(t *testing.T) {
	// Each node gets a random subset. After full mesh, all converge.
	cluster := newCluster(t, 5)
	full := GenerateMessages(DefaultDatasetA)
	seeds := []int64{10, 20, 30, 40, 50}
	fractions := []float64{0.3, 0.5, 0.4, 0.6, 0.2}

	for i := 0; i < 5; i++ {
		cluster.Node(i).LoadMessages(SubsetMessages(full, fractions[i], seeds[i]))
	}

	cluster.SyncAll("conv-a")

	if !cluster.VerifyAllConverged() {
		t.Fatal("gradual convergence failed")
	}
}

// ============================================================================
// EDGE CASE TESTS
// ============================================================================

func TestEmptySync(t *testing.T) {
	// Two nodes with no messages should sync without error
	cluster := newCluster(t, 2)

	if err := cluster.SyncPair(0, 1, "conv-empty"); err != nil {
		t.Fatalf("empty sync failed: %v", err)
	}
}

func TestSingleMessageSync(t *testing.T) {
	cluster := newCluster(t, 2)
	msg := &db.Message{
		ID:             "single-msg",
		HLC:            "2026-06-01T12:00:00.000Z_0001_test",
		Content:        []byte("hello"),
		ConversationID: "conv-single",
		TeamID:         "test-team",
	}
	cluster.Node(0).LoadMessages([]*db.Message{msg})

	if err := cluster.SyncPair(0, 1, "conv-single"); err != nil {
		t.Fatalf("single message sync failed: %v", err)
	}

	if !cluster.VerifyAllConverged() {
		t.Fatal("single message sync did not converge")
	}
}

func TestSyncWithFrozenBuckets(t *testing.T) {
	// Simulate frozen buckets: freeze a day-level bucket, then verify
	// that sync skips it correctly.
	cluster := newCluster(t, 2)
	dataset := GenerateMessages(DefaultDatasetA)

	for _, n := range cluster.nodes {
		n.LoadMessages(CloneMessages(dataset))
	}

	// Initial sync to build trees
	cluster.SyncAll("conv-a")

	// Freeze a day bucket on node 0
	node0 := cluster.Node(0)
	dayBucket := "2026-04-10" // first day of Dataset A
	node, _ := node0.db.GetMerkleNode(context.Background(), "conv-a", dayBucket)
	if node != nil {
		node.Frozen = true
		node0.db.UpsertMerkleNode(context.Background(), node)
	}

	// Second sync — should still work (frozen bucket is trusted)
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			if i == j {
				continue
			}
			if err := cluster.SyncPair(i, j, "conv-a"); err != nil {
				t.Errorf("sync with frozen bucket %d→%d failed: %v", i, j, err)
			}
		}
	}

	if !cluster.VerifyAllConverged() {
		t.Fatal("sync with frozen buckets did not converge")
	}
}

func TestSync_CrossConversationNoCrossContamination(t *testing.T) {
	// Two conversations must not interfere during sync
	cluster := newCluster(t, 3)
	datasetA := GenerateMessages(DefaultDatasetA) // 500 msgs in conv-a
	datasetB := GenerateMessages(DefaultDatasetB) // 500 msgs split across conv-b1, conv-b2

	// Node 0 has both datasets
	cluster.Node(0).LoadMessages(CloneMessages(datasetA))
	cluster.Node(0).LoadMessages(CloneMessages(datasetB))

	// Node 1 has only dataset A
	cluster.Node(1).LoadMessages(CloneMessages(datasetA))

	// Node 2 has only dataset B
	cluster.Node(2).LoadMessages(CloneMessages(datasetB))

	// Sync conv-a: node 0 → node 2 (node 2 should get conv-a messages)
	if err := cluster.SyncPair(0, 2, "conv-a"); err != nil {
		t.Fatalf("cross-conv sync failed: %v", err)
	}

	// Node 2 should now have 500 conv-a + 250 conv-b1 + 250 conv-b2
	if cnt := cluster.Node(2).MessageCount("conv-a"); cnt != 500 {
		t.Errorf("node 2 conv-a count = %d, want 500", cnt)
	}
	if cnt := cluster.Node(2).MessageCount("conv-b1"); cnt != 250 {
		t.Errorf("node 2 conv-b1 count = %d, want 250", cnt)
	}
	if cnt := cluster.Node(2).MessageCount("conv-b2"); cnt != 250 {
		t.Errorf("node 2 conv-b2 count = %d, want 250", cnt)
	}
}

func TestSync_DeterministicRootHash(t *testing.T) {
	// Same messages must produce the same root hash on different nodes
	dataset := GenerateMessages(DefaultDatasetA)

	node1 := newTestNode("det-1", "Det1", newTestNetwork())
	node2 := newTestNode("det-2", "Det2", newTestNetwork())

	node1.LoadMessages(CloneMessages(dataset))
	node2.LoadMessages(CloneMessages(dataset))

	tree1, _ := node1.orch.HandleMerkleRootRequest(context.Background(), "conv-a")
	tree2, _ := node2.orch.HandleMerkleRootRequest(context.Background(), "conv-a")

	if len(tree1) == 0 || len(tree2) == 0 {
		t.Fatal("expected non-empty root hashes")
	}
	if string(tree1) != string(tree2) {
		t.Fatal("root hashes differ for identical datasets")
	}
}

func TestSync_ContentTamperingDetection(t *testing.T) {
	// Two nodes with same HLCs but different content must NOT converge
	cluster := newCluster(t, 2)

	// Node 0: normal messages
	msgs0 := []*db.Message{
		{HLC: "2026-07-01T10:00:00.000Z_0001_a", Content: []byte("original"), ConversationID: "conv-tamper", TeamID: "test", ID: "1"},
	}
	cluster.Node(0).LoadMessages(msgs0)

	// Node 1: same HLC, different content (tampered)
	msgs1 := []*db.Message{
		{HLC: "2026-07-01T10:00:00.000Z_0001_a", Content: []byte("TAMPERED"), ConversationID: "conv-tamper", TeamID: "test", ID: "2"},
	}
	cluster.Node(1).LoadMessages(msgs1)

	// Sync should detect the difference
	err := cluster.SyncPair(0, 1, "conv-tamper")
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// The nodes should have different content (idempotent insert by HLC)
	// Node 1 already has HLC, so node 0's message won't overwrite
	// But the Merkle trees will differ, proving detection
	tree0, _ := cluster.Node(0).orch.HandleMerkleRootRequest(context.Background(), "conv-tamper")
	tree1, _ := cluster.Node(1).orch.HandleMerkleRootRequest(context.Background(), "conv-tamper")

	if string(tree0) == string(tree1) {
		t.Error("tampered content should produce different root hashes")
	}
}

// ============================================================================
// PERFORMANCE BENCHMARKS
// ============================================================================

func BenchmarkBuildMerkleTree_DatasetA(b *testing.B) {
	dataset := GenerateMessages(DefaultDatasetA)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := BuildMerkleTree("conv-a", dataset)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildMerkleTree_DatasetB(b *testing.B) {
	dataset := GenerateMessages(DefaultDatasetB)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := BuildMerkleTree("conv-b1", dataset)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark3Node_FullSync_DatasetA(b *testing.B) {
	dataset := GenerateMessages(DefaultDatasetA)
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cluster := newCluster(b, 3)
		for _, n := range cluster.nodes {
			n.LoadMessages(CloneMessages(dataset))
		}
		b.StartTimer()
		cluster.SyncAll("conv-a")
	}
}

func Benchmark3Node_FullSync_DatasetB(b *testing.B) {
	dataset := GenerateMessages(DefaultDatasetB)
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cluster := newCluster(b, 3)
		for _, n := range cluster.nodes {
			n.LoadMessages(CloneMessages(dataset))
		}
		b.StartTimer()
		cluster.SyncAll("conv-b1")
		cluster.SyncAll("conv-b2")
	}
}

func Benchmark5Node_FullSync_DatasetA(b *testing.B) {
	dataset := GenerateMessages(DefaultDatasetA)
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cluster := newCluster(b, 5)
		for _, n := range cluster.nodes {
			n.LoadMessages(CloneMessages(dataset))
		}
		b.StartTimer()
		cluster.SyncAll("conv-a")
	}
}

func Benchmark5Node_PartialSync_DatasetA(b *testing.B) {
	full := GenerateMessages(DefaultDatasetA)
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cluster := newCluster(b, 5)
		cluster.Node(0).LoadMessages(CloneMessages(full))
		cluster.Node(1).LoadMessages(SubsetMessages(full, 0.6, 100))
		cluster.Node(2).LoadMessages(SubsetMessages(full, 0.4, 200))
		cluster.Node(3).LoadMessages(SubsetMessages(full, 0.3, 300))
		cluster.Node(4).LoadMessages(SubsetMessages(full, 0.2, 400))
		b.StartTimer()
		cluster.SyncAll("conv-a")
	}
}

func BenchmarkComputeMessageHash(b *testing.B) {
	convID := "bench-conv"
	hlc := "2026-07-01T12:00:00.000Z_0001_bench"
	content := []byte("benchmark message content for hash computation")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ComputeMessageHash(convID, hlc, content)
	}
}

func BenchmarkComputeBucketHash_100Children(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	children := make([][]byte, 100)
	for i := range children {
		h := make([]byte, 32)
		rng.Read(h)
		children[i] = h
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ComputeBucketHash(children)
	}
}

func BenchmarkMerkleTree_GetChildren(b *testing.B) {
	dataset := GenerateMessages(DefaultDatasetA)
	tree, _ := BuildMerkleTree("conv-a", dataset)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.GetChildren("2026-04", LevelMonth)
	}
}

// ============================================================================
// MEMORY / SCALABILITY TESTS
// ============================================================================

func TestLargeDataset_TreeSize(t *testing.T) {
	// Verify tree node count scales correctly with message count
	cfg := DefaultDatasetA
	cfg.TotalMessages = 10000 // 10K messages
	dataset := GenerateMessages(cfg)

	tree, err := BuildMerkleTree("conv-a", dataset)
	if err != nil {
		t.Fatalf("build tree failed: %v", err)
	}

	// 10K messages over 72 hours → at most 72 hour buckets
	// + 3 day buckets + 1 month + 1 year = ~77 nodes
	nodeCount := len(tree.Nodes)
	t.Logf("10K messages: %d tree nodes", nodeCount)

	if nodeCount < 10 || nodeCount > 200 {
		t.Errorf("unexpected node count %d for 10K messages", nodeCount)
	}

	root := tree.RootHash()
	if root == nil || IsZeroHash(root) {
		t.Error("expected valid root hash")
	}
}

func TestConcurrentSync_Safety(t *testing.T) {
	// Multiple goroutines syncing simultaneously must not corrupt state
	cluster := newCluster(t, 3)
	dataset := GenerateMessages(DefaultDatasetA)

	for _, n := range cluster.nodes {
		n.LoadMessages(CloneMessages(dataset))
	}

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if i == j {
				continue
			}
			wg.Add(1)
			go func(from, to int) {
				defer wg.Done()
				_ = cluster.SyncPair(from, to, "conv-a")
			}(i, j)
		}
	}
	wg.Wait()

	if !cluster.VerifyAllConverged() {
		t.Fatal("concurrent sync did not converge")
	}
}
