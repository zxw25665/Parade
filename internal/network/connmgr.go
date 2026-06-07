package network

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
	chatpb "parade/internal/network/pb/chatpb"
	pb "parade/internal/network/pb"
)

const (
	dialTimeout  = 1500 * time.Millisecond
	dialBackoff  = 500 * time.Millisecond
	dialAttempts = 3
)

// PeerConn manages all communication channels to a single peer.
type PeerConn struct {
	pubKey string
	ipAddr string

	mu        sync.Mutex
	conn      *grpc.ClientConn
	ctrlStream chatpb.ChatService_StreamChatClient
	sendCh     chan *chatpb.Envelope
	incoming   chatpb.ChatService_StreamChatServer
	incomingMu sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc

	logr logger.Logger

	heartbeatSeq    uint64
	lastHeartbeatAt time.Time
	heartbeatMu     sync.Mutex
}

// ConnMgr manages all peer connections.
type ConnMgr struct {
	peers map[string]*PeerConn
	mu    sync.RWMutex

	crypto      CryptoOps
	bus         EventPublisher
	logr        LogSink
	fileEngine  FileTransferEngine
	controlPort int

	// Pending file browse requests awaiting response
	pendingReqs   map[string]chan []byte
	pendingReqsMu sync.Mutex

	// flood dedup: recently forwarded message HLCs
	recentHLCs   map[string]time.Time
	recentHLCsMu sync.Mutex

	stopCh chan struct{}

	discovery *Discovery
}

func NewConnMgr(bus EventPublisher, crypto CryptoOps, logr LogSink, controlPort int) *ConnMgr {
	cm := &ConnMgr{
		peers:       make(map[string]*PeerConn),
		pendingReqs: make(map[string]chan []byte),
		recentHLCs:  make(map[string]time.Time),
		crypto:      crypto,
		bus:         bus,
		logr:        logr,
		controlPort: controlPort,
		discovery:   NewDiscovery(bus),
		stopCh:      make(chan struct{}),
	}
	cm.discovery.WithLogger(logr)
	go cm.runStatusTicker()
	return cm
}

func (cm *ConnMgr) runStatusTicker() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-cm.stopCh:
			return
		case <-ticker.C:
			cm.discovery.CheckTimeouts(45*time.Second, 0)
		}
	}
}

func (cm *ConnMgr) Stop() {
	close(cm.stopCh)
}

// AttachFileEngine sets the file transfer engine on the connection manager.
func (cm *ConnMgr) AttachFileEngine(fe FileTransferEngine) {
	cm.fileEngine = fe
}

// GetOrDial returns or creates a PeerConn for the given peer.
func (cm *ConnMgr) GetOrDial(pubKey, ipAddr string) (*PeerConn, error) {
	if pubKey == "" || ipAddr == "" {
		return nil, fmt.Errorf("pubKey and ipAddr are required")
	}

	cm.mu.RLock()
	pc, exists := cm.peers[pubKey]
	cm.mu.RUnlock()

	if exists && pc != nil {
		pc.mu.Lock()
		conn := pc.conn
		pc.mu.Unlock()
		if conn != nil && conn.GetState() != connectivity.Shutdown {
			return pc, nil
		}
		// conn is nil (incoming-only) or dead → proceed to dial and upgrade
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Check again under write lock — another goroutine may have completed the dial
	oldPc := cm.peers[pubKey]
	if oldPc != nil {
		oldPc.mu.Lock()
		if oldPc.conn != nil && oldPc.conn.GetState() != connectivity.Shutdown {
			oldPc.mu.Unlock()
			return oldPc, nil
		}
		oldPc.mu.Unlock()
	}

	cm.logr.Debug("dial", fmt.Sprintf("GetOrDial: dialing %s at %s:%d", truncateKey(pubKey), ipAddr, cm.controlPort))

	target := fmt.Sprintf("%s:%d", ipAddr, cm.controlPort)
	ctx, cancel := context.WithCancel(context.Background())

	var (
		conn *grpc.ClientConn
		err  error
	)
	for attempt := 1; attempt <= dialAttempts; attempt++ {
		conn, err = grpc.NewClient(target,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:    15 * time.Second,
				Timeout: 5 * time.Second,
			}),
		)
		if err == nil {
			conn.Connect()
			dialCtx, dialCancel := context.WithTimeout(ctx, dialTimeout)
			for conn.GetState() != connectivity.Ready {
				if !conn.WaitForStateChange(dialCtx, conn.GetState()) {
					conn.Close()
					err = fmt.Errorf("connection timeout to %s", target)
					break
				}
			}
			dialCancel()
		}
		if err == nil {
			break
		}
		cm.logr.Warn("dial",
			fmt.Sprintf("dial attempt %d/%d to %s failed: %v", attempt, dialAttempts, target, err))
		if attempt < dialAttempts {
			select {
			case <-ctx.Done():
				cancel()
				return nil, ctx.Err()
			case <-time.After(dialBackoff):
			}
		}
	}
	if err != nil {
		cancel()
		return nil, fmt.Errorf("dial %s failed after %d attempts: %w", target, dialAttempts, err)
	}

	pc = &PeerConn{
		pubKey: pubKey,
		ipAddr: ipAddr,
		conn:   conn,
		sendCh: make(chan *chatpb.Envelope, 64),
		ctx:    ctx,
		cancel: cancel,
		logr:   cm.logr,
		lastHeartbeatAt: time.Now(),
	}

	if err := pc.startPersistentStream(cm); err != nil {
		cancel()
		conn.Close()
		return nil, err
	}

	// Preserve incoming stream from old incoming-only PeerConn
	if oldPc != nil {
		oldPc.incomingMu.Lock()
		pc.incoming = oldPc.incoming
		oldPc.incoming = nil
		oldPc.incomingMu.Unlock()
	}

	cm.peers[pubKey] = pc
	cm.logr.Debug("dial", fmt.Sprintf("GetOrDial: connected to %s at %s:%d", truncateKey(pubKey), ipAddr, cm.controlPort))
	return pc, nil
}

