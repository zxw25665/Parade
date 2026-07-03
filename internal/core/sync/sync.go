package sync

import (
	"context"
	"fmt"
	"sort"

	"parade/internal/core/db"
	"parade/internal/core/logger"
)

// MerkleDB is the subset of DB operations needed by the sync orchestrator.
type MerkleDB interface {
	FreezeDB
	GetMerkleNodesByParent(ctx context.Context, convID, parentPath string) ([]*db.MerkleNode, error)
	DeleteMerkleNodesByConv(ctx context.Context, convID string) error
	GetMessagesInBucket(ctx context.Context, convID, bucketPath string, level int) ([]*db.Message, error)
	GetConversationMessagesSinceHLC(ctx context.Context, convID string, sinceHLC string, limit int) ([]*db.Message, error)
	RunInTx(ctx context.Context, fn func(tx db.DBTx) error) error
	InsertMessage(ctx context.Context, msg *db.Message) error
	UpdateConversationLastHLC(ctx context.Context, convID string, hlc string) error
}

// MerkleNetwork is the network interface needed by the sync orchestrator.
type MerkleNetwork interface {
	SendMerkleRootRequest(targetUUID, convID string) ([]byte, error)
	SendBucketCompareRequest(targetUUID, convID string, level int, paths []string) ([]BucketInfo, error)
	SendFetchMessagesRequest(targetUUID, convID, bucketPath, sinceHLC string) ([]*db.Message, error)
	SendPushMessages(targetUUID, convID string, messages []*db.Message) error
}

// MerkleSyncHandler is the interface for handling incoming Merkle sync requests.
// The network layer uses this to dispatch incoming sync protocol messages.
type MerkleSyncHandler interface {
	HandleMerkleRootRequest(ctx context.Context, convID string) ([]byte, error)
	HandleBucketCompare(ctx context.Context, convID string, level int, paths []string) ([]BucketInfo, error)
	HandleFetchMessages(ctx context.Context, convID, bucketPath, sinceHLC string) ([]*db.Message, error)
}

// SyncOrchestrator coordinates the level-by-level Merkle tree sync between peers.
type SyncOrchestrator struct {
	db     MerkleDB
	net    MerkleNetwork
	logr   logger.Logger
}

// NewSyncOrchestrator creates a new SyncOrchestrator.
func NewSyncOrchestrator(db MerkleDB, net MerkleNetwork, logr logger.Logger) *SyncOrchestrator {
	return &SyncOrchestrator{
		db:   db,
		net:  net,
		logr: logr,
	}
}

func (s *SyncOrchestrator) log(level logger.LogLevel, source, msg string) {
	if s.logr != nil {
		switch level {
		case logger.Trace:
			s.logr.Trace(source, msg)
		case logger.Debug:
			s.logr.Debug(source, msg)
		case logger.Info:
			s.logr.Info(source, msg)
		case logger.Warning:
			s.logr.Warn(source, msg)
		case logger.Error:
			s.logr.Error(source, msg)
		}
	}
}

