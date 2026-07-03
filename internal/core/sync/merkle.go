package sync

import (
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/zeebo/blake3"

	"parade/internal/core/db"
)

// BucketInfo is used for network exchange of bucket hashes.
type BucketInfo struct {
	Path   string `json:"path"`
	Hash   []byte `json:"hash"`   // hex-encoded in JSON
	Frozen bool   `json:"frozen"`
}

// MerkleTree represents a complete Merkle tree for a conversation.
// Nodes are keyed by bucket_path.
type MerkleTree struct {
	ConvID string
	Nodes  map[string]*db.MerkleNode // keyed by bucket_path
}

// NewMerkleTree creates an empty Merkle tree for a conversation.
func NewMerkleTree(convID string) *MerkleTree {
	return &MerkleTree{
		ConvID: convID,
		Nodes:  make(map[string]*db.MerkleNode),
	}
}

// ContentHash returns the BLAKE3 hash of raw content bytes.
func ContentHash(content []byte) []byte {
	h := blake3.Sum256(content)
	return h[:]
}

// ComputeMessageHash computes the leaf hash for a single message.
// Format: BLAKE3(convID + "|" + hlc + "|" + contentHash)
func ComputeMessageHash(convID, hlc string, content []byte) []byte {
	contentH := ContentHash(content)
	hasher := blake3.New()
	hasher.Write([]byte(convID))
	hasher.Write([]byte("|"))
	hasher.Write([]byte(hlc))
	hasher.Write([]byte("|"))
	hasher.Write(contentH)
	return hasher.Sum(nil)
}

// ComputeBucketHash computes the hash for a bucket from its child hashes.
// Child hashes are sorted lexicographically before hashing.
func ComputeBucketHash(childHashes [][]byte) []byte {
	if len(childHashes) == 0 {
		return make([]byte, 32) // zero hash for empty bucket
	}

	// Sort child hashes lexicographically for deterministic ordering
	sort.Slice(childHashes, func(i, j int) bool {
		return string(childHashes[i]) < string(childHashes[j])
	})

	hasher := blake3.New()
	for _, h := range childHashes {
		hasher.Write(h)
	}
	return hasher.Sum(nil)
}

// BuildMerkleTree constructs a Merkle tree from a set of messages for a conversation.
// Messages are grouped by time bucket (year → month → day → hour → message),
// and hashes are computed bottom-up.
func BuildMerkleTree(convID string, messages []*db.Message) (*MerkleTree, error) {
	tree := NewMerkleTree(convID)
	if len(messages) == 0 {
		return tree, nil
	}

	// Phase 1: Group messages by hour bucket and compute message hashes
	hourBuckets := make(map[string][]*db.Message) // hour_path → messages
	for _, msg := range messages {
		hourPath, err := BucketPath(LevelHour, msg.HLC)
		if err != nil {
			return nil, fmt.Errorf("sync: build merkle tree: %w", err)
		}
		hourBuckets[hourPath] = append(hourBuckets[hourPath], msg)
	}

	// Phase 2: Compute message-level hashes and hour-level hashes
	dayBuckets := make(map[string][][]byte) // day_path → hour hashes
	for hourPath, msgs := range hourBuckets {
		// Sort messages by HLC within the hour
		sort.Slice(msgs, func(i, j int) bool {
			return msgs[i].HLC < msgs[j].HLC
		})

		// Compute individual message hashes
		var msgHashes [][]byte
		for _, msg := range msgs {
			msgHash := ComputeMessageHash(convID, msg.HLC, msg.Content)
			msgHashes = append(msgHashes, msgHash)
		}

		// Compute hour bucket hash from message hashes
		hourHash := ComputeBucketHash(msgHashes)
		tree.Nodes[hourPath] = &db.MerkleNode{
			ConvID:       convID,
			BucketPath:   hourPath,
			Level:        LevelHour,
			Hash:         hourHash,
			MessageCount: len(msgs),
		}

		// Group hour hash under its parent day
		dayPath := ParentBucketPath(hourPath, LevelHour)
		dayBuckets[dayPath] = append(dayBuckets[dayPath], hourHash)
	}

	// Phase 3: Compute day-level hashes
	monthBuckets := make(map[string][][]byte) // month_path → day hashes
	for dayPath, hourHashes := range dayBuckets {
		dayHash := ComputeBucketHash(hourHashes)
		tree.Nodes[dayPath] = &db.MerkleNode{
			ConvID:       convID,
			BucketPath:   dayPath,
			Level:        LevelDay,
			Hash:         dayHash,
			MessageCount: len(hourHashes), // number of hour buckets
		}

		monthPath := ParentBucketPath(dayPath, LevelDay)
		monthBuckets[monthPath] = append(monthBuckets[monthPath], dayHash)
	}

	// Phase 4: Compute month-level hashes
	yearBuckets := make(map[string][][]byte) // year_path → month hashes
	for monthPath, dayHashes := range monthBuckets {
		monthHash := ComputeBucketHash(dayHashes)
		tree.Nodes[monthPath] = &db.MerkleNode{
			ConvID:       convID,
			BucketPath:   monthPath,
			Level:        LevelMonth,
			Hash:         monthHash,
			MessageCount: len(dayHashes), // number of day buckets
		}

		yearPath := ParentBucketPath(monthPath, LevelMonth)
		yearBuckets[yearPath] = append(yearBuckets[yearPath], monthHash)
	}

	// Phase 5: Compute year-level hashes
	for yearPath, monthHashes := range yearBuckets {
		yearHash := ComputeBucketHash(monthHashes)
		tree.Nodes[yearPath] = &db.MerkleNode{
			ConvID:       convID,
			BucketPath:   yearPath,
			Level:        LevelYear,
			Hash:         yearHash,
			MessageCount: len(monthHashes), // number of month buckets
		}
	}

	return tree, nil
}