// RegisterIncoming stores the server-side incoming stream on a PeerConn.
// If no PeerConn exists for this pubKey, an incoming-only PeerConn is created
// so that SendViaIncoming can reply through the bidi stream.
func (cm *ConnMgr) RegisterIncoming(pubKey string, stream chatpb.ChatService_StreamChatServer) {
	cm.mu.RLock()
	pc, exists := cm.peers[pubKey]
	cm.mu.RUnlock()
	if exists {
		pc.incomingMu.Lock()
		pc.incoming = stream
		pc.incomingMu.Unlock()
		return
	}
	// Create incoming-only PeerConn — no outgoing connection yet.
	// The peer's IP will be populated when we receive a Handshake or StreamChat message.
	pc = &PeerConn{
		pubKey:   pubKey,
		incoming: stream,
		logr:     cm.logr,
	}
	cm.mu.Lock()
	cm.peers[pubKey] = pc
	cm.mu.Unlock()
}

// UnregisterIncoming removes the incoming stream.
func (cm *ConnMgr) UnregisterIncoming(pubKey string) {
	cm.mu.RLock()
	pc, exists := cm.peers[pubKey]
	cm.mu.RUnlock()
	if exists {
		pc.incomingMu.Lock()
		pc.incoming = nil
		pc.incomingMu.Unlock()
	}
}

// SendViaIncoming sends an envelope through the peer's incoming bidi stream.
func (cm *ConnMgr) SendViaIncoming(pubKey string, envelope *chatpb.Envelope) bool {
	cm.mu.RLock()
	pc, exists := cm.peers[pubKey]
	cm.mu.RUnlock()
	if !exists {
		cm.logr.Debug("conn", fmt.Sprintf("SendViaIncoming: peer %s not in peers map", truncateKey(pubKey)))
		return false
	}
	pc.incomingMu.Lock()
	stream := pc.incoming
	pc.incomingMu.Unlock()
	if stream == nil {
		cm.logr.Debug("conn", fmt.Sprintf("SendViaIncoming: no incoming stream for %s", truncateKey(pubKey)))
		return false
	}
	if err := stream.Send(envelope); err != nil {
		pc.incomingMu.Lock()
		pc.incoming = nil
		pc.incomingMu.Unlock()
		cm.logr.Debug("conn", fmt.Sprintf("SendViaIncoming: stream.Send failed for %s: %v", truncateKey(pubKey), err))
		return false
	}
	cm.logr.Trace("conn", fmt.Sprintf("SendViaIncoming: sent type=%d to %s", envelope.Type, truncateKey(pubKey)))
	return true
}

// SendViaChannel sends through the persistent outgoing stream's channel.
func (cm *ConnMgr) SendViaChannel(pubKey string, envelope *chatpb.Envelope) bool {
	cm.mu.RLock()
	pc, exists := cm.peers[pubKey]
	cm.mu.RUnlock()
	if !exists || pc.sendCh == nil {
		cm.logr.Debug("conn", fmt.Sprintf("SendViaChannel: no channel for %s", truncateKey(pubKey)))
		return false
	}
	select {
	case pc.sendCh <- envelope:
		cm.logr.Trace("conn", fmt.Sprintf("SendViaChannel: sent type=%d to %s", envelope.Type, truncateKey(pubKey)))
		return true
	default:
		cm.logr.Warn("conn", fmt.Sprintf("SendViaChannel: sendCh full for %s", truncateKey(pubKey)))
		return false
	}
}

// CloseAll closes all peer connections.
func (cm *ConnMgr) CloseAll() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for _, pc := range cm.peers {
		pc.Close()
	}
	cm.peers = make(map[string]*PeerConn)
}
// Peers returns a snapshot of all discovered peers.
func (cm *ConnMgr) Peers() []PeerInfo {
	return cm.discovery.Snapshot()
}

func (cm *ConnMgr) isPeerOffline(pubKey string) bool {
	cm.mu.RLock()
	pc, inPeers := cm.peers[pubKey]
	cm.mu.RUnlock()
	if inPeers && pc != nil {
		return false
	}
	for _, s := range cm.discovery.ListWithStatus() {
		if s.PubKeyBase64 == pubKey {
			return s.Status != PeerStatusOnline
		}
	}
	return false
}

// GetPeer looks up a peer by public key.
func (cm *ConnMgr) GetPeer(pubKey string) (PeerInfo, bool) {
	return cm.discovery.GetPeerByPubKey(pubKey)
}

// UpsertPeer adds or updates a peer in the discovery table.
func (cm *ConnMgr) UpsertPeer(peer PeerInfo) {
	_, existed := cm.discovery.GetPeerByPubKey(peer.PubKeyBase64)
	cm.discovery.UpsertPeer(peer)
	if !existed && peer.PubKeyBase64 != "" && peer.PubKeyBase64 != cm.crypto.GetPublicKeyBase64() {
		_ = cm.discovery.SavePeers(PeersFile)
		cm.sendFullPeerList(peer.PubKeyBase64)
		cm.BroadcastPeerInfo(peer)
	}
}

