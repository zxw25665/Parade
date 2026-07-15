package network

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	libp2pNetwork "github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"parade/internal/core/crypto"
	"parade/internal/core/db"
	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
	merkleSync "parade/internal/core/sync"
)

const (
	protocolIdentify = "/parade/identify/1.0.0"
	peersFile        = "./.parade_peers"
)

type peerIdentity struct {
	UUID   string
	PubKey string
}

type libp2pEngine struct {
	mu      sync.RWMutex
	started bool

	peerMap map[peer.ID]*peerIdentity
	uuidMap map[string]peer.ID
	peerMu  sync.RWMutex

	host        *libp2pHost
	chat        *libp2pChat
	file        *libp2pFile
	linearSync  *libp2pSync
	merkleSync  *libp2pMerkleSync
	mdns        *MDNSService
	bus         eventbus.EventBus
	crypto      crypto.Engine
	logr        logger.Logger
	port        int
	mdnsEnabled bool

	identifyLn   net.Listener
	fileEngine   FileTransferEngine
	merkleSyncH  merkleSync.MerkleSyncHandler
}

func NewLibp2pEngine(bus eventbus.EventBus, cry crypto.Engine, logr logger.Logger) *libp2pEngine {
	return &libp2pEngine{
		bus:    bus,
		crypto: cry,
		logr:   logr,
	}
}

func (e *libp2pEngine) setPeer(pid peer.ID, uuid, pubkey string) {
	e.peerMu.Lock()
	defer e.peerMu.Unlock()
	if e.peerMap == nil {
		e.peerMap = make(map[peer.ID]*peerIdentity)
	}
	if e.uuidMap == nil {
		e.uuidMap = make(map[string]peer.ID)
	}
	e.peerMap[pid] = &peerIdentity{UUID: uuid, PubKey: pubkey}
	e.uuidMap[uuid] = pid
}

func (e *libp2pEngine) getPeerIdentity(pid peer.ID) *peerIdentity {
	e.peerMu.RLock()
	defer e.peerMu.RUnlock()
	return e.peerMap[pid]
}

func (e *libp2pEngine) getPeerID(uuid string) (peer.ID, bool) {
	e.peerMu.RLock()
	defer e.peerMu.RUnlock()
	pid, ok := e.uuidMap[uuid]
	return pid, ok
}

func (e *libp2pEngine) lookupPubkey(uuid string) string {
	e.peerMu.RLock()
	defer e.peerMu.RUnlock()
	if pid, ok := e.uuidMap[uuid]; ok {
		if ident := e.peerMap[pid]; ident != nil {
			return ident.PubKey
		}
	}
	return ""
}

func (e *libp2pEngine) ResolveUUID(uuid string) (string, error) {
	pubkey := e.lookupPubkey(uuid)
	if pubkey == "" {
		return "", fmt.Errorf("pubkey not found for UUID %s", uuid)
	}
	return pubkey, nil
}

func (e *libp2pEngine) AttachFileEngine(fe FileTransferEngine) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fileEngine = fe
}

func (e *libp2pEngine) log(level logger.LogLevel, source, msg string) {
	if e.logr == nil {
		return
	}
	switch level {
	case logger.Trace:
		e.logr.Trace(source, msg)
	case logger.Debug:
		e.logr.Debug(source, msg)
	case logger.Info:
		e.logr.Info(source, msg)
	case logger.Warning:
		e.logr.Warn(source, msg)
	case logger.Error:
		e.logr.Error(source, msg)
	}
}

func (e *libp2pEngine) identifyRemotePeer(pid peer.ID) (uuid, pubkey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := e.host.NewStream(ctx, pid, protocolIdentify)
	if err != nil {
		return "", ""
	}
	defer stream.Close()
	var resp struct {
		PeerID string `json:"peer_id"`
		UUID   string `json:"uuid"`
		PubKey string `json:"pubkey"`
	}
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		return "", ""
	}
	return resp.UUID, resp.PubKey
}

func (e *libp2pEngine) startIdentifyServer(port int) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		e.log(logger.Warning, "libp2p", fmt.Sprintf("identify server listen on :%d failed: %v", port, err))
		return
	}
	e.identifyLn = ln
	e.log(logger.Info, "libp2p", fmt.Sprintf("identify server on :%d", port))

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				resp, _ := json.Marshal(map[string]string{
					"peer_id": e.host.ID().String(),
					"uuid":    e.crypto.GetPersonalUUID(),
					"pubkey":  e.crypto.GetPublicKeyBase64(),
				})
				c.Write(append(resp, '\n'))
			}(conn)
		}
	}()
}

