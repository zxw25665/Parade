package network

import "time"

// PeerInfo represents a known peer on the LAN.
type PeerInfo struct {
	PeerUUID  string
	IPAddress string
}

// PeerStatus represents a peer with online/offline status.
type PeerStatus struct {
	PeerInfo
	Status        string    // "online" or "offline"
	LastHeartbeat time.Time
	LastOnlineAt  time.Time
}

const (
	PeerStatusOnline  = "online"
	PeerStatusOffline = "offline"
)

// PhaseResult reports the outcome of one connection-test step.
type PhaseResult struct {
	Success bool   `json:"success"`
	Label   string `json:"label"`
	Error   string `json:"error"`
}

// PeerConnectResult aggregates the three-phase test result for a peer.
type PeerConnectResult struct {
	IP         string      `json:"ip"`
	PubKey     string      `json:"pubkey"`
	Phase1     PhaseResult `json:"phase1"`     // libp2p connect + Handshake
	Phase2     PhaseResult `json:"phase2"`     // team-key challenge exchange
	Phase3Send PhaseResult `json:"phase3Send"` // test message sent
	Phase3Recv PhaseResult `json:"phase3Recv"` // ack received
}

// BrowseEntry represents a directory entry in remote file browsing.
// Replaces the protobuf-generated pb.BrowseEntry.
type BrowseEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	IsDirectory bool   `json:"is_directory"`
	Size        int64  `json:"size"`
	Hash        string `json:"hash"`
}

// FileChunk represents a chunk of a file during transfer.
// Replaces the protobuf-generated pb.FileChunk.
type FileChunk struct {
	TaskId    string `json:"task_id"`
	PeerId    string `json:"peer_id"`
	FilePath  string `json:"file_path"`
	Offset    int64  `json:"offset"`
	Data      []byte `json:"data"`
	TotalSize int64  `json:"total_size"`
	Eof       bool   `json:"eof"`
	FileHash  string `json:"file_hash"`
}