func (cm *ConnMgr) sendFullPeerList(targetPubKey string) {
	allPeers := cm.discovery.Snapshot()
	myPub := cm.crypto.GetPublicKeyBase64()
	peerList := make([]PeerInfo, 0, len(allPeers))
	for _, p := range allPeers {
		if p.PubKeyBase64 != targetPubKey && p.PubKeyBase64 != myPub {
			peerList = append(peerList, p)
		}
	}
	if len(peerList) > 0 {
		payload, _ := json.Marshal(peerList)
		env := &chatpb.Envelope{
			Type: 8, SenderId: myPub, Payload: payload, TeamId: cm.crypto.GetActiveTeam(),
		}
		if !cm.SendViaIncoming(targetPubKey, env) {
			cm.SendViaChannel(targetPubKey, env)
		}
	}
}

// AddPeerTeam records that a peer belongs to a specific team hash.
func (cm *ConnMgr) AddPeerTeam(pubKey, teamHash string) {
	cm.discovery.AddPeerTeamHash(pubKey, teamHash)
}

// BrowseRemote browses a remote peer's shared directory via the envelope tunnel.
func (cm *ConnMgr) BrowseRemote(targetPubKey, path string) ([]*pb.BrowseEntry, error) {
	reqID := uuid.New().String()[:8]
	respCh := make(chan []byte, 1)

	cm.pendingReqsMu.Lock()
	cm.pendingReqs[reqID] = respCh
	cm.pendingReqsMu.Unlock()
	defer func() {
		cm.pendingReqsMu.Lock()
		delete(cm.pendingReqs, reqID)
		cm.pendingReqsMu.Unlock()
	}()

	envelope := &chatpb.Envelope{
		Type:       2,
		SenderId:   cm.crypto.GetPublicKeyBase64(),
		Payload:    []byte(path),
		ReceiverId: targetPubKey,
		TeamId:     reqID,
	}
	if !cm.SendViaIncoming(targetPubKey, envelope) && !cm.SendViaChannel(targetPubKey, envelope) {
		return nil, fmt.Errorf("failed to send browse request via persistent stream")
	}
	cm.logr.Debug("file", fmt.Sprintf("browse req %s sent to %s (path=%q)", reqID, truncateKey(targetPubKey), path))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("file browse request timed out")
	case data := <-respCh:
		cm.logr.Debug("file", fmt.Sprintf("browse resp %s received (%d bytes)", reqID, len(data)))
		if len(data) > 5 && string(data[:6]) == "ERROR:" {
			return nil, fmt.Errorf("remote browse error: %s", string(data[6:]))
		}
		var entries []*pb.BrowseEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, fmt.Errorf("failed to parse browse response: %w", err)
		}
		return entries, nil
	}
}

// DownloadFileViaTunnel downloads a file chunk from a remote peer via the envelope tunnel.
func (cm *ConnMgr) DownloadFileViaTunnel(targetPubKey, taskID, filePath string, offset int64) (*pb.FileChunk, error) {
	reqID := uuid.New().String()[:8]
	respCh := make(chan []byte, 1)

	cm.pendingReqsMu.Lock()
	cm.pendingReqs[reqID] = respCh
	cm.pendingReqsMu.Unlock()
	defer func() {
		cm.pendingReqsMu.Lock()
		delete(cm.pendingReqs, reqID)
		cm.pendingReqsMu.Unlock()
	}()

	reqData, _ := json.Marshal(map[string]interface{}{
		"task_id":   taskID,
		"file_path": filePath,
		"offset":    offset,
	})

	envelope := &chatpb.Envelope{
		Type:       6,
		SenderId:   cm.crypto.GetPublicKeyBase64(),
		Payload:    reqData,
		ReceiverId: targetPubKey,
		TeamId:     reqID,
	}
	if !cm.SendViaChannel(targetPubKey, envelope) && !cm.SendViaIncoming(targetPubKey, envelope) {
		return nil, fmt.Errorf("failed to send download request via persistent stream")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("file download request timed out")
	case data := <-respCh:
		var chunk pb.FileChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return nil, fmt.Errorf("failed to parse file chunk: %w", err)
		}
		return &chunk, nil
	}
}

// RefreshSeen updates the last-seen timestamp and heartbeat state for a peer.
func (cm *ConnMgr) RefreshSeen(pubKey string) {
	cm.discovery.RefreshLastSeen(pubKey)
	cm.discovery.MarkHeartbeat(pubKey)
}

func (cm *ConnMgr) removePeer(pubKey string) {
	cm.mu.Lock()
	pc, exists := cm.peers[pubKey]
	if exists {
		delete(cm.peers, pubKey)
	}
	cm.mu.Unlock()
	if exists {
		pc.Close()
	}
	// Keep peer in discovery for future reconnection; persist on explicit removal only
}