func (e *libp2pEngine) Start(port int) error {
	return e.StartWithMDNS(port, "0.0.0.0", true)
}

func (e *libp2pEngine) StartWithMDNS(port int, listenAddr string, mdnsEnabled bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.started {
		return nil
	}

	priv := e.crypto.GetPrivateKey()
	if priv == nil {
		return fmt.Errorf("identity not loaded")
	}

	host, err := NewLibp2pHost(priv, port, listenAddr, e.bus, e.crypto, e.logr)
	if err != nil {
		return fmt.Errorf("Start: %w", err)
	}
	e.host = host

	// When a remote peer connects, identify them and trigger sync.
	// On disconnect, clean up stale state so reconnection works.
	host.Network().Notify(&libp2pNetwork.NotifyBundle{
		ConnectedF: func(n libp2pNetwork.Network, conn libp2pNetwork.Conn) {
			remoteID := conn.RemotePeer()
			if remoteID == host.ID() {
				return
			}
			if e.getPeerIdentity(remoteID) != nil {
				return // already registered by connectToPeer or onAutoRegister
			}
			go func(pid peer.ID) {
				uuid, pubkey := e.identifyRemotePeer(pid)
				if uuid == "" {
					return
				}
				e.setPeer(pid, uuid, pubkey)
				e.savePeers()
				e.bus.Publish(eventbus.TopicPeerJoined, eventbus.PeerEventPayload{
					PeerUUID:  uuid,
					IPAddress: extractIPFromPeer(host.Network(), pid),
				})
				e.log(logger.Info, "libp2p", fmt.Sprintf("connection from %s identified as uuid=%s", pid.ShortString(), uuid[:16]))
			}(remoteID)
		},
		DisconnectedF: func(n libp2pNetwork.Network, conn libp2pNetwork.Conn) {
			remoteID := conn.RemotePeer()
			e.peerMu.Lock()
			ident := e.peerMap[remoteID]
			delete(e.peerMap, remoteID)
			if ident != nil {
				delete(e.uuidMap, ident.UUID)
			}
			e.peerMu.Unlock()
			e.savePeers()
			if ident != nil {
				e.bus.Publish(eventbus.TopicPeerLeft, eventbus.PeerEventPayload{
					PeerUUID:  ident.UUID,
					IPAddress: extractIPFromPeer(host.Network(), remoteID),
				})
				e.log(logger.Info, "libp2p", fmt.Sprintf("peer %s disconnected (uuid=%s)", remoteID.ShortString(), ident.UUID[:16]))
			}
		},
	})

	chat, err := NewLibp2pChat(host.Host, e.bus, e.crypto, e.logr)
	if err != nil {
		host.Close()
		return fmt.Errorf("Start: chat: %w", err)
	}
	e.chat = chat

	chat.onPubkeyLookup = func(uuid string) string { return e.lookupPubkey(uuid) }
	chat.onAutoRegister = func(pid peer.ID, uuid string) {
		e.setPeer(pid, uuid, "")
		e.savePeers()
		e.bus.Publish(eventbus.TopicPeerJoined, eventbus.PeerEventPayload{
			PeerUUID:  uuid,
			IPAddress: extractIPFromPeer(host.Network(), pid),
		})
		e.log(logger.Info, "chat", fmt.Sprintf("auto-registered peer %s (uuid=%s)", pid.ShortString(), uuid[:16]))
	}
	chat.onPeerInfoReceived = func(pid peer.ID, ip, pubkey string) {
		e.peerMu.Lock()
		ident := e.peerMap[pid]
		needsSave := false
		if ident != nil {
			if ident.PubKey == "" && pubkey != "" {
				ident.PubKey = pubkey
				needsSave = true
			}
		} else {
			e.peerMap[pid] = &peerIdentity{UUID: "", PubKey: pubkey}
			needsSave = true
		}
		e.peerMu.Unlock()
		if needsSave {
			e.savePeers()
		}
		// Try to establish a direct libp2p connection to the peer using their IP
		if ip != "" {
			go func(ipAddr string) {
				maddr, _ := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/%d", ipAddr, e.port))
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				e.host.Connect(ctx, peer.AddrInfo{ID: pid, Addrs: []multiaddr.Multiaddr{maddr}})
				// ConnectedF handles the rest if successful
			}(ip)
		}
	}
	chat.hasPeerIdentity = func(pid peer.ID) bool { return e.getPeerIdentity(pid) != nil }

	// Register identify stream handler — responds with PeerID + UUID + Curve25519 pubkey
	host.SetStreamHandler(protocolIdentify, func(stream libp2pNetwork.Stream) {
		defer stream.Close()
		json.NewEncoder(stream).Encode(map[string]string{
			"peer_id": host.ID().String(),
			"uuid":    e.crypto.GetPersonalUUID(),
			"pubkey":  e.crypto.GetPublicKeyBase64(),
		})
	})

	if err := chat.JoinTeam(e.crypto.TeamKeyHash()); err != nil {
		host.Close()
		return fmt.Errorf("Start: join team: %w", err)
	}

	if e.fileEngine != nil {
		e.file = NewLibp2pFile(host.Host, e.fileEngine, e.crypto.GetPersonalUUID(), e.bus, e.logr)
	}

	host.SetStreamHandler(protocolHandshake, func(stream libp2pNetwork.Stream) {
		defer stream.Close()
		data, err := io.ReadAll(stream)
		if err != nil {
			return
		}
		plain, err := e.crypto.DecryptTeam(data)
		if err != nil {
			stream.Write([]byte("TEAM_MISMATCH"))
			return
		}
		reply, err := e.crypto.EncryptTeam(plain)
		if err != nil {
			return
		}
		stream.Write(reply)
	})

	host.SetStreamHandler(protocolTest, func(stream libp2pNetwork.Stream) {
		defer stream.Close()
		stream.Write([]byte{0x01})
	})

	e.linearSync = NewLibp2pSync(host.Host, e.bus, e.logr)
	e.linearSync.onUUIDLookup = func(pid peer.ID) string {
		if ident := e.getPeerIdentity(pid); ident != nil {
			return ident.UUID
		}
		return pid.String()
	}

	e.merkleSync = NewLibp2pMerkleSync(host.Host, e.bus, e.logr)
	e.merkleSync.onUUIDLookup = func(pid peer.ID) string {
		if ident := e.getPeerIdentity(pid); ident != nil {
			return ident.UUID
		}
		return pid.String()
	}
	if e.merkleSyncH != nil {
		e.merkleSync.handler = e.merkleSyncH
	}

	e.startIdentifyServer(port + 1)

	e.port = port
	e.mdnsEnabled = mdnsEnabled

	if mdnsEnabled {
		e.mdns = NewMDNSService(host, e.bus, e.logr)
		e.mdns.SetOnPeerFound(func(info peer.AddrInfo) {
			if info.ID == host.ID() || len(info.Addrs) == 0 {
				return
			}
			e.log(logger.Debug, "mdns", fmt.Sprintf("peer discovered: %s", info.ID.ShortString()))
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				e.host.Connect(ctx, info)
			}()
		})
		if err := e.mdns.Start(); err != nil {
			e.log(logger.Warning, "mdns", fmt.Sprintf("mDNS start failed (non-critical): %v", err))
			e.mdns = nil
		}
	}

	e.started = true

	e.log(logger.Info, "libp2p", fmt.Sprintf("libp2p engine started on port %d", port))

	go e.loadAndReconnect()

	return nil
}

