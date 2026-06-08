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
	Phase1     PhaseResult `json:"phase1"`     // gRPC Dial + Handshake
	Phase2     PhaseResult `json:"phase2"`     // team-key challenge exchange
	Phase3Send PhaseResult `json:"phase3Send"` // test message sent
	Phase3Recv PhaseResult `json:"phase3Recv"` // ack received
}