// BroadcastTeam sends a team message to all discovered peers.
// If no peers exist, decrypts and self-publishes to the event bus.
func (cm *ConnMgr) BroadcastTeam(myPubKey string, teamID string, payload []byte) error {
	peers := cm.discovery.Snapshot()
	if len(peers) == 0 {
		plain, err := cm.crypto.DecryptTeamForTeam(teamID, payload)
		if err != nil {
			return err
		}
		var msg eventbus.MsgReceivedPayload
		if err := json.Unmarshal(plain, &msg); err != nil {
			return err
		}
		msg.TeamID = teamID
		cm.bus.Publish(eventbus.TopicMsgReceived, msg)
		return nil
	}

	go func() {
		envelope := &chatpb.Envelope{
			SenderId:   myPubKey,
			Payload:    payload,
			Type:       0,
			ReceiverId: "",
			TeamId:     teamID,
		}

		for _, peer := range peers {
			if cm.isPeerOffline(peer.PubKeyBase64) {
				continue
			}
			go func(p PeerInfo) {
				if cm.SendViaIncoming(p.PubKeyBase64, envelope) {
					return
				}
				if cm.SendViaChannel(p.PubKeyBase64, envelope) {
					return
				}
				_, err := cm.GetOrDial(p.PubKeyBase64, p.IPAddress)
				if err != nil {
					cm.logr.Warn("grpc", fmt.Sprintf("failed to dial peer %s: %v", p.PubKeyBase64, err))
					return
				}
				cm.SendViaChannel(p.PubKeyBase64, envelope)
			}(peer)
		}
	}()

	return nil
}

// BroadcastChannel sends a channel message to all discovered peers.
func (cm *ConnMgr) BroadcastChannel(myPubKey string, teamID string, channelID string, payload []byte) error {
	peers := cm.discovery.Snapshot()
	if len(peers) == 0 {
		plain, err := cm.crypto.DecryptTeamForTeam(teamID, payload)
		if err != nil {
			return err
		}
		var msg eventbus.MsgReceivedPayload
		if err := json.Unmarshal(plain, &msg); err != nil {
			return err
		}
		msg.TeamID = teamID
		msg.ChannelID = channelID
		cm.bus.Publish(eventbus.TopicMsgReceived, msg)
		return nil
	}

	go func() {
		envelope := &chatpb.Envelope{
			SenderId:   myPubKey,
			Payload:    payload,
			Type:       0,
			ReceiverId: "",
			TeamId:     teamID,
			ChannelId:  channelID,
		}

		for _, peer := range peers {
			if cm.isPeerOffline(peer.PubKeyBase64) {
				continue
			}
			go func(p PeerInfo) {
				if cm.SendViaIncoming(p.PubKeyBase64, envelope) {
					return
				}
				if cm.SendViaChannel(p.PubKeyBase64, envelope) {
					return
				}
				_, err := cm.GetOrDial(p.PubKeyBase64, p.IPAddress)
				if err != nil {
					cm.logr.Warn("grpc", fmt.Sprintf("failed to dial peer %s: %v", p.PubKeyBase64, err))
					return
				}
				cm.SendViaChannel(p.PubKeyBase64, envelope)
			}(peer)
		}
	}()

	return nil
}

// UnicastPrivate sends a private message to a specific peer.
// The payload is already encrypted (team-wrapped) by the caller.
func (cm *ConnMgr) UnicastPrivate(myPubKey string, targetPubKey string, teamID string, payload []byte) error {
	if targetPubKey == "" {
		return fmt.Errorf("target pubkey is required")
	}

	go func() {
		envelope := &chatpb.Envelope{
			SenderId:   myPubKey,
			Payload:    payload,
			Type:       1,
			ReceiverId: targetPubKey,
			TeamId:     teamID,
		}

		if cm.SendViaIncoming(targetPubKey, envelope) {
			cm.logr.Debug("chat", "private message sent via reverse stream to "+truncateKey(targetPubKey))
			return
		}

		peer, ok := cm.discovery.GetPeerByPubKey(targetPubKey)
		if !ok {
			cm.logr.Warn("chat", "peer not found for private message: "+truncateKey(targetPubKey))
			return
		}

		_, err := cm.GetOrDial(peer.PubKeyBase64, peer.IPAddress)
		if err != nil {
			cm.logr.Warn("grpc", fmt.Sprintf("failed to dial peer %s: %v", peer.PubKeyBase64, err))
			return
		}

		if cm.SendViaChannel(targetPubKey, envelope) {
			cm.logr.Debug("chat", "private message sent via persistent stream to "+truncateKey(targetPubKey))
		}
	}()

	return nil
}

// Conn returns the underlying gRPC connection, or nil.
func (pc *PeerConn) Conn() *grpc.ClientConn {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.conn
}