func (e *libp2pEngine) Stop() error {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return nil
	}
	host := e.host
	chat := e.chat
	mdnsSvc := e.mdns
	identifyLn := e.identifyLn
	e.started = false
	e.host = nil
	e.chat = nil
	e.linearSync = nil
	e.mdns = nil
	e.identifyLn = nil
	e.mu.Unlock()

	_ = e.savePeers()

	if identifyLn != nil {
		identifyLn.Close()
	}

	if chat != nil {
		chat.Close()
	}

	if mdnsSvc != nil {
		mdnsSvc.Stop()
	}

	if host != nil {
		return host.Close()
	}
	return nil
}

func (e *libp2pEngine) Peers() []map[string]string {
	e.mu.RLock()
	host := e.host
	e.mu.RUnlock()

	if host == nil {
		return nil
	}

	conns := host.Network().Peers()
	out := make([]map[string]string, 0, len(conns))
	for _, pid := range conns {
		ip := extractIPFromPeer(host.Network(), pid)
		uuid := ""
		if ident := e.getPeerIdentity(pid); ident != nil {
			uuid = ident.UUID
		}
		out = append(out, map[string]string{
			"pubKey": uuid,
			"ip":     ip,
		})
	}
	return out
}

func (e *libp2pEngine) PeersWithStatus() []PeerStatus {
	e.mu.RLock()
	host := e.host
	e.mu.RUnlock()

	if host == nil {
		return nil
	}

	conns := host.Network().Peers()
	out := make([]PeerStatus, 0, len(conns))
	for _, pid := range conns {
		ip := extractIPFromPeer(host.Network(), pid)
		uuid := ""
		if ident := e.getPeerIdentity(pid); ident != nil {
			uuid = ident.UUID
		}
		out = append(out, PeerStatus{
			PeerInfo: PeerInfo{
				PeerUUID:  uuid,
				IPAddress: ip,
			},
			Status: PeerStatusOnline,
		})
	}
	return out
}