// RootHash returns the root hash of the Merkle tree.
// The root is computed from all year-level bucket hashes.
// Returns nil for an empty tree.
func (t *MerkleTree) RootHash() []byte {
	var yearHashes [][]byte
	for _, node := range t.Nodes {
		if node.Level == LevelYear {
			yearHashes = append(yearHashes, node.Hash)
		}
	}
	if len(yearHashes) == 0 {
		return nil
	}
	return ComputeBucketHash(yearHashes)
}

// RootHashHex returns the hex-encoded root hash.
func (t *MerkleTree) RootHashHex() string {
	h := t.RootHash()
	if h == nil {
		return ""
	}
	return hex.EncodeToString(h)
}

// GetBucket returns the node for a given bucket path, or nil if not found.
func (t *MerkleTree) GetBucket(path string) *db.MerkleNode {
	return t.Nodes[path]
}

// GetChildren returns all child nodes of a given bucket path at the next level.
func (t *MerkleTree) GetChildren(parentPath string, parentLevel int) []*db.MerkleNode {
	childLevel := parentLevel + 1
	if childLevel > MaxLevel {
		return nil
	}
	var children []*db.MerkleNode
	for _, node := range t.Nodes {
		if node.Level == childLevel && len(node.BucketPath) > len(parentPath) {
			// Check if the node's bucket path starts with the parent path
			if len(node.BucketPath) >= len(parentPath)+1 &&
				node.BucketPath[:len(parentPath)] == parentPath {
				children = append(children, node)
			}
		}
	}
	// Sort children by bucket path for deterministic ordering
	sort.Slice(children, func(i, j int) bool {
		return children[i].BucketPath < children[j].BucketPath
	})
	return children
}

// GetLevelBuckets returns all nodes at a given level.
func (t *MerkleTree) GetLevelBuckets(level int) []*db.MerkleNode {
	var nodes []*db.MerkleNode
	for _, node := range t.Nodes {
		if node.Level == level {
			nodes = append(nodes, node)
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].BucketPath < nodes[j].BucketPath
	})
	return nodes
}

// MissingBuckets compares this tree's buckets with a remote set and returns
// buckets that exist on the remote side but are missing or have different hashes locally.
// If a bucket is frozen locally, it is considered authoritative and not compared.
func (t *MerkleTree) MissingBuckets(remote []BucketInfo) []BucketInfo {
	var missing []BucketInfo
	for _, rb := range remote {
		local := t.GetBucket(rb.Path)
		if local == nil {
			// Bucket exists on remote but not locally → missing
			missing = append(missing, rb)
		} else if !local.Frozen && string(local.Hash) != string(rb.Hash) {
			// Hash differs and not frozen → needs reconciliation
			missing = append(missing, rb)
		}
		// If frozen and hash matches, skip (trust frozen state)
		// If frozen and hash differs, we trust our frozen state (remote is outdated)
	}
	return missing
}

// ToNodeSlice returns all nodes as a flat slice.
func (t *MerkleTree) ToNodeSlice() []*db.MerkleNode {
	nodes := make([]*db.MerkleNode, 0, len(t.Nodes))
	for _, node := range t.Nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

// NodeHashHex returns the hex-encoded hash of a Merkle node.
func NodeHashHex(n *db.MerkleNode) string {
	return hex.EncodeToString(n.Hash)
}

// IsZeroHash returns true if the hash is all zeros (empty bucket).
func IsZeroHash(hash []byte) bool {
	if len(hash) != 32 {
		return false
	}
	for _, b := range hash {
		if b != 0 {
			return false
		}
	}
	return true
}
