package network

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
}

// ConnMgr manages all peer connections.
type ConnMgr struct {
	peers map[string]*PeerConn
	mu    sync.RWMutex

	engine      *Engine
	controlPort int

	// Pending file browse requests awaiting response
	pendingReqs   map[string]chan []byte
	pendingReqsMu sync.Mutex

	// Discovery/mDNS
	discovery       *Discovery
	mdnsHandle      ServiceHandle
	discoveryCtx    context.Context
	discoveryCancel context.CancelFunc
	discoveryWG     sync.WaitGroup
}

func NewConnMgr(eng *Engine, controlPort int) *ConnMgr {
	return &ConnMgr{
		peers:       make(map[string]*PeerConn),
		pendingReqs: make(map[string]chan []byte),
		engine:      eng,
		controlPort: controlPort,
	}
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
		pc.Close()
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	pc, exists = cm.peers[pubKey]
	if exists && pc != nil {
		pc.mu.Lock()
		if pc.conn != nil && pc.conn.GetState() != connectivity.Shutdown {
			pc.mu.Unlock()
			return pc, nil
		}
		pc.mu.Unlock()
		pc.Close()
	}

	target := fmt.Sprintf("%s:%d", ipAddr, cm.controlPort)
	conn, err := grpc.Dial(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:    15 * time.Second,
			Timeout: 5 * time.Second,
		}),
	)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	pc = &PeerConn{
		pubKey: pubKey,
		ipAddr: ipAddr,
		conn:   conn,
		sendCh: make(chan *chatpb.Envelope, 64),
		ctx:    ctx,
		cancel: cancel,
		logr:   cm.engine.logr,
	}

	if err := pc.startPersistentStream(cm.engine); err != nil {
		cancel()
		conn.Close()
		return nil, err
	}

	cm.peers[pubKey] = pc
	return pc, nil
}

// RegisterIncoming registers a server-side incoming stream for reverse sends.
func (cm *ConnMgr) RegisterIncoming(pubKey string, stream chatpb.ChatService_StreamChatServer) {
	cm.mu.RLock()
	pc, exists := cm.peers[pubKey]
	cm.mu.RUnlock()
	if !exists {
		cm.mu.Lock()
		if existing, ok := cm.peers[pubKey]; ok {
			pc = existing
		} else {
			pc = &PeerConn{
				pubKey: pubKey,
				sendCh: make(chan *chatpb.Envelope, 64),
				logr:   cm.engine.logr,
			}
			cm.peers[pubKey] = pc
		}
		cm.mu.Unlock()
	}
	pc.incomingMu.Lock()
	pc.incoming = stream
	pc.incomingMu.Unlock()
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
		return false
	}
	pc.incomingMu.Lock()
	stream := pc.incoming
	pc.incomingMu.Unlock()
	if stream == nil {
		return false
	}
	if err := stream.Send(envelope); err != nil {
		pc.incomingMu.Lock()
		pc.incoming = nil
		pc.incomingMu.Unlock()
		return false
	}
	return true
}

// SendViaChannel sends through the persistent outgoing stream's channel.
func (cm *ConnMgr) SendViaChannel(pubKey string, envelope *chatpb.Envelope) bool {
	cm.mu.RLock()
	pc, exists := cm.peers[pubKey]
	cm.mu.RUnlock()
	if !exists || pc.sendCh == nil {
		return false
	}
	select {
	case pc.sendCh <- envelope:
		return true
	default:
		return false
	}
}

// CloseAll closes all peer connections.
func (cm *ConnMgr) CloseAll() {
	cm.StopDiscovery()
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for _, pc := range cm.peers {
		pc.Close()
	}
	cm.peers = make(map[string]*PeerConn)
}