func extractIPFromPeer(h libp2pNetwork.Network, pid peer.ID) string {
	conns := h.ConnsToPeer(pid)
	if len(conns) == 0 {
		return ""
	}
	remoteAddr := conns[0].RemoteMultiaddr()
	return extractIP(peer.AddrInfo{
		ID:    pid,
		Addrs: []multiaddr.Multiaddr{remoteAddr},
	})
}

func (e *libp2pEngine) BroadcastTeam(payload []byte) error {
	if e.chat == nil {
		return fmt.Errorf("chat not initialized")
	}
	return e.chat.BroadcastTeam(e.crypto.TeamKeyHash(), payload)
}

func (e *libp2pEngine) UnicastPrivate(targetUUID string, payload []byte) error {
	if e.chat == nil {
		return fmt.Errorf("chat not initialized")
	}

	targetPeerID, ok := e.getPeerID(targetUUID)
	if !ok {
		return fmt.Errorf("peer not found for UUID %s", targetUUID)
	}

	wrapped, err := e.crypto.EncryptTeam(payload)
	if err != nil {
		return fmt.Errorf("UnicastPrivate: encrypt team: %w", err)
	}

	return e.chat.UnicastPrivate(targetPeerID, e.crypto.GetPersonalUUID(), e.crypto.GetPublicKeyBase64(), wrapped)
}

func (e *libp2pEngine) StartDownload(targetUUID, virtualPath, localSavePath string) error {
	if e.file == nil {
		return fmt.Errorf("file engine not attached")
	}
	targetPeerID, ok := e.getPeerID(targetUUID)
	if !ok {
		return fmt.Errorf("peer not found for UUID %s", targetUUID)
	}
	return e.file.StartDownload(targetPeerID, virtualPath, localSavePath)
}

func (e *libp2pEngine) ConnectToPeer(ipAddress string) (*PeerConnectResult, error) {
	return e.connectToPeer(ipAddress)
}

func (e *libp2pEngine) BrowseRemoteDirectory(targetUUID, path string) ([]*BrowseEntry, error) {
	if e.file == nil {
		return nil, fmt.Errorf("file engine not attached")
	}
	targetPeerID, ok := e.getPeerID(targetUUID)
	if !ok {
		return nil, fmt.Errorf("peer not found for UUID %s", targetUUID)
	}
	return e.file.BrowseRemote(targetPeerID, path)
}

func (e *libp2pEngine) OnForeground() {}

func (e *libp2pEngine) SendConvSyncRequest(targetUUID, convID, sinceHLC string) error {
	if e.linearSync == nil {
		return fmt.Errorf("sync not initialized")
	}
	targetPeerID, ok := e.getPeerID(targetUUID)
	if !ok {
		return fmt.Errorf("peer not found for UUID %s", targetUUID)
	}
	return e.linearSync.SendConvSyncRequest(targetPeerID, convID, sinceHLC)
}

func (e *libp2pEngine) SendConvSyncResponse(targetUUID, convID string, messagesJSON []byte) error {
	if e.linearSync == nil {
		return fmt.Errorf("sync not initialized")
	}
	targetPeerID, ok := e.getPeerID(targetUUID)
	if !ok {
		return fmt.Errorf("peer not found for UUID %s", targetUUID)
	}
	return e.linearSync.SendConvSyncResponse(targetPeerID, convID, messagesJSON)
}