func trunc8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func trunc16(s string) string {
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

// SyncConversation performs a full Merkle tree sync for a single conversation with a peer.
// It compares the Merkle tree level by level from top to bottom, fetching only
// the messages that are missing or diverged.
func (s *SyncOrchestrator) SyncConversation(ctx context.Context, peerUUID, convID string) error {
	s.log(logger.Debug, "sync", fmt.Sprintf("merkle sync start conv=%s peer=%s", trunc8(convID), trunc16(peerUUID)))

	localTree, err := s.buildLocalTree(ctx, convID)
	if err != nil {
		return fmt.Errorf("sync: build local tree: %w", err)
	}

	remoteRoot, err := s.net.SendMerkleRootRequest(peerUUID, convID)
	if err != nil {
		return fmt.Errorf("sync: get remote root: %w", err)
	}

	localRoot := localTree.RootHash()

	if len(localRoot) == 0 && len(remoteRoot) == 0 {
		s.log(logger.Debug, "sync", fmt.Sprintf("merkle sync conv=%s: both empty, in sync", trunc8(convID)))
		return nil
	}
	if len(localRoot) > 0 && len(remoteRoot) > 0 && string(localRoot) == string(remoteRoot) {
		s.log(logger.Debug, "sync", fmt.Sprintf("merkle sync conv=%s: roots match, in sync", trunc8(convID)))
		return nil
	}

	s.log(logger.Debug, "sync", fmt.Sprintf("merkle sync conv=%s: roots differ, starting level-by-level comparison", trunc8(convID)))

	totalSynced, err := s.compareLevel(ctx, peerUUID, convID, localTree, LevelYear, "")
	if err != nil {
		return fmt.Errorf("sync: level comparison: %w", err)
	}

	if totalSynced > 0 {
		s.log(logger.Info, "sync", fmt.Sprintf("merkle sync conv=%s: synced %d messages from %s", trunc8(convID), totalSynced, trunc16(peerUUID)))
	} else {
		s.log(logger.Debug, "sync", fmt.Sprintf("merkle sync conv=%s: no new messages from %s", trunc8(convID), trunc16(peerUUID)))
	}

	return nil
}

// compareLevel recursively compares buckets at a given level between local and remote.
// Returns the number of messages synced.
func (s *SyncOrchestrator) compareLevel(ctx context.Context, peerUUID, convID string, localTree *MerkleTree, level int, parentPath string) (int, error) {
	var localBuckets []*db.MerkleNode
	if parentPath == "" {
		localBuckets = localTree.GetLevelBuckets(level)
	} else {
		localBuckets = localTree.GetChildren(parentPath, level-1)
	}

	localPaths := make([]string, len(localBuckets))
	for i, b := range localBuckets {
		localPaths[i] = b.BucketPath
	}

	remoteBuckets, err := s.net.SendBucketCompareRequest(peerUUID, convID, level, localPaths)
	if err != nil {
		return 0, fmt.Errorf("compare level %d: %w", level, err)
	}

	remoteMap := make(map[string]BucketInfo)
	for _, rb := range remoteBuckets {
		remoteMap[rb.Path] = rb
	}

	// Build local bucket map
	localMap := make(map[string]*db.MerkleNode)
	for _, lb := range localBuckets {
		localMap[lb.BucketPath] = lb
	}

	// Collect all unique bucket paths from both sides
	allPaths := make(map[string]bool)
	for _, p := range localPaths {
		allPaths[p] = true
	}
	for _, rb := range remoteBuckets {
		allPaths[rb.Path] = true
	}

	totalSynced := 0

	// Sort paths for deterministic processing
	sortedPaths := make([]string, 0, len(allPaths))
	for p := range allPaths {
		sortedPaths = append(sortedPaths, p)
	}
	sort.Strings(sortedPaths)

	for _, path := range sortedPaths {
		localNode := localMap[path]
		remoteInfo, remoteExists := remoteMap[path]

		if !remoteExists {
			// Bucket exists locally but not remotely → remote is missing these messages
			// We need to send our messages to the remote
			s.log(logger.Debug, "sync", fmt.Sprintf("conv=%s: local-only bucket %s (level %d), sending messages", trunc8(convID), path, level))
			synced, err := s.sendBucketMessages(ctx, peerUUID, convID, path, level)
			if err != nil {
				return totalSynced, err
			}
			totalSynced += synced
			continue
		}

		if localNode == nil {
			// Bucket exists on remote but not locally → we need to fetch
			s.log(logger.Debug, "sync", fmt.Sprintf("conv=%s: remote-only bucket %s (level %d), fetching messages", trunc8(convID), path, level))
			synced, err := s.fetchBucketMessages(ctx, peerUUID, convID, path, level)
			if err != nil {
				return totalSynced, err
			}
			totalSynced += synced
			continue
		}

		// Both sides have this bucket
		if localNode.Frozen {
			// Local is frozen - trust local state, skip comparison
			continue
		}

		if remoteInfo.Frozen {
			// Remote is frozen - trust remote state, fetch if hash differs
			if string(localNode.Hash) != string(remoteInfo.Hash) {
				s.log(logger.Debug, "sync", fmt.Sprintf("conv=%s: remote-frozen bucket %s hash differs, fetching", trunc8(convID), path))
				synced, err := s.fetchBucketMessages(ctx, peerUUID, convID, path, level)
				if err != nil {
					return totalSynced, err
				}
				totalSynced += synced
			}
			continue
		}

		// Both active - compare hashes
		if string(localNode.Hash) == string(remoteInfo.Hash) {
			// Hashes match - subtree is in sync
			continue
		}

		// Hashes differ - drill down
		if level < LevelHour {
			s.log(logger.Debug, "sync", fmt.Sprintf("conv=%s: bucket %s hash differs, drilling down to level %d", trunc8(convID), path, level+1))
			synced, err := s.compareLevel(ctx, peerUUID, convID, localTree, level+1, path)
			if err != nil {
				return totalSynced, err
			}
			totalSynced += synced
		} else {
			// At hour level: bidirectional exchange.
			// First fetch what the remote has that we don't.
			s.log(logger.Debug, "sync", fmt.Sprintf("conv=%s: hour bucket %s hash differs, bidirectional exchange", trunc8(convID), path))
			fetched, err := s.fetchBucketMessages(ctx, peerUUID, convID, path, level)
			if err != nil {
				return totalSynced, err
			}
			totalSynced += fetched
			// Then push what we have that the remote doesn't.
			pushed, err := s.sendBucketMessages(ctx, peerUUID, convID, path, level)
			if err != nil {
				return totalSynced, err
			}
			totalSynced += pushed
		}
	}

	return totalSynced, nil
}

// fetchBucketMessages fetches all messages in a time bucket from the remote peer.
func (s *SyncOrchestrator) fetchBucketMessages(ctx context.Context, peerUUID, convID, bucketPath string, level int) (int, error) {
	startHLC, endHLC, err := BucketTimeRange(bucketPath, level)
	if err != nil {
		return 0, fmt.Errorf("fetch bucket %s: %w", bucketPath, err)
	}

	// Fetch messages from remote peer
	messages, err := s.net.SendFetchMessagesRequest(peerUUID, convID, bucketPath, startHLC)
	if err != nil {
		return 0, fmt.Errorf("fetch messages %s: %w", bucketPath, err)
	}

	if len(messages) == 0 {
		return 0, nil
	}

	// Filter messages within the bucket's time range
	var inRange []*db.Message
	for _, msg := range messages {
		if msg.HLC >= startHLC && msg.HLC < endHLC {
			inRange = append(inRange, msg)
		}
	}

	if len(inRange) == 0 {
		return 0, nil
	}

	// Batch insert in transaction
	if err := s.db.RunInTx(ctx, func(tx db.DBTx) error {
		for _, msg := range inRange {
			if err := tx.InsertMessageTx(ctx, msg); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return 0, fmt.Errorf("insert messages %s: %w", bucketPath, err)
	}

	// Update conversation last HLC
	lastHLC := inRange[len(inRange)-1].HLC
	_ = s.db.UpdateConversationLastHLC(ctx, convID, lastHLC)

	s.log(logger.Debug, "sync", fmt.Sprintf("conv=%s: fetched %d messages for bucket %s", trunc8(convID), len(inRange), bucketPath))
	return len(inRange), nil
}

// sendBucketMessages sends all messages in a time bucket to the remote peer.
func (s *SyncOrchestrator) sendBucketMessages(ctx context.Context, peerUUID, convID, bucketPath string, level int) (int, error) {
	messages, err := s.db.GetMessagesInBucket(ctx, convID, bucketPath, level)
	if err != nil {
		return 0, fmt.Errorf("get messages for bucket %s: %w", bucketPath, err)
	}
	if len(messages) == 0 {
		return 0, nil
	}
	if err := s.net.SendPushMessages(peerUUID, convID, messages); err != nil {
		return 0, fmt.Errorf("push messages %s: %w", bucketPath, err)
	}
	s.log(logger.Debug, "sync", fmt.Sprintf("conv=%s: pushed %d messages for bucket %s to %s", trunc8(convID), len(messages), bucketPath, trunc16(peerUUID)))
	return len(messages), nil
}

// buildLocalTree constructs a Merkle tree from locally stored messages.
// It first checks the DB for existing tree nodes; if none exist, it rebuilds from messages.
func (s *SyncOrchestrator) buildLocalTree(ctx context.Context, convID string) (*MerkleTree, error) {
	// Try to load existing tree nodes from DB
	nodes, err := s.db.GetMerkleNodesByLevel(ctx, convID, LevelYear)
	if err != nil {
		return nil, err
	}

	if len(nodes) > 0 {
		// Tree exists in DB, load all nodes
		tree := NewMerkleTree(convID)
		for level := LevelYear; level <= LevelHour; level++ {
			levelNodes, err := s.db.GetMerkleNodesByLevel(ctx, convID, level)
			if err != nil {
				return nil, err
			}
			for _, node := range levelNodes {
				tree.Nodes[node.BucketPath] = node
			}
		}
		return tree, nil
	}

	// No existing tree, rebuild from messages
	// Fetch all messages for this conversation (up to a reasonable limit)
	allMessages, err := s.db.GetConversationMessagesSinceHLC(ctx, convID, "", 100000)
	if err != nil {
		return nil, fmt.Errorf("get messages for tree rebuild: %w", err)
	}

	if len(allMessages) == 0 {
		return NewMerkleTree(convID), nil
	}

	tree, err := BuildMerkleTree(convID, allMessages)
	if err != nil {
		return nil, fmt.Errorf("build merkle tree: %w", err)
	}

	// Persist tree nodes to DB
	for _, node := range tree.Nodes {
		if err := s.db.UpsertMerkleNode(ctx, node); err != nil {
			return nil, fmt.Errorf("persist merkle node %s: %w", node.BucketPath, err)
		}
	}

	return tree, nil
}

// HandleMerkleRootRequest handles an incoming Merkle root request from a peer.
// Returns the root hash of the local Merkle tree for the given conversation.
func (s *SyncOrchestrator) HandleMerkleRootRequest(ctx context.Context, convID string) ([]byte, error) {
	tree, err := s.buildLocalTree(ctx, convID)
	if err != nil {
		return nil, err
	}
	root := tree.RootHash()
	if root == nil {
		return make([]byte, 32), nil // zero hash for empty tree
	}
	return root, nil
}

// HandleBucketCompare handles an incoming bucket comparison request.
// Returns the bucket info for the requested paths at the given level.
func (s *SyncOrchestrator) HandleBucketCompare(ctx context.Context, convID string, level int, paths []string) ([]BucketInfo, error) {
	var result []BucketInfo

	for _, path := range paths {
		node, err := s.db.GetMerkleNode(ctx, convID, path)
		if err != nil {
			return nil, fmt.Errorf("get node %s: %w", path, err)
		}
		if node == nil {
			continue // bucket doesn't exist locally
		}
		result = append(result, BucketInfo{
			Path:   node.BucketPath,
			Hash:   node.Hash,
			Frozen: node.Frozen,
		})
	}

	// Also include any buckets we have that the requester didn't ask about
	// (they may not know about them)
	if len(paths) > 0 {
		existing, err := s.db.GetMerkleNodesByLevel(ctx, convID, level)
		if err != nil {
			return nil, err
		}
		pathSet := make(map[string]bool)
		for _, p := range paths {
			pathSet[p] = true
		}
		for _, node := range existing {
			if !pathSet[node.BucketPath] {
				result = append(result, BucketInfo{
					Path:   node.BucketPath,
					Hash:   node.Hash,
					Frozen: node.Frozen,
				})
			}
		}
	}

	return result, nil
}

// HandleFetchMessages handles an incoming request to fetch messages in a time bucket.
func (s *SyncOrchestrator) HandleFetchMessages(ctx context.Context, convID, bucketPath, sinceHLC string) ([]*db.Message, error) {
	messages, err := s.db.GetMessagesInBucket(ctx, convID, bucketPath, LevelHour)
	if err != nil {
		return nil, fmt.Errorf("get messages in bucket %s: %w", bucketPath, err)
	}

	if sinceHLC != "" {
		var filtered []*db.Message
		for _, msg := range messages {
			if msg.HLC > sinceHLC {
				filtered = append(filtered, msg)
			}
		}
		return filtered, nil
	}

	return messages, nil
}