// StartDiscovery initializes mDNS discovery.
func (cm *ConnMgr) StartDiscovery(pubKey string, teamHashes []string, iface *net.Interface, lanIPs []net.IP, controlPort int) error {
	info := []string{
		fmt.Sprintf("pubkey=%s", pubKey),
	}
	teamHashStr := strings.Join(teamHashes, ",")
	if teamHashStr != "" {
		info = append(info, fmt.Sprintf("team=%s", teamHashStr))
	}

	browser := NewServiceBrowser()
	cm.discovery = NewDiscovery(cm.engine.bus, browser)
	cm.discovery.logr = cm.engine.logr

	handle, err := browser.Register(
		"Parade-"+pubKey[:8],
		"_parade._tcp",
		"local.",
		controlPort,
		lanIPs,
		info,
		iface,
	)
	if err != nil {
		return fmt.Errorf("failed to register mDNS service: %w", err)
	}
	cm.mdnsHandle = handle
	cm.engine.log(logger.Info, "discovery", fmt.Sprintf("mDNS started, advertising %s on %v (team=%s)",
		"_parade._tcp", lanIPs, truncateKey(teamHashStr)))

	cm.discovery.SetLocalPubKey(pubKey)
	cm.discovery.SetTeamHashes(teamHashes)
	if iface != nil {
		cm.discovery.SetIface(iface)
	}
	cm.discovery.SetOnPeerDiscovered(func(peer PeerInfo) {
		go func() {
			if _, err := cm.GetOrDial(peer.PubKeyBase64, peer.IPAddress); err != nil {
				cm.engine.log(logger.Warning, "grpc", fmt.Sprintf("mDNS auto-connect to peer %s failed: %v", truncateKey(peer.PubKeyBase64), err))
			} else {
				cm.engine.log(logger.Info, "network", fmt.Sprintf("mDNS auto-connected to peer %s @ %s", truncateKey(peer.PubKeyBase64), peer.IPAddress))
			}
		}()
	})
	cm.discoveryCtx, cm.discoveryCancel = context.WithCancel(context.Background())
	cm.discoveryWG.Add(1)
	go func() {
		defer cm.discoveryWG.Done()
		cm.discovery.Run(cm.discoveryCtx)
	}()

	return nil
}

// StopDiscovery tears down mDNS discovery.
func (cm *ConnMgr) StopDiscovery() {
	if cm.discoveryCancel != nil {
		cm.discoveryCancel()
	}
	if cm.mdnsHandle != nil {
		cm.mdnsHandle.Shutdown()
	}
	cm.discoveryWG.Wait()
}

// TriggerDiscovery triggers an immediate mDNS query.
func (cm *ConnMgr) TriggerDiscovery() {
	if cm.discovery != nil {
		cm.discovery.TriggerQuery()
	}
}

// Peers returns a snapshot of all discovered peers.
func (cm *ConnMgr) Peers() []PeerInfo {
	if cm.discovery == nil {
		return nil
	}
	return cm.discovery.Snapshot()
}

// GetPeer looks up a peer by public key.
func (cm *ConnMgr) GetPeer(pubKey string) (PeerInfo, bool) {
	if cm.discovery == nil {
		return PeerInfo{}, false
	}
	return cm.discovery.GetPeerByPubKey(pubKey)
}

// UpsertPeer adds or updates a peer in the discovery table.
func (cm *ConnMgr) UpsertPeer(peer PeerInfo) {
	if cm.discovery == nil {
		return
	}
	cm.discovery.UpsertPeer(peer)
}

// AddPeerTeam records that a peer belongs to a specific team hash.
func (cm *ConnMgr) AddPeerTeam(pubKey, teamHash string) {
	if cm.discovery == nil {
		return
	}
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
		SenderId:   cm.engine.crypto.GetPublicKeyBase64(),
		Payload:    []byte(path),
		ReceiverId: targetPubKey,
		TeamId:     reqID,
	}
	if !cm.SendViaIncoming(targetPubKey, envelope) && !cm.SendViaChannel(targetPubKey, envelope) {
		return nil, fmt.Errorf("failed to send browse request via persistent stream")
	}
	cm.engine.log(logger.Debug, "file", fmt.Sprintf("browse req %s sent to %s (path=%q)", reqID, truncateKey(targetPubKey), path))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("file browse request timed out")
	case data := <-respCh:
		cm.engine.log(logger.Debug, "file", fmt.Sprintf("browse resp %s received (%d bytes)", reqID, len(data)))
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
		SenderId:   cm.engine.crypto.GetPublicKeyBase64(),
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