func (e *libp2pEngine) SendMerkleRootRequest(targetUUID, convID string) ([]byte, error) {
	if e.merkleSync == nil {
		return nil, fmt.Errorf("merkle sync not initialized")
	}
	targetPeerID, ok := e.getPeerID(targetUUID)
	if !ok {
		return nil, fmt.Errorf("peer not found for UUID %s", targetUUID)
	}
	return e.merkleSync.SendMerkleRootRequest(targetPeerID, convID)
}

func (e *libp2pEngine) SendBucketCompareRequest(targetUUID, convID string, level int, paths []string) ([]merkleSync.BucketInfo, error) {
	if e.merkleSync == nil {
		return nil, fmt.Errorf("merkle sync not initialized")
	}
	targetPeerID, ok := e.getPeerID(targetUUID)
	if !ok {
		return nil, fmt.Errorf("peer not found for UUID %s", targetUUID)
	}
	return e.merkleSync.SendBucketCompareRequest(targetPeerID, convID, level, paths)
}

func (e *libp2pEngine) SendFetchMessagesRequest(targetUUID, convID, bucketPath, sinceHLC string) ([]*db.Message, error) {
	if e.merkleSync == nil {
		return nil, fmt.Errorf("merkle sync not initialized")
	}
	targetPeerID, ok := e.getPeerID(targetUUID)
	if !ok {
		return nil, fmt.Errorf("peer not found for UUID %s", targetUUID)
	}
	return e.merkleSync.SendFetchMessagesRequest(targetPeerID, convID, bucketPath, sinceHLC)
}

func (e *libp2pEngine) SendPushMessages(targetUUID, convID string, messages []*db.Message) error {
	if e.merkleSync == nil {
		return fmt.Errorf("merkle sync not initialized")
	}
	targetPeerID, ok := e.getPeerID(targetUUID)
	if !ok {
		return fmt.Errorf("peer not found for UUID %s", targetUUID)
	}
	return e.merkleSync.SendPushMessages(targetPeerID, convID, messages)
}

func (e *libp2pEngine) SetMerkleSyncHandler(handler merkleSync.MerkleSyncHandler) {
	e.merkleSyncH = handler
	if e.merkleSync != nil {
		e.merkleSync.handler = handler
	}
}

func (e *libp2pEngine) SavePeers() error {
	return e.savePeers()
}

type savedPeer struct {
	PeerID string `json:"peer_id"`
	UUID   string `json:"uuid"`
	PubKey string `json:"pubkey"`
	IP     string `json:"ip"`
}

func (e *libp2pEngine) savePeers() error {
	e.peerMu.RLock()
	defer e.peerMu.RUnlock()

	var list []savedPeer
	for pid, ident := range e.peerMap {
		ip := extractIPFromPeer(e.host.Network(), pid)
		list = append(list, savedPeer{
			PeerID: pid.String(),
			UUID:   ident.UUID,
			PubKey: ident.PubKey,
			IP:     ip,
		})
	}
	data, _ := json.Marshal(list)
	return os.WriteFile(peersFile, data, 0600)
}

func (e *libp2pEngine) loadAndReconnect() {
	data, err := os.ReadFile(peersFile)
	if err != nil {
		return
	}
	var list []savedPeer
	if err := json.Unmarshal(data, &list); err != nil {
		return
	}
		e.log(logger.Info, "libp2p", fmt.Sprintf("loaded %d known peers, trying to reconnect", len(list)))
	for _, p := range list {
		if p.UUID == e.crypto.GetPersonalUUID() {
			continue
		}
		// Inject saved pubkey into peerMap so ResolveUUID works even before reconnection
		if p.PeerID != "" && p.PubKey != "" {
			pid, err := peer.Decode(p.PeerID)
			if err == nil {
				e.setPeer(pid, p.UUID, p.PubKey)
			}
		}
		go func(p savedPeer) {
			pid, err := peer.Decode(p.PeerID)
			if err != nil {
				return
			}
			// Try direct IP connect first, fall back to PeerID-only connect
			if p.IP != "" {
				result, _ := e.ConnectToPeer(p.IP)
				if result != nil && result.Phase1.Success {
					e.log(logger.Info, "libp2p", fmt.Sprintf("reconnected to %s at %s", p.PeerID[:12], p.IP))
					return
				}
			}
			// Fallback: try connecting by PeerID (libp2p may route through existing peers)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = e.host.Connect(ctx, peer.AddrInfo{ID: pid})
		}(p)
	}
}