// Close cleans up the peer connection.
func (pc *PeerConn) Close() {
	if pc.cancel != nil {
		pc.cancel()
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.conn != nil {
		pc.conn.Close()
		pc.conn = nil
	}
}

func (pc *PeerConn) startPersistentStream(cm *ConnMgr) error {
	client := chatpb.NewChatServiceClient(pc.conn)
	stream, err := client.StreamChat(pc.ctx)
	if err != nil {
		return err
	}
	pc.ctrlStream = stream

	// Send goroutine
	go func() {
		defer func() {
			stream.CloseSend()
			cm.removePeer(pc.pubKey)
		}()
		for {
			select {
			case <-pc.ctx.Done():
				return
			case envelope, ok := <-pc.sendCh:
				if !ok {
					return
				}
				if err := stream.Send(envelope); err != nil {
					return
				}
			}
		}
	}()

	// Heartbeat goroutine
	go func() {
		myPubKey := cm.crypto.GetPublicKeyBase64()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pc.ctx.Done():
				return
			case <-ticker.C:
				pc.heartbeatMu.Lock()
				lastAt := pc.lastHeartbeatAt
				pc.heartbeatMu.Unlock()
				if time.Since(lastAt) > 45*time.Second {
					cm.logr.Warn("heartbeat", fmt.Sprintf("peer %s timed out (last seen %v ago)", truncateKey(pc.pubKey), time.Since(lastAt)))
					pubKey := pc.pubKey
					cm.removePeer(pubKey)
					time.AfterFunc(30*time.Second, func() {
						if peer, ok := cm.discovery.GetPeerByPubKey(pubKey); ok && peer.IPAddress != "" {
							cm.GetOrDial(pubKey, peer.IPAddress)
						}
					})
					return
				}
				pc.heartbeatMu.Lock()
				seq := pc.heartbeatSeq
				pc.heartbeatSeq++
				pc.heartbeatMu.Unlock()
				ping := map[string]interface{}{"seq": seq, "ack": false}
				pingBytes, _ := json.Marshal(ping)
				select {
				case pc.sendCh <- &chatpb.Envelope{SenderId: myPubKey, Payload: pingBytes, Type: 9}:
				default:
				}
			}
		}
	}()

	// Recv goroutine — processes incoming messages (decrypt + publish)
	go func() {
		for {
			envelope, err := stream.Recv()
			if err != nil {
				if err != io.EOF {
					cm.logr.Debug("chat",
						fmt.Sprintf("persistent stream recv from %s: %v", truncateKey(pc.pubKey), err))
				}
				return
			}
			if envelope != nil {
				cm.logr.Debug("file", fmt.Sprintf("raw recv: type=%d sender=%s teamid=%s payload=%d",
					envelope.Type, truncateKey(envelope.SenderId), envelope.TeamId, len(envelope.Payload)))
			}
			if envelope == nil {
				continue
			}
			if envelope.Type == 9 {
				var hb struct {
					Seq uint64 `json:"seq"`
					Ack bool   `json:"ack"`
				}
				if err := json.Unmarshal(envelope.Payload, &hb); err != nil {
					continue
				}
				myPubKey := cm.crypto.GetPublicKeyBase64()
				if hb.Ack {
					pc.heartbeatMu.Lock()
					pc.lastHeartbeatAt = time.Now()
					pc.heartbeatMu.Unlock()
				} else {
					pong := map[string]interface{}{"seq": hb.Seq, "ack": true}
					pongBytes, _ := json.Marshal(pong)
					pc.heartbeatMu.Lock()
					pc.lastHeartbeatAt = time.Now()
					pc.heartbeatMu.Unlock()
					select {
					case pc.sendCh <- &chatpb.Envelope{SenderId: myPubKey, Payload: pongBytes, Type: 9}:
					default:
					}
				}
				continue
			}
			if envelope.SenderId == "" {
				continue // ACK or empty envelope
			}
			if envelope.Type == 3 {
				cm.pendingReqsMu.Lock()
				ch, ok := cm.pendingReqs[envelope.TeamId]
				cm.pendingReqsMu.Unlock()
				if ok {
					select {
					case ch <- envelope.Payload:
						cm.logr.Debug("file", fmt.Sprintf("browse resp %s dispatched via recv", envelope.TeamId))
					default:
						cm.logr.Warn("file", fmt.Sprintf("browse resp %s channel full", envelope.TeamId))
					}
				} else {
					cm.logr.Warn("file", fmt.Sprintf("browse resp %s: no pending request", envelope.TeamId))
				}
				continue
			}
			cm.processReceivedEnvelope(envelope)
		}
	}()

	return nil
}

