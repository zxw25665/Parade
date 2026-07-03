package sync

import (
	"testing"

	"github.com/google/uuid"
	"parade/internal/core/db"
)

func makeMsg(hlc, content string) *db.Message {
	return &db.Message{
		ID:             uuid.New().String(),
		HLC:            hlc,
		Content:        []byte(content),
		ConversationID: "test-conv",
	}
}

func TestComputeMessageHash_Deterministic(t *testing.T) {
	h1 := ComputeMessageHash("conv1", "2026-04-13T10:00:00.000Z_0001_a", []byte("hello"))
	h2 := ComputeMessageHash("conv1", "2026-04-13T10:00:00.000Z_0001_a", []byte("hello"))
	if string(h1) != string(h2) {
		t.Error("message hash should be deterministic")
	}
}

func TestComputeMessageHash_DifferentContent(t *testing.T) {
	h1 := ComputeMessageHash("conv1", "2026-04-13T10:00:00.000Z_0001_a", []byte("hello"))
	h2 := ComputeMessageHash("conv1", "2026-04-13T10:00:00.000Z_0001_a", []byte("world"))
	if string(h1) == string(h2) {
		t.Error("different content should produce different hashes")
	}
}

func TestComputeBucketHash_OrderIndependent(t *testing.T) {
	h1 := ComputeBucketHash([][]byte{
		{0x01, 0x02},
		{0x03, 0x04},
	})
	h2 := ComputeBucketHash([][]byte{
		{0x03, 0x04},
		{0x01, 0x02},
	})
	if string(h1) != string(h2) {
		t.Error("bucket hash should be order-independent (sorted)")
	}
}

func TestBuildMerkleTree_Empty(t *testing.T) {
	tree, err := BuildMerkleTree("conv1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tree.Nodes) != 0 {
		t.Errorf("expected empty tree, got %d nodes", len(tree.Nodes))
	}
	if tree.RootHash() != nil {
		t.Error("expected nil root hash for empty tree")
	}
}