// RefreshSeen updates the last-seen timestamp for a peer.
func (cm *ConnMgr) RefreshSeen(pubKey string) {
	if cm.discovery == nil {
		return
	}
	cm.discovery.RefreshLastSeen(pubKey)
}

func (cm *ConnMgr) removePeer(pubKey string) {
	cm.mu.Lock()
	delete(cm.peers, pubKey)
	cm.mu.Unlock()
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

func (pc *PeerConn) startPersistentStream(eng *Engine) error {
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
			eng.connMgr.removePeer(pc.pubKey)
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

	// Recv goroutine — processes incoming messages (decrypt + publish)
	go func() {
		defer pc.cancel()
		for {
			envelope, err := stream.Recv()
			if err != nil {
				if err != io.EOF {
					eng.log(logger.Debug, "chat",
						fmt.Sprintf("persistent stream recv from %s: %v", truncateKey(pc.pubKey), err))
				}
				return
			}
			if envelope != nil {
				eng.log(logger.Debug, "file", fmt.Sprintf("raw recv: type=%d sender=%s teamid=%s payload=%d",
					envelope.Type, truncateKey(envelope.SenderId), envelope.TeamId, len(envelope.Payload)))
			}
			if envelope == nil {
				continue
			}
			if envelope.SenderId == "" {
				continue // ACK or empty envelope
			}
			if envelope.Type == 3 {
				eng.connMgr.pendingReqsMu.Lock()
				ch, ok := eng.connMgr.pendingReqs[envelope.TeamId]
				eng.connMgr.pendingReqsMu.Unlock()
				if ok {
					select {
					case ch <- envelope.Payload:
						eng.log(logger.Debug, "file", fmt.Sprintf("browse resp %s dispatched via recv", envelope.TeamId))
					default:
						eng.log(logger.Warning, "file", fmt.Sprintf("browse resp %s channel full", envelope.TeamId))
					}
				} else {
					eng.log(logger.Warning, "file", fmt.Sprintf("browse resp %s: no pending request", envelope.TeamId))
				}
				continue
			}
			processReceivedEnvelope(eng, envelope)
		}
	}()

	return nil
}

// processReceivedEnvelope decrypts and publishes a received message envelope.
// Shared between StreamChat server handler and persistent stream recv goroutine.
func processReceivedEnvelope(e *Engine, envelope *chatpb.Envelope) {
	if envelope.SenderId == "" {
		return
	}
	if envelope.Type == 0 && len(envelope.Payload) == 0 {
		return
	}

	// Type 8: Peer info exchange — update discovery with sender's reachable IPs
	if envelope.Type == 8 {
		var info struct {
			PubKey string   `json:"pub_key"`
			IPs    []string `json:"ips"`
		}
		if err := json.Unmarshal(envelope.Payload, &info); err != nil {
			e.log(logger.Warning, "network", fmt.Sprintf("failed to parse peer info from %s: %v", truncateKey(envelope.SenderId), err))
			return
		}
		for i, ip := range info.IPs {
			if i == 0 {
				e.connMgr.UpsertPeer(PeerInfo{
					PubKeyBase64: envelope.SenderId,
					IPAddress:    ip,
				})
			}
			e.connMgr.AddPeerTeam(envelope.SenderId, e.crypto.TeamKeyHash())
		}
		e.connMgr.RefreshSeen(envelope.SenderId)
		return
	}

	// Type 2: File browse request (tunneled through persistent stream)
	if envelope.Type == 2 {
		path := string(envelope.Payload)
		e.log(logger.Debug, "file", fmt.Sprintf("browse req received from %s (path=%q, reqID=%s)", truncateKey(envelope.SenderId), path, envelope.TeamId))
		if path == "" {
			roots := e.fileEngine.GetSharedRoots()
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
		e.log(logger.Debug, "file", fmt.Sprintf("browse resp %s: %d entries, sending to %s", envelope.TeamId, len(entries), truncateKey(envelope.SenderId)))
		if !e.connMgr.SendViaIncoming(envelope.SenderId, &chatpb.Envelope{
			Type:       3,
			SenderId:   e.crypto.GetPublicKeyBase64(),
			Payload:    responseData,
			ReceiverId: envelope.SenderId,
			TeamId:     envelope.TeamId,
		}) && !e.connMgr.SendViaChannel(envelope.SenderId, &chatpb.Envelope{
			Type:       3,
			SenderId:   e.crypto.GetPublicKeyBase64(),
			Payload:    responseData,
			ReceiverId: envelope.SenderId,
			TeamId:     envelope.TeamId,
		}) {
			e.log(logger.Warning, "file", "failed to send file browse response via any path")
		} else {
			e.log(logger.Debug, "file", fmt.Sprintf("browse resp %s sent via dual-path", envelope.TeamId))
		}
		return
		}

		absPath := filepath.Clean(path)
		sharedRoots := e.fileEngine.GetSharedRoots()
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
		if !e.connMgr.SendViaIncoming(envelope.SenderId, &chatpb.Envelope{
			Type:       3,
			SenderId:   e.crypto.GetPublicKeyBase64(),
			Payload:    responseData,
			ReceiverId: envelope.SenderId,
			TeamId:     envelope.TeamId,
		}) && !e.connMgr.SendViaChannel(envelope.SenderId, &chatpb.Envelope{
			Type:       3,
			SenderId:   e.crypto.GetPublicKeyBase64(),
			Payload:    responseData,
			ReceiverId: envelope.SenderId,
			TeamId:     envelope.TeamId,
		}) {
			e.log(logger.Warning, "file", "failed to send file browse response via any path")
		} else {
			e.log(logger.Debug, "file", fmt.Sprintf("browse resp %s sent via dual-path", envelope.TeamId))
		}
		return
	}

	if envelope.Type == 3 {
		e.connMgr.pendingReqsMu.Lock()
		ch, ok := e.connMgr.pendingReqs[envelope.TeamId]
		e.connMgr.pendingReqsMu.Unlock()
		if ok {
			select {
			case ch <- envelope.Payload:
				e.log(logger.Debug, "file", fmt.Sprintf("browse resp %s dispatched via StreamChat", envelope.TeamId))
			default:
				e.log(logger.Warning, "file", fmt.Sprintf("browse resp %s channel full (sc)", envelope.TeamId))
			}
		} else {
			e.log(logger.Warning, "file", fmt.Sprintf("browse resp %s: no pending request (sc)", envelope.TeamId))
		}
		return
	}

	var plain []byte
	switch envelope.Type {
	case 1: // Private: DecryptTeam(outer) -> DecryptPrivate(inner)
		teamPlain, err := e.crypto.DecryptTeam(envelope.Payload)
		if err != nil {
			e.log(logger.Warning, "chat", fmt.Sprintf("private message DecryptTeam failed: %v", err))
			return
		}
		plain, err = e.crypto.DecryptPrivate(teamPlain, envelope.SenderId)
		if err != nil {
			e.log(logger.Warning, "chat", fmt.Sprintf("private message DecryptPrivate failed: %v", err))
			return
		}
	default: // Type 0 (team) or 99 (test): DecryptTeam only
		var err error
		if envelope.TeamId != "" {
			plain, err = e.crypto.DecryptTeamForTeam(envelope.TeamId, envelope.Payload)
		} else {
			plain, err = e.crypto.DecryptTeam(envelope.Payload)
		}
		if err != nil {
			e.log(logger.Warning, "chat", fmt.Sprintf("DecryptTeam failed: %v", err))
			return
		}
	}

	var msg eventbus.MsgReceivedPayload
	if err := json.Unmarshal(plain, &msg); err != nil {
		e.log(logger.Warning, "chat", fmt.Sprintf("unmarshal error: %v", err))
		return
	}

	msg.TeamID = envelope.TeamId
	msg.ChannelID = envelope.ChannelId

	if envelope.Type == 1 {
		msg.ReceiverID = envelope.ReceiverId
		e.bus.Publish(eventbus.TopicPrivateMsgReceived, msg)
	} else {
		e.bus.Publish(eventbus.TopicMsgReceived, msg)
	}
	e.connMgr.RefreshSeen(envelope.SenderId)
}