// processReceivedEnvelope decrypts and publishes a received message envelope.
// Shared between StreamChat server handler and persistent stream recv goroutine.
func (cm *ConnMgr) processReceivedEnvelope(envelope *chatpb.Envelope) {
	if envelope.Type == 9 {
		var hb struct {
			Seq uint64 `json:"seq"`
			Ack bool   `json:"ack"`
		}
		if len(envelope.Payload) > 0 {
			if err := json.Unmarshal(envelope.Payload, &hb); err == nil {
				if hb.Ack {
					cm.RefreshSeen(envelope.SenderId)
				} else {
					cm.RefreshSeen(envelope.SenderId)
					pong := map[string]interface{}{"seq": hb.Seq, "ack": true}
					pongBytes, _ := json.Marshal(pong)
					if !cm.SendViaIncoming(envelope.SenderId, &chatpb.Envelope{
						Type:       9,
						SenderId:   cm.crypto.GetPublicKeyBase64(),
						Payload:    pongBytes,
						ReceiverId: envelope.SenderId,
					}) && !cm.SendViaChannel(envelope.SenderId, &chatpb.Envelope{
						Type:       9,
						SenderId:   cm.crypto.GetPublicKeyBase64(),
						Payload:    pongBytes,
						ReceiverId: envelope.SenderId,
					}) {
					}
				}
			}
		}
		return
	}
	if envelope.SenderId == "" {
		return
	}
	if envelope.Type == 0 && len(envelope.Payload) == 0 {
		return
	}


	// Type 12: Conversation sync request → publish on EventBus for app layer
	if envelope.Type == 12 {
		var req struct {
			ConvID   string `json:"conv_id"`
			SinceHLC string `json:"since_hlc"`
		}
		if err := json.Unmarshal(envelope.Payload, &req); err != nil {
			return
		}
		cm.bus.Publish(eventbus.TopicConvSyncRequest, eventbus.ConversationSyncPayload{
			RequesterPubKey: envelope.SenderId,
			ConversationID:  req.ConvID,
			SinceHLC:        req.SinceHLC,
		})
		return
	}

	// Type 13: Conversation sync response → batch insert messages
	if envelope.Type == 13 {
		var resp struct {
			ConvID   string          `json:"conv_id"`
			Messages json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal(envelope.Payload, &resp); err != nil {
			return
		}
		cm.bus.Publish(eventbus.TopicConvSyncRequest, eventbus.ConversationSyncPayload{
			RequesterPubKey: envelope.SenderId,
			ConversationID:  resp.ConvID,
			Messages:        []byte(resp.Messages),
		})
		return
	}

	// Type 8: Peer info exchange — update discovery with received peer list
	if envelope.Type == 8 {
		var peerList []PeerInfo
		if err := json.Unmarshal(envelope.Payload, &peerList); err == nil && len(peerList) > 0 {
			for _, p := range peerList {
				if p.PubKeyBase64 != "" && p.PubKeyBase64 != cm.crypto.GetPublicKeyBase64() {
					cm.UpsertPeer(p)
				}
			}
			cm.RefreshSeen(envelope.SenderId)
			return
		}

		// Backward compat: single peer format {pub_key, ips}
		var info struct {
			PubKey string   `json:"pub_key"`
			IPs    []string `json:"ips"`
		}
		if err := json.Unmarshal(envelope.Payload, &info); err != nil {
			cm.logr.Warn("network", fmt.Sprintf("failed to parse peer info from %s: %v", truncateKey(envelope.SenderId), err))
			return
		}
		for i, ip := range info.IPs {
			if i == 0 {
				cm.UpsertPeer(PeerInfo{
					PubKeyBase64: envelope.SenderId,
					IPAddress:    ip,
				})
			}
			cm.AddPeerTeam(envelope.SenderId, cm.crypto.TeamKeyHash())
		}
		cm.RefreshSeen(envelope.SenderId)
		return
	}

	// Type 2: File browse request (tunneled through persistent stream)
	if envelope.Type == 2 {
		path := string(envelope.Payload)
		cm.logr.Debug("file", fmt.Sprintf("browse req received from %s (path=%q, reqID=%s)", truncateKey(envelope.SenderId), path, envelope.TeamId))
		if cm.fileEngine == nil {
			cm.logr.Warn("file", "browse request received but file engine not attached")
			responseData := []byte("ERROR:file engine not available")
			if !cm.SendViaIncoming(envelope.SenderId, &chatpb.Envelope{
				Type:       3,
				SenderId:   cm.crypto.GetPublicKeyBase64(),
				Payload:    responseData,
				ReceiverId: envelope.SenderId,
				TeamId:     envelope.TeamId,
			}) && !cm.SendViaChannel(envelope.SenderId, &chatpb.Envelope{
				Type:       3,
				SenderId:   cm.crypto.GetPublicKeyBase64(),
				Payload:    responseData,
				ReceiverId: envelope.SenderId,
				TeamId:     envelope.TeamId,
			}) {
				cm.logr.Warn("file", "failed to send file browse error response")
			}
			return
		}
		if path == "" {
			roots := cm.fileEngine.GetSharedRoots()
			entries := make([]*pb.BrowseEntry, 0, len(roots))
			for _, root := range roots {
				info, err := os.Stat(root)
				if err != nil {
					continue
				}
				entries = append(entries, &pb.BrowseEntry{
					Name:        filepath.Base(root),
					Path:        root,
					IsDirectory: true,
					Size:        info.Size(),
				})
			}
		responseData, _ := json.Marshal(entries)
		cm.logr.Debug("file", fmt.Sprintf("browse resp %s: %d entries, sending to %s", envelope.TeamId, len(entries), truncateKey(envelope.SenderId)))
		if !cm.SendViaIncoming(envelope.SenderId, &chatpb.Envelope{
			Type:       3,
			SenderId:   cm.crypto.GetPublicKeyBase64(),
			Payload:    responseData,
			ReceiverId: envelope.SenderId,
			TeamId:     envelope.TeamId,
		}) && !cm.SendViaChannel(envelope.SenderId, &chatpb.Envelope{
			Type:       3,
			SenderId:   cm.crypto.GetPublicKeyBase64(),
			Payload:    responseData,
			ReceiverId: envelope.SenderId,
			TeamId:     envelope.TeamId,
		}) {
			cm.logr.Warn("file", "failed to send file browse response via any path")
		} else {
			cm.logr.Debug("file", fmt.Sprintf("browse resp %s sent via dual-path", envelope.TeamId))
		}
		return
		}

		absPath := filepath.Clean(path)
		sharedRoots := cm.fileEngine.GetSharedRoots()
		allowed := false
		for _, root := range sharedRoots {
			if strings.HasPrefix(absPath, root+string(os.PathSeparator)) || absPath == root {
				allowed = true
				break
			}
		}

		var responseData []byte
		if !allowed {
			responseData = []byte("ERROR:path not in shared directories")
		} else {
			entries, err := os.ReadDir(absPath)
			if err != nil {
				responseData = []byte("ERROR:" + err.Error())
			} else {
				var browseEntries []*pb.BrowseEntry
				for _, entry := range entries {
					info, err := entry.Info()
					if err != nil {
						continue
					}
					browseEntries = append(browseEntries, &pb.BrowseEntry{
						Name:        entry.Name(),
						Path:        filepath.Join(absPath, entry.Name()),
						IsDirectory: entry.IsDir(),
						Size:        info.Size(),
					})
				}
		responseData, _ = json.Marshal(browseEntries)
			}
		}
		if !cm.SendViaIncoming(envelope.SenderId, &chatpb.Envelope{
			Type:       3,
			SenderId:   cm.crypto.GetPublicKeyBase64(),
			Payload:    responseData,
			ReceiverId: envelope.SenderId,
			TeamId:     envelope.TeamId,
		}) && !cm.SendViaChannel(envelope.SenderId, &chatpb.Envelope{
			Type:       3,
			SenderId:   cm.crypto.GetPublicKeyBase64(),
			Payload:    responseData,
			ReceiverId: envelope.SenderId,
			TeamId:     envelope.TeamId,
		}) {
			cm.logr.Warn("file", "failed to send file browse response via any path")
		} else {
			cm.logr.Debug("file", fmt.Sprintf("browse resp %s sent via dual-path", envelope.TeamId))
		}
		return
	}

	if envelope.Type == 3 {
		cm.pendingReqsMu.Lock()
		ch, ok := cm.pendingReqs[envelope.TeamId]
		cm.pendingReqsMu.Unlock()
		if ok {
			select {
			case ch <- envelope.Payload:
				cm.logr.Debug("file", fmt.Sprintf("browse resp %s dispatched via StreamChat", envelope.TeamId))
			default:
				cm.logr.Warn("file", fmt.Sprintf("browse resp %s channel full (sc)", envelope.TeamId))
			}
		} else {
			cm.logr.Warn("file", fmt.Sprintf("browse resp %s: no pending request (sc)", envelope.TeamId))
		}
		return
	}

	var plain []byte
	switch envelope.Type {
	case 1: // Private: DecryptTeam(outer) -> DecryptPrivate(inner)
		teamPlain, err := cm.crypto.DecryptTeam(envelope.Payload)
		if err != nil {
			cm.logr.Warn("chat", fmt.Sprintf("private message DecryptTeam failed: %v", err))
			return
		}
		plain, err = cm.crypto.DecryptPrivate(teamPlain, envelope.SenderId)
		if err != nil {
			cm.logr.Warn("chat", fmt.Sprintf("private message DecryptPrivate failed: %v", err))
			return
		}
	default: // Type 0 (team) or 99 (test): DecryptTeam only
		var err error
		if envelope.TeamId != "" {
			plain, err = cm.crypto.DecryptTeamForTeam(envelope.TeamId, envelope.Payload)
		} else {
			plain, err = cm.crypto.DecryptTeam(envelope.Payload)
		}
		if err != nil {
			cm.logr.Warn("chat", fmt.Sprintf("DecryptTeam failed: %v", err))
			return
		}
	}

	var msg eventbus.MsgReceivedPayload
	if err := json.Unmarshal(plain, &msg); err != nil {
		cm.logr.Warn("chat", fmt.Sprintf("unmarshal error: %v", err))
		return
	}

	cm.logr.Trace("chat", fmt.Sprintf("processReceived: type=%d from=%s hlc=%s conv=%s", envelope.Type, truncateKey(envelope.SenderId), msg.HLC, msg.ConversationID))

	msg.TeamID = envelope.TeamId
	msg.ChannelID = envelope.ChannelId

	// Flood dedup: if we've already processed this message (via direct + forwarded paths), skip.
	if envelope.Type != 1 && cm.isHLCSeen(msg.HLC) {
		return
	}
	if envelope.Type != 1 {
		cm.markHLCSeen(msg.HLC)
	}

	if envelope.Type == 1 {
		msg.ReceiverID = envelope.ReceiverId
		cm.bus.Publish(eventbus.TopicPrivateMsgReceived, msg)
	} else {
		cm.bus.Publish(eventbus.TopicMsgReceived, msg)
		cm.floodTeamMessage(envelope, msg.HLC)
	}
	cm.RefreshSeen(envelope.SenderId)
}

// isHLCSeen checks whether an HLC has been recently forwarded (flood dedup).
func (cm *ConnMgr) isHLCSeen(hlc string) bool {
	cm.recentHLCsMu.Lock()
	defer cm.recentHLCsMu.Unlock()
	_, ok := cm.recentHLCs[hlc]
	return ok
}

// markHLCSeen records an HLC for flood dedup. Periodically cleans old entries.
func (cm *ConnMgr) markHLCSeen(hlc string) {
	cm.recentHLCsMu.Lock()
	defer cm.recentHLCsMu.Unlock()

	cm.recentHLCs[hlc] = time.Now()
	if len(cm.recentHLCs) > 2000 {
		cutoff := time.Now().Add(-5 * time.Minute)
		for h, t := range cm.recentHLCs {
			if t.Before(cutoff) {
				delete(cm.recentHLCs, h)
			}
		}
	}
}

// floodTeamMessage forwards a received team message to all other peers,
// excluding the original sender. Uses HLC dedup to prevent loops.
func (cm *ConnMgr) floodTeamMessage(envelope *chatpb.Envelope, hlc string) {
	if cm.isHLCSeen(hlc) {
		return
	}
	cm.markHLCSeen(hlc)
	cm.logr.Trace("flood", fmt.Sprintf("floodTeamMessage: type=%d from=%s hlc=%s to %d peers", envelope.Type, truncateKey(envelope.SenderId), hlc, len(cm.peers)))

	cm.mu.RLock()
	peers := make([]*PeerConn, 0, len(cm.peers))
	for _, pc := range cm.peers {
		peers = append(peers, pc)
	}
	cm.mu.RUnlock()

	if len(peers) <= 1 {
		return
	}

	myPub := cm.crypto.GetPublicKeyBase64()
	for _, pc := range peers {
		if pc.pubKey == envelope.SenderId || pc.pubKey == myPub {
			continue
		}
		if !cm.SendViaIncoming(pc.pubKey, envelope) && !cm.SendViaChannel(pc.pubKey, envelope) {
			cm.logr.Debug("flood", fmt.Sprintf("failed to forward to %s", truncateKey(pc.pubKey)))
		}
	}
}

// SendConvSyncRequest sends a per-conversation sync request (Type 12) to a peer.
func (cm *ConnMgr) SendConvSyncRequest(targetPubKey, convID, sinceHLC string) {
	payload, _ := json.Marshal(map[string]string{"conv_id": convID, "since_hlc": sinceHLC})
	envelope := &chatpb.Envelope{
		Type:     12,
		SenderId: cm.crypto.GetPublicKeyBase64(),
		Payload:  payload,
		TeamId:   cm.crypto.GetActiveTeam(),
	}
	cm.logr.Debug("sync", fmt.Sprintf("SyncRequest type=12 to %s conv=%s since=%s", truncateKey(targetPubKey), convID[:8], sinceHLC))
	if !cm.SendViaIncoming(targetPubKey, envelope) && !cm.SendViaChannel(targetPubKey, envelope) {
		peer, ok := cm.discovery.GetPeerByPubKey(targetPubKey)
		if !ok {
			cm.logr.Warn("sync", fmt.Sprintf("SyncRequest to %s failed: peer not in discovery", truncateKey(targetPubKey)))
			return
		}
		cm.logr.Debug("sync", fmt.Sprintf("SyncRequest dialing %s for conv=%s", truncateKey(targetPubKey), convID[:8]))
		if _, err := cm.GetOrDial(peer.PubKeyBase64, peer.IPAddress); err != nil {
			cm.logr.Warn("sync", fmt.Sprintf("SyncRequest to %s failed: dial error %v", truncateKey(targetPubKey), err))
			return
		}
		cm.SendViaChannel(targetPubKey, envelope)
	}
}

// SendConvSyncResponse sends a per-conversation sync response (Type 13) with messages.
func (cm *ConnMgr) SendConvSyncResponse(targetPubKey, convID string, messagesJSON []byte) {
	payload, _ := json.Marshal(map[string]interface{}{
		"conv_id":  convID,
		"messages": json.RawMessage(messagesJSON),
	})
	envelope := &chatpb.Envelope{
		Type:     13,
		SenderId: cm.crypto.GetPublicKeyBase64(),
		Payload:  payload,
		TeamId:   cm.crypto.GetActiveTeam(),
	}
	cm.logr.Debug("sync", fmt.Sprintf("SyncResponse type=13 to %s conv=%s (%d bytes)", truncateKey(targetPubKey), convID[:8], len(messagesJSON)))
	if !cm.SendViaIncoming(targetPubKey, envelope) && !cm.SendViaChannel(targetPubKey, envelope) {
		peer, ok := cm.discovery.GetPeerByPubKey(targetPubKey)
		if !ok {
			cm.logr.Warn("sync", fmt.Sprintf("SyncResponse to %s failed: peer not in discovery", truncateKey(targetPubKey)))
			return
		}
		cm.logr.Debug("sync", fmt.Sprintf("SyncResponse dialing %s for conv=%s", truncateKey(targetPubKey), convID[:8]))
		if _, err := cm.GetOrDial(peer.PubKeyBase64, peer.IPAddress); err != nil {
			cm.logr.Warn("sync", fmt.Sprintf("SyncResponse to %s failed: dial error %v", truncateKey(targetPubKey), err))
			return
		}
		cm.SendViaChannel(targetPubKey, envelope)
	}
}

// BroadcastPeerInfo sends peer information to all connected peers.
// When a new peer joins, this propagates their info and sends the full peer list back.
func (cm *ConnMgr) BroadcastPeerInfo(newPeer PeerInfo) {
	allPeers := cm.discovery.Snapshot()

	myPub := cm.crypto.GetPublicKeyBase64()

	// Notify the new peer about all existing peers (excluding newPeer itself)
	if newPeer.PubKeyBase64 != "" && newPeer.PubKeyBase64 != myPub {
		peerList := make([]PeerInfo, 0, len(allPeers))
		for _, p := range allPeers {
			if p.PubKeyBase64 != newPeer.PubKeyBase64 && p.PubKeyBase64 != myPub {
				peerList = append(peerList, p)
			}
		}
		if len(peerList) > 0 {
			payload, _ := json.Marshal(peerList)
			envelope := &chatpb.Envelope{
				Type:     8,
				SenderId: myPub,
				Payload:  payload,
				TeamId:   cm.crypto.GetActiveTeam(),
			}
			cm.SendViaIncoming(newPeer.PubKeyBase64, envelope)
			cm.SendViaChannel(newPeer.PubKeyBase64, envelope)
		}
	}

	// Notify all existing peers about the new peer
	if newPeer.PubKeyBase64 != "" && newPeer.PubKeyBase64 != myPub {
		payload, _ := json.Marshal([]PeerInfo{newPeer})
		envelope := &chatpb.Envelope{
			Type:     8,
			SenderId: myPub,
			Payload:  payload,
			TeamId:   cm.crypto.GetActiveTeam(),
		}
		for _, p := range allPeers {
			if p.PubKeyBase64 == newPeer.PubKeyBase64 || p.PubKeyBase64 == myPub {
				continue
			}
			cm.SendViaIncoming(p.PubKeyBase64, envelope)
			cm.SendViaChannel(p.PubKeyBase64, envelope)
		}
	}
}
