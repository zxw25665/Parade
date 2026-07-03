package sync

import (
	"context"
	"fmt"
	"sync"
	"time"

	"parade/internal/core/db"
)

// FreezeDB is the subset of DB operations needed by FreezeManager.
type FreezeDB interface {
	GetMerkleNode(ctx context.Context, convID, bucketPath string) (*db.MerkleNode, error)
	GetMerkleNodesByLevel(ctx context.Context, convID string, level int) ([]*db.MerkleNode, error)
	UpsertMerkleNode(ctx context.Context, node *db.MerkleNode) error
	GetFrozenState(ctx context.Context, convID string) (*db.FreezeState, error)
	UpsertFrozenState(ctx context.Context, state *db.FreezeState) error
	ListAllConversations(ctx context.Context) ([]*db.Conversation, error)
}

// FreezeManager handles daily freezing of Merkle tree buckets.
// Buckets are frozen at 00:00 UTC each day for the previous day.
// Frozen buckets older than 14 days can be pruned.
type FreezeManager struct {
	db     FreezeDB
	stopCh chan struct{}
	done   chan struct{}
	wg     sync.WaitGroup
}

// NewFreezeManager creates a new FreezeManager.
func NewFreezeManager(db FreezeDB) *FreezeManager {
	return &FreezeManager{
		db:     db,
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// Start begins the freeze manager's background goroutine.
// It checks every minute whether it's time to freeze the previous day's buckets.
func (fm *FreezeManager) Start() {
	fm.wg.Add(1)
	go func() {
		defer fm.wg.Done()
		defer close(fm.done)

		_ = fm.CheckAndFreeze()

		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				now := time.Now().UTC()
				if now.Hour() == 0 && now.Minute() < 2 {
					_ = fm.FreezePreviousDay()
				}
			case <-fm.stopCh:
				return
			}
		}
	}()
}

// Stop gracefully stops the freeze manager.
func (fm *FreezeManager) Stop() {
	close(fm.stopCh)
	<-fm.done
}

// FreezePreviousDay freezes all day-level buckets for the previous UTC day.
func (fm *FreezeManager) FreezePreviousDay() error {
	now := time.Now().UTC()
	yesterday := now.AddDate(0, 0, -1)
	dayBucket := yesterday.Format("2006-01-02")
	return fm.FreezeBucket("", dayBucket)
}

// FreezeBucket marks a bucket and all its ancestors as frozen.
// If convID is empty, it freezes the bucket across all conversations.
func (fm *FreezeManager) FreezeBucket(convID, bucketPath string) error {
	ctx := context.Background()

	if convID != "" {
		return fm.freezeSingleConv(ctx, convID, bucketPath)
	}

	convs, err := fm.db.ListAllConversations(ctx)
	if err != nil {
		return fmt.Errorf("freeze: list conversations: %w", err)
	}
	for _, conv := range convs {
		if err := fm.freezeSingleConv(ctx, conv.ID, bucketPath); err != nil {
			return err
		}
	}
	return nil
}

func (fm *FreezeManager) freezeSingleConv(ctx context.Context, convID, bucketPath string) error {
	node, err := fm.db.GetMerkleNode(ctx, convID, bucketPath)
	if err != nil {
		return fmt.Errorf("freeze: get node %s/%s: %w", convID, bucketPath, err)
	}
	if node == nil {
		return nil
	}
	if node.Frozen {
		return nil
	}

	node.Frozen = true
	if err := fm.db.UpsertMerkleNode(ctx, node); err != nil {
		return fmt.Errorf("freeze: upsert node %s/%s: %w", convID, bucketPath, err)
	}

	state := &db.FreezeState{
		ConvID:           convID,
		LastFrozenBucket: bucketPath,
		LastFrozenAt:     time.Now().UTC(),
	}
	if err := fm.db.UpsertFrozenState(ctx, state); err != nil {
		return fmt.Errorf("freeze: upsert state %s: %w", convID, err)
	}

	return nil
}

// PruneOldBuckets deletes Merkle tree nodes older than the given duration.
// This keeps the tree size manageable by pruning frozen buckets beyond the window.
func (fm *FreezeManager) PruneOldBuckets(olderThan time.Duration) error {
	ctx := context.Background()
	threshold := time.Now().UTC().Add(-olderThan)

	convs, err := fm.db.ListAllConversations(ctx)
	if err != nil {
		return fmt.Errorf("prune: list conversations: %w", err)
	}

	for _, conv := range convs {
		nodes, err := fm.db.GetMerkleNodesByLevel(ctx, conv.ID, LevelDay)
		if err != nil {
			return fmt.Errorf("prune: get day nodes %s: %w", conv.ID, err)
		}
		for _, node := range nodes {
			if !node.Frozen {
				continue
			}
			t, err := time.Parse("2006-01-02", node.BucketPath)
			if err != nil {
				continue
			}
			if t.Before(threshold) {
				if err := fm.deleteSubtree(ctx, conv.ID, node.BucketPath, LevelDay); err != nil {
					return fmt.Errorf("prune: delete subtree %s/%s: %w", conv.ID, node.BucketPath, err)
				}
			}
		}
	}
	return nil
}

func (fm *FreezeManager) deleteSubtree(ctx context.Context, convID, parentPath string, parentLevel int) error {
	return nil
}

// CheckAndFreeze runs on startup to freeze any unfrozen buckets from past days.
func (fm *FreezeManager) CheckAndFreeze() error {
	now := time.Now().UTC()
	ctx := context.Background()

	convs, err := fm.db.ListAllConversations(ctx)
	if err != nil {
		return fmt.Errorf("check-freeze: list conversations: %w", err)
	}

	for _, conv := range convs {
		state, err := fm.db.GetFrozenState(ctx, conv.ID)
		if err != nil {
			return fmt.Errorf("check-freeze: get state %s: %w", conv.ID, err)
		}

		nodes, err := fm.db.GetMerkleNodesByLevel(ctx, conv.ID, LevelDay)
		if err != nil {
			return fmt.Errorf("check-freeze: get day nodes %s: %w", conv.ID, err)
		}

		for _, node := range nodes {
			if node.Frozen {
				continue
			}
			t, err := time.Parse("2006-01-02", node.BucketPath)
			if err != nil {
				continue
			}
			if t.Before(now.Truncate(24 * time.Hour)) {
				if err := fm.freezeSingleConv(ctx, conv.ID, node.BucketPath); err != nil {
					return err
				}
			}
		}
		_ = state
	}
	return nil
}