func TestBuildMerkleTree_SingleMessage(t *testing.T) {
	msgs := []*db.Message{
		makeMsg("2026-04-13T10:30:00.000Z_0001_a", "hello"),
	}
	tree, err := BuildMerkleTree("conv1", msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 4 nodes: year, month, day, hour
	if len(tree.Nodes) != 4 {
		t.Errorf("expected 4 nodes, got %d", len(tree.Nodes))
	}

	// Check hour bucket exists
	hour := tree.GetBucket("2026-04-13T10")
	if hour == nil {
		t.Fatal("expected hour bucket")
	}
	if hour.Level != LevelHour {
		t.Errorf("expected level %d, got %d", LevelHour, hour.Level)
	}
	if hour.MessageCount != 1 {
		t.Errorf("expected 1 message, got %d", hour.MessageCount)
	}

	// Root hash should be non-nil
	root := tree.RootHash()
	if root == nil {
		t.Fatal("expected non-nil root hash")
	}
	if IsZeroHash(root) {
		t.Error("expected non-zero root hash")
	}
}

func TestBuildMerkleTree_MultipleMessagesSameHour(t *testing.T) {
	msgs := []*db.Message{
		makeMsg("2026-04-13T10:30:00.000Z_0001_a", "first"),
		makeMsg("2026-04-13T10:31:00.000Z_0002_b", "second"),
	}
	tree, err := BuildMerkleTree("conv1", msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hour := tree.GetBucket("2026-04-13T10")
	if hour == nil {
		t.Fatal("expected hour bucket")
	}
	if hour.MessageCount != 2 {
		t.Errorf("expected 2 messages, got %d", hour.MessageCount)
	}
}

func TestBuildMerkleTree_MultipleHours(t *testing.T) {
	msgs := []*db.Message{
		makeMsg("2026-04-13T10:00:00.000Z_0001_a", "morning"),
		makeMsg("2026-04-13T14:00:00.000Z_0002_b", "afternoon"),
	}
	tree, err := BuildMerkleTree("conv1", msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 5 nodes: year, month, day, 2 hours
	if len(tree.Nodes) != 5 {
		t.Errorf("expected 5 nodes, got %d", len(tree.Nodes))
	}

	day := tree.GetBucket("2026-04-13")
	if day == nil {
		t.Fatal("expected day bucket")
	}
	if day.Level != LevelDay {
		t.Errorf("expected level %d", LevelDay)
	}
	if day.MessageCount != 2 {
		t.Errorf("expected 2 hour buckets, got %d", day.MessageCount)
	}
}

func TestBuildMerkleTree_MultipleYears(t *testing.T) {
	msgs := []*db.Message{
		makeMsg("2025-12-31T23:00:00.000Z_0001_a", "old"),
		makeMsg("2026-01-01T00:00:00.000Z_0002_b", "new"),
	}
	tree, err := BuildMerkleTree("conv1", msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	year2025 := tree.GetBucket("2025")
	year2026 := tree.GetBucket("2026")
	if year2025 == nil || year2026 == nil {
		t.Fatal("expected both year buckets")
	}

	root := tree.RootHash()
	if root == nil {
		t.Fatal("expected non-nil root hash")
	}
}

func TestBuildMerkleTree_RootHashChanges(t *testing.T) {
	msgs1 := []*db.Message{
		makeMsg("2026-04-13T10:00:00.000Z_0001_a", "hello"),
	}
	msgs2 := []*db.Message{
		makeMsg("2026-04-13T10:00:00.000Z_0001_a", "world"),
	}

	tree1, _ := BuildMerkleTree("conv1", msgs1)
	tree2, _ := BuildMerkleTree("conv1", msgs2)

	if string(tree1.RootHash()) == string(tree2.RootHash()) {
		t.Error("different content should produce different root hashes")
	}
}

func TestGetChildren(t *testing.T) {
	msgs := []*db.Message{
		makeMsg("2026-04-13T10:00:00.000Z_0001_a", "a"),
		makeMsg("2026-04-13T14:00:00.000Z_0002_b", "b"),
		makeMsg("2026-04-14T10:00:00.000Z_0003_c", "c"),
	}
	tree, _ := BuildMerkleTree("conv1", msgs)

	// Year 2026 should have 1 child (month 04)
	children := tree.GetChildren("2026", LevelYear)
	if len(children) != 1 {
		t.Errorf("expected 1 month child, got %d", len(children))
	}

	// Month 2026-04 should have 2 children (days 13, 14)
	children = tree.GetChildren("2026-04", LevelMonth)
	if len(children) != 2 {
		t.Errorf("expected 2 day children, got %d", len(children))
	}
}

func TestMissingBuckets(t *testing.T) {
	msgs := []*db.Message{
		makeMsg("2026-04-13T10:00:00.000Z_0001_a", "hello"),
	}
	tree, _ := BuildMerkleTree("conv1", msgs)

	// Remote has the same bucket
	remote := []BucketInfo{
		{Path: "2026", Hash: tree.GetBucket("2026").Hash},
	}
	missing := tree.MissingBuckets(remote)
	if len(missing) != 0 {
		t.Errorf("expected 0 missing, got %d", len(missing))
	}

	// Remote has a different hash
	remote2 := []BucketInfo{
		{Path: "2026", Hash: make([]byte, 32)},
	}
	missing2 := tree.MissingBuckets(remote2)
	if len(missing2) != 1 {
		t.Errorf("expected 1 missing, got %d", len(missing2))
	}

	// Remote has a bucket we don't have
	remote3 := []BucketInfo{
		{Path: "2025", Hash: []byte{0x01}},
	}
	missing3 := tree.MissingBuckets(remote3)
	if len(missing3) != 1 {
		t.Errorf("expected 1 missing, got %d", len(missing3))
	}
}

func TestContentHash(t *testing.T) {
	h := ContentHash([]byte("hello"))
	if len(h) != 32 {
		t.Errorf("expected 32 bytes, got %d", len(h))
	}
}

func TestHLCToTime(t *testing.T) {
	ts, err := HLCToTime("2026-04-13T10:30:00.000Z_0001_abc12345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts.Year() != 2026 || ts.Month() != 4 || ts.Day() != 13 || ts.Hour() != 10 {
		t.Errorf("unexpected time: %v", ts)
	}
}

func TestBucketLevelName(t *testing.T) {
	if BucketLevelName(LevelYear) != "year" {
		t.Errorf("got %q", BucketLevelName(LevelYear))
	}
	if BucketLevelName(LevelHour) != "hour" {
		t.Errorf("got %q", BucketLevelName(LevelHour))
	}
}
