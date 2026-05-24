package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"parade/internal/core/crypto"
	"parade/internal/core/eventbus"
	chatpb "parade/internal/network/pb/chatpb"
	pb "parade/internal/network/pb"
)

// Engine 是网络控制面实现，满足 app.NetworkEngine。
type Engine struct {
	mu      sync.RWMutex
	started bool

	bus    eventbus.EventBus
	crypto crypto.Engine

	discovery *Discovery
	filePlane *FilePlane
	fileEngine FileTransferEngine

	// 控制面 gRPC 服务器字段
	controlListener  net.Listener
	controlServer    *grpc.Server
	chatServiceImpl   *ChatServiceImpl
	controlPort      int

	// 数据面 gRPC 服务器字段
	dataListener     net.Listener
	dataServer       *grpc.Server
	fileServiceImpl  *FileService
	dataPort         int

	// mDNS 服务字段
	mdnsHandle ServiceHandle

	// gRPC 客户端连接池（控制面）
	clientConns map[string]*grpc.ClientConn // 按 pubKey 索引
	connMu      sync.RWMutex

	// gRPC 客户端连接池（数据面）
	dataClientConns map[string]*grpc.ClientConn
	dataConnMu      sync.RWMutex

	// 对等节点会话（健康监测 + 自动重连）
	peerSessions map[string]*PeerSession
	sessionMu    sync.RWMutex

	discoveryCtx    context.Context
	discoveryCancel context.CancelFunc
	lifecycleWG     sync.WaitGroup
}

// ChatServiceImpl 实现 ChatService 的 gRPC 服务。
type ChatServiceImpl struct {
	engine *Engine
	chatpb.UnimplementedChatServiceServer
}

func NewEngine(bus eventbus.EventBus, cry crypto.Engine) *Engine {
	eng := &Engine{
		bus:             bus,
		crypto:          cry,
		discovery:       NewDiscovery(bus, NewServiceBrowser()),
		filePlane:       NewFilePlane(bus),
		controlPort:     4327,
		dataPort:        4328,
		clientConns:     make(map[string]*grpc.ClientConn),
		dataClientConns: make(map[string]*grpc.ClientConn),
		peerSessions:    make(map[string]*PeerSession),
	}
	eng.chatServiceImpl = &ChatServiceImpl{engine: eng}
	return eng
}

func (e *Engine) Start(port int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return nil
	}

	if port > 0 {
		e.controlPort = port
	}

	keepaliveServerOpts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:              60 * time.Second,
			Timeout:           10 * time.Second,
			MaxConnectionIdle: 30 * time.Minute,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	// 启动控制面 gRPC 服务器（端口 4327）
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", e.controlPort))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", e.controlPort, err)
	}

	e.controlListener = listener
	e.controlServer = grpc.NewServer(keepaliveServerOpts...)
	chatpb.RegisterChatServiceServer(e.controlServer, e.chatServiceImpl)

	e.lifecycleWG.Add(1)
	go func() {
		defer e.lifecycleWG.Done()
		if err := e.controlServer.Serve(listener); err != nil && err != grpc.ErrServerStopped {
			fmt.Printf("gRPC control server error: %v\n", err)
		}
	}()

	// 启动数据面 gRPC 服务器（端口 4328）
	if e.fileEngine != nil {
		dataListener, err := net.Listen("tcp", fmt.Sprintf(":%d", e.dataPort))
		if err != nil {
			return fmt.Errorf("failed to listen on data port %d: %w", e.dataPort, err)
		}

		e.dataListener = dataListener
		e.dataServer = grpc.NewServer(keepaliveServerOpts...)
		e.fileServiceImpl = NewFileService(e.fileEngine, e.crypto.GetPublicKeyBase64())
		pb.RegisterFileTransferServiceServer(e.dataServer, e.fileServiceImpl)

		e.lifecycleWG.Add(1)
		go func() {
			defer e.lifecycleWG.Done()
			if err := e.dataServer.Serve(dataListener); err != nil && err != grpc.ErrServerStopped {
				fmt.Printf("gRPC data server error: %v\n", err)
			}
		}()
	}

	// mDNS: register _parade._tcp service via ServiceBrowser
	iface, lanIP, err := SelectLANInterface()
	if err != nil {
		fmt.Printf("[mDNS] WARNING: %v, discovery may not work\n", err)
	} else {
		fmt.Printf("[mDNS] selected interface %s (%s)\n", iface.Name, lanIP)
	}

	teamHash := e.crypto.TeamKeyHash()
	teamIDs := e.crypto.GetTeamIDs()
	var teamHashes []string
	for _, tid := range teamIDs {
		if h := e.crypto.TeamKeyHashFor(tid); h != "" {
			teamHashes = append(teamHashes, h)
		}
	}
	teamHashStr := strings.Join(teamHashes, ",")

	info := []string{
		fmt.Sprintf("pubkey=%s", e.crypto.GetPublicKeyBase64()),
		fmt.Sprintf("team=%s", teamHashStr),
	}
	lanIPs := []net.IP{lanIP}

	browser := NewServiceBrowser()
	e.discovery = NewDiscovery(e.bus, browser)

	handle, err := browser.Register(
		"Parade-"+e.crypto.GetPublicKeyBase64()[:8],
		"_parade._tcp",
		"local.",
		e.controlPort,
		lanIPs,
		info,
		iface,
	)
	if err != nil {
		return fmt.Errorf("failed to register mDNS service: %w", err)
	}
	e.mdnsHandle = handle
	fmt.Printf("[mDNS] started, advertising %s on %v (team=%s)\n",
		"_parade._tcp", lanIPs, truncateKey(teamHash))

	// 启动 Discovery 循环
	e.discovery.SetLocalPubKey(e.crypto.GetPublicKeyBase64())
	e.discovery.SetTeamHashes(teamHashes)
	if iface != nil {
		e.discovery.SetIface(iface)
	}
	e.discovery.SetOnPeerDiscovered(func(peer PeerInfo) {
		go func() {
			if _, err := e.getOrDialPeer(peer.PubKeyBase64, peer.IPAddress); err != nil {
				fmt.Printf("[mDNS] auto-connect to peer %s failed: %v\n", truncateKey(peer.PubKeyBase64), err)
			} else {
				fmt.Printf("[mDNS] auto-connected to peer %s @ %s\n", truncateKey(peer.PubKeyBase64), peer.IPAddress)
			}
		}()
	})
	e.discoveryCtx, e.discoveryCancel = context.WithCancel(context.Background())
	e.lifecycleWG.Add(1)
	go func() {
		defer e.lifecycleWG.Done()
		e.discovery.Run(e.discoveryCtx)
	}()

	e.started = true
	return nil
}

func (e *Engine) Stop() error {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return nil
	}
	
	cancel := e.discoveryCancel
	server := e.controlServer
	dataSrv := e.dataServer
	mdnsHandle := e.mdnsHandle
	e.started = false
	e.discoveryCtx = nil
	e.discoveryCancel = nil
	e.controlServer = nil
	e.dataServer = nil
	e.mdnsHandle = nil
	e.mu.Unlock()

	if mdnsHandle != nil {
		mdnsHandle.Shutdown()
	}

	e.sessionMu.Lock()
	for _, session := range e.peerSessions {
		session.Stop()
	}
	e.peerSessions = make(map[string]*PeerSession)
	e.sessionMu.Unlock()

	e.connMu.Lock()
	for _, conn := range e.clientConns {
		conn.Close()
	}
	e.clientConns = make(map[string]*grpc.ClientConn)
	e.connMu.Unlock()

	e.dataConnMu.Lock()
	for _, conn := range e.dataClientConns {
		conn.Close()
	}
	e.dataClientConns = make(map[string]*grpc.ClientConn)
	e.dataConnMu.Unlock()

	if dataSrv != nil {
		dataSrv.GracefulStop()
	}

	if server != nil {
		server.GracefulStop()
	}

	if cancel != nil {
		cancel()
	}

	e.lifecycleWG.Wait()
	return nil
}

// BroadcastTeam 广播消息到所有发现的对等节点。
func (e *Engine) BroadcastTeam(payload []byte) error {
	e.mu.RLock()
	started := e.started
	e.mu.RUnlock()
	if !started {
		return errors.New("network engine not started")
	}

	// 获取当前队伍的对等节点
	teamHash := e.crypto.TeamKeyHash()
	var peers []PeerInfo
	if teamHash != "" {
		peers = e.discovery.GetPeersForTeam(teamHash)
	} else {
		peers = e.discovery.Snapshot()
	}
	if len(peers) == 0 {
		// 没有对等节点，仅本地处理
		plain, err := e.crypto.DecryptTeam(payload)
		if err != nil {
			return err
		}

		var msg eventbus.MsgReceivedPayload
		if err := json.Unmarshal(plain, &msg); err != nil {
			return err
		}

		e.bus.Publish(eventbus.TopicMsgReceived, msg)
		return nil
	}

	// 异步发送到所有对等节点
	go func() {
		envelope := &chatpb.Envelope{
			SenderId:   e.crypto.GetPublicKeyBase64(),
			Payload:    payload,
			Type:       0,
			ReceiverId: "",
			TeamId:     e.crypto.GetActiveTeam(),
		}

		for _, peer := range peers {
			go func(p PeerInfo) {
				conn, err := e.getOrDialPeer(p.PubKeyBase64, p.IPAddress)
				if err != nil {
					fmt.Printf("failed to dial peer %s: %v\n", p.PubKeyBase64, err)
					return
				}

				client := chatpb.NewChatServiceClient(conn)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				stream, err := client.StreamChat(ctx)
				if err != nil {
					fmt.Printf("failed to create stream with %s: %v\n", p.PubKeyBase64, err)
					return
				}
				defer stream.CloseSend()

				// 发送消息
				if err := stream.Send(envelope); err != nil {
					fmt.Printf("failed to send to %s: %v\n", p.PubKeyBase64, err)
					e.removePeerConnection(p.PubKeyBase64)
					return
				}

				// 等待确认
				_, err = stream.Recv()
				if err != nil {
					fmt.Printf("failed to receive ack from %s: %v\n", p.PubKeyBase64, err)
					e.removePeerConnection(p.PubKeyBase64)
					return
				}
			}(peer)
		}
	}()

	return nil
}

// BroadcastChannel 广播频道消息到所有发现的对等节点。
func (e *Engine) BroadcastChannel(channelID string, payload []byte) error {
	e.mu.RLock()
	started := e.started
	e.mu.RUnlock()
	if !started {
		return errors.New("network engine not started")
	}

	// 获取当前队伍的对等节点
	teamHash := e.crypto.TeamKeyHash()
	var peers []PeerInfo
	if teamHash != "" {
		peers = e.discovery.GetPeersForTeam(teamHash)
	} else {
		peers = e.discovery.Snapshot()
	}
	if len(peers) == 0 {
		// 没有对等节点，仅本地处理
		plain, err := e.crypto.DecryptTeam(payload)
		if err != nil {
			return err
		}

		var msg eventbus.MsgReceivedPayload
		if err := json.Unmarshal(plain, &msg); err != nil {
			return err
		}
		msg.ChannelID = channelID

		e.bus.Publish(eventbus.TopicMsgReceived, msg)
		return nil
	}

	// 异步发送到所有对等节点
	go func() {
		envelope := &chatpb.Envelope{
			SenderId:   e.crypto.GetPublicKeyBase64(),
			Payload:    payload,
			Type:       0,
			ReceiverId: "",
			TeamId:     e.crypto.GetActiveTeam(),
			ChannelId:  channelID,
		}

		for _, peer := range peers {
			go func(p PeerInfo) {
				conn, err := e.getOrDialPeer(p.PubKeyBase64, p.IPAddress)
				if err != nil {
					fmt.Printf("failed to dial peer %s: %v\n", p.PubKeyBase64, err)
					return
				}

				client := chatpb.NewChatServiceClient(conn)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				stream, err := client.StreamChat(ctx)
				if err != nil {
					fmt.Printf("failed to create stream with %s: %v\n", p.PubKeyBase64, err)
					return
				}
				defer stream.CloseSend()

				// 发送消息
				if err := stream.Send(envelope); err != nil {
					fmt.Printf("failed to send to %s: %v\n", p.PubKeyBase64, err)
					e.removePeerConnection(p.PubKeyBase64)
					return
				}

				// 等待确认
				_, err = stream.Recv()
				if err != nil {
					fmt.Printf("failed to receive ack from %s: %v\n", p.PubKeyBase64, err)
					e.removePeerConnection(p.PubKeyBase64)
					return
				}
			}(peer)
		}
	}()

	return nil
}

// UnicastPrivate 发送私聊消息到指定对等节点。
func (e *Engine) UnicastPrivate(targetPubKey string, payload []byte) error {
	if targetPubKey == "" {
		return errors.New("target public key is required")
	}

	e.mu.RLock()
	started := e.started
	e.mu.RUnlock()
	if !started {
		return errors.New("network engine not started")
	}

	// 查找目标对等节点
	peer, exists := e.discovery.GetPeerByPubKey(targetPubKey)
	if !exists {
		return fmt.Errorf("peer %s not found in discovery", targetPubKey)
	}

	// 异步发送消息
	go func() {
		// Double encryption: payload is already EncryptPrivate(inner), wrap with EncryptTeam(outer)
		wrapped, err := e.crypto.EncryptTeam(payload)
		if err != nil {
			fmt.Printf("EncryptTeam wrap for private message failed: %v\n", err)
			return
		}

		envelope := &chatpb.Envelope{
			SenderId:   e.crypto.GetPublicKeyBase64(),
			Payload:    wrapped,
			Type:       1,
			ReceiverId: targetPubKey,
			TeamId:     e.crypto.GetActiveTeam(),
		}

		conn, err := e.getOrDialPeer(peer.PubKeyBase64, peer.IPAddress)
		if err != nil {
			fmt.Printf("failed to dial peer %s: %v\n", peer.PubKeyBase64, err)
			return
		}

		client := chatpb.NewChatServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		stream, err := client.StreamChat(ctx)
		if err != nil {
			fmt.Printf("failed to create stream with %s: %v\n", peer.PubKeyBase64, err)
			return
		}
		defer stream.CloseSend()

		// 发送消息
		if err := stream.Send(envelope); err != nil {
			fmt.Printf("failed to send to %s: %v\n", peer.PubKeyBase64, err)
			e.removePeerConnection(peer.PubKeyBase64)
			return
		}

		// 等待确认
		_, err = stream.Recv()
		if err != nil {
			fmt.Printf("failed to receive ack from %s: %v\n", peer.PubKeyBase64, err)
			e.removePeerConnection(peer.PubKeyBase64)
			return
		}
	}()

	return nil
}

func (e *Engine) Discovery() *Discovery {
	return e.discovery
}

// OnForeground 前台恢复时触发：立即刷新 mDNS 发现 + 对所有已知 peer 执行健康检查。
func (e *Engine) OnForeground() {
	e.mu.RLock()
	started := e.started
	e.mu.RUnlock()
	if !started {
		return
	}

	fmt.Println("[network] foreground resume: triggering discovery refresh and peer health checks")

	e.discovery.TriggerQuery()

	e.sessionMu.RLock()
	sessions := make([]*PeerSession, 0, len(e.peerSessions))
	for _, ps := range e.peerSessions {
		sessions = append(sessions, ps)
	}
	e.sessionMu.RUnlock()

	for _, ps := range sessions {
		if !ps.reconnecting.Load() {
			go func(p *PeerSession) {
				p.checkAndReconnect()
			}(ps)
		}
	}
}

// Peers 返回已发现节点的快照（用于 app.NetworkEngine 接口）。
func (e *Engine) Peers() []map[string]string {
	peers := e.discovery.Snapshot()
	out := make([]map[string]string, 0, len(peers))
	for _, p := range peers {
		out = append(out, map[string]string{
			"pubKey": p.PubKeyBase64,
			"ip":     p.IPAddress,
		})
	}
	return out
}

func (e *Engine) FilePlane() *FilePlane {
	return e.filePlane
}

func (e *Engine) AttachFileEngine(fe FileTransferEngine) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fileEngine = fe
}

func (e *Engine) StartDownload(targetPubKey, virtualPath, localSavePath string) error {
	if targetPubKey == "" || virtualPath == "" || localSavePath == "" {
		return errors.New("targetPubKey, virtualPath and localSavePath are required")
	}
	if e.fileEngine == nil {
		return errors.New("file engine is not attached")
	}
	peer, exists := e.discovery.GetPeerByPubKey(targetPubKey)
	if !exists {
		return fmt.Errorf("peer %s not found in discovery", targetPubKey)
	}
	conn, err := e.getOrDialDataPeer(peer.PubKeyBase64, peer.IPAddress)
	if err != nil {
		return err
	}
	client := pb.NewFileTransferServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	meta, err := client.GetFileMeta(ctx, &pb.FileMetaRequest{
		FilePath: virtualPath,
	})
	if err != nil {
		return fmt.Errorf("get remote file meta failed: %w", err)
	}
	return StartDownloadWithRetry(
		ctx,
		DownloadDeps{
			FileEngine: e.fileEngine,
			Client:     client,
			LocalPeer:  e.crypto.GetPublicKeyBase64(),
		},
		uuid.NewString(),
		targetPubKey,
		virtualPath,
		localSavePath,
		meta.GetTotalSize(),
		DefaultDownloadOptions(),
	)
}

func (e *Engine) BrowseRemoteDirectory(targetPubKey, path string) ([]*pb.BrowseEntry, error) {
	e.mu.RLock()
	started := e.started
	e.mu.RUnlock()
	if !started {
		return nil, errors.New("network engine not started")
	}

	peer, exists := e.discovery.GetPeerByPubKey(targetPubKey)
	if !exists {
		return nil, fmt.Errorf("peer %s not found in discovery", targetPubKey)
	}

	conn, err := e.getOrDialDataPeer(peer.PubKeyBase64, peer.IPAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to peer data plane: %w", err)
	}

	client := pb.NewFileTransferServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.BrowseDirectory(ctx, &pb.BrowseRequest{
		PeerId: targetPubKey,
		Path:   path,
	})
	if err != nil {
		return nil, fmt.Errorf("browse directory RPC failed: %w", err)
	}

	return resp.GetEntries(), nil
}

// getOrDialPeer 获取或建立到对等节点的控制面 gRPC 连接。
func (e *Engine) getOrDialPeer(pubKey, ipAddr string) (*grpc.ClientConn, error) {
	if pubKey == "" || ipAddr == "" {
		return nil, errors.New("pubKey and ipAddr are required")
	}

	e.connMu.RLock()
	conn, exists := e.clientConns[pubKey]
	e.connMu.RUnlock()

	if exists && conn != nil {
		state := conn.GetState()
		if state == connectivity.Ready || state == connectivity.Idle {
			return conn, nil
		}
		conn.Close()
		e.connMu.Lock()
		delete(e.clientConns, pubKey)
		e.connMu.Unlock()
	}

	e.connMu.Lock()
	defer e.connMu.Unlock()

	conn, exists = e.clientConns[pubKey]
	if exists && conn != nil {
		return conn, nil
	}

	target := fmt.Sprintf("%s:%d", ipAddr, e.controlPort)
	conn, err := grpc.Dial(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                15 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", target, err)
	}

	e.clientConns[pubKey] = conn

	e.sessionMu.Lock()
	if _, exists := e.peerSessions[pubKey]; !exists {
		session := NewPeerSession(pubKey, ipAddr, e, conn)
		e.peerSessions[pubKey] = session
		session.Start()
	}
	e.sessionMu.Unlock()

	return conn, nil
}

// getOrDialDataPeer 获取或建立到对等节点的数据面 gRPC 连接。
func (e *Engine) getOrDialDataPeer(pubKey, ipAddr string) (*grpc.ClientConn, error) {
	if pubKey == "" || ipAddr == "" {
		return nil, errors.New("pubKey and ipAddr are required")
	}

	e.dataConnMu.RLock()
	conn, exists := e.dataClientConns[pubKey]
	e.dataConnMu.RUnlock()

	if exists && conn != nil {
		state := conn.GetState()
		if state == connectivity.Ready || state == connectivity.Idle {
			return conn, nil
		}
		conn.Close()
		e.dataConnMu.Lock()
		delete(e.dataClientConns, pubKey)
		e.dataConnMu.Unlock()
	}

	e.dataConnMu.Lock()
	defer e.dataConnMu.Unlock()

	conn, exists = e.dataClientConns[pubKey]
	if exists && conn != nil {
		return conn, nil
	}

	target := fmt.Sprintf("%s:%d", ipAddr, e.dataPort)
	conn, err := grpc.Dial(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                15 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial data %s: %w", target, err)
	}

	e.dataClientConns[pubKey] = conn
	return conn, nil
}

// removePeerConnection 移除到指定对等节点的所有连接并通知 Discovery。
func (e *Engine) removePeerConnection(pubKey string) {
	e.sessionMu.Lock()
	if session, exists := e.peerSessions[pubKey]; exists {
		session.Stop()
		delete(e.peerSessions, pubKey)
	}
	e.sessionMu.Unlock()

	e.connMu.Lock()
	if conn, exists := e.clientConns[pubKey]; exists {
		conn.Close()
		delete(e.clientConns, pubKey)
	}
	e.connMu.Unlock()

	e.dataConnMu.Lock()
	if conn, exists := e.dataClientConns[pubKey]; exists {
		conn.Close()
		delete(e.dataClientConns, pubKey)
	}
	e.dataConnMu.Unlock()

	e.discovery.RemovePeer(pubKey)
}

// getLANIPv4Addrs 返回本机所有非 loopback 的 IPv4 地址。
func getLANIPv4Addrs() []net.IP {
	var ips []net.IP
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				ip4 := ipnet.IP.To4()
				if ip4 != nil && !ip4.IsLoopback() {
					ips = append(ips, ip4)
				}
			}
		}
	}
	return ips
}

// StreamChat 实现双向流聊天 RPC。
func (cs *ChatServiceImpl) StreamChat(stream chatpb.ChatService_StreamChatServer) error {
	ctx := stream.Context()
	senderID := ""

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 接收来自客户端的消息
		envelope, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("recv error: %w", err)
		}

		if envelope == nil {
			continue
		}

		if envelope.SenderId != "" {
			if senderID == "" {
				senderID = envelope.SenderId
			}
			if p, ok := peer.FromContext(stream.Context()); ok {
				ipAddr := ""
				if tcpAddr, ok := p.Addr.(*net.TCPAddr); ok {
					ipAddr = tcpAddr.IP.String()
				} else {
					host, _, _ := net.SplitHostPort(p.Addr.String())
					ipAddr = host
				}
				if ipAddr != "" {
					cs.engine.discovery.UpsertPeer(PeerInfo{
						PubKeyBase64: envelope.SenderId,
						IPAddress:    ipAddr,
					})
				}
			}
		}

		var plain []byte
		switch envelope.Type {
		case 1: // Private: DecryptTeam(outer) → DecryptPrivate(inner)
			teamPlain, err := cs.engine.crypto.DecryptTeam(envelope.Payload)
			if err != nil {
				fmt.Printf("[chat] private message DecryptTeam failed: %v\n", err)
				continue
			}
			plain, err = cs.engine.crypto.DecryptPrivate(teamPlain, envelope.SenderId)
			if err != nil {
				fmt.Printf("[chat] private message DecryptPrivate failed: %v\n", err)
				continue
			}
		default: // Type 0 (team) or 99 (test): DecryptTeam only
			if envelope.Type != 0 && envelope.Type != 99 {
				fmt.Printf("[chat] warning: unknown envelope type %d\n", envelope.Type)
			}
			var err error
			if envelope.TeamId != "" {
				plain, err = cs.engine.crypto.DecryptTeamForTeam(envelope.TeamId, envelope.Payload)
			} else {
				plain, err = cs.engine.crypto.DecryptTeam(envelope.Payload)
			}
			if err != nil {
				fmt.Printf("[chat] DecryptTeam failed: %v\n", err)
				continue
			}
		}

		var msg eventbus.MsgReceivedPayload
		if err := json.Unmarshal(plain, &msg); err != nil {
			fmt.Printf("[chat] unmarshal error: %v\n", err)
			continue
		}

		msg.TeamID = envelope.TeamId
		msg.ChannelID = envelope.ChannelId

		if envelope.Type == 1 {
			msg.ReceiverID = envelope.ReceiverId
			cs.engine.bus.Publish(eventbus.TopicPrivateMsgReceived, msg)
		} else {
			cs.engine.bus.Publish(eventbus.TopicMsgReceived, msg)
		}
		cs.engine.discovery.RefreshLastSeen(envelope.SenderId)

		// 响应客户端（确认收到）
		response := &chatpb.Envelope{
			SenderId:  cs.engine.crypto.GetPublicKeyBase64(),
			Payload:   []byte("ack"),
			Signature: "",
		}
		if err := stream.Send(response); err != nil {
			return fmt.Errorf("send error: %w", err)
		}
	}
}

// SyncMetadata 实现元数据同步双向流 RPC。
func (cs *ChatServiceImpl) SyncMetadata(stream chatpb.ChatService_SyncMetadataServer) error {
	ctx := stream.Context()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 接收客户端的元数据同步请求
		req, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("recv error: %w", err)
		}

		if req == nil {
			continue
		}

		// TODO: 实现 HLC 对账逻辑
		// 对等节点提供的 HLC 时钟，用于同步离线期间的消息

		// 响应同步结果
		response := &chatpb.MetadataSyncResponse{
			SenderId: cs.engine.crypto.GetPublicKeyBase64(),
			Hlc:      0,
			Payload:  []byte{},
		}
		if err := stream.Send(response); err != nil {
			return fmt.Errorf("send error: %w", err)
		}
	}
}

// Handshake 实现三阶段握手 RPC。
func (cs *ChatServiceImpl) Handshake(ctx context.Context, req *chatpb.HandshakeRequest) (*chatpb.HandshakeResponse, error) {
	teamMatch := false
	var teamReply []byte

	if plain, err := cs.engine.crypto.DecryptTeam(req.TeamChallenge); err == nil {
		if strings.HasPrefix(string(plain), "parade-handshake-") {
			teamMatch = true
			teamReply, _ = cs.engine.crypto.EncryptTeam(plain)
		}
	}

	if req.SenderId != "" {
		if p, ok := peer.FromContext(ctx); ok {
			ipAddr := ""
			if tcpAddr, ok := p.Addr.(*net.TCPAddr); ok {
				ipAddr = tcpAddr.IP.String()
			} else {
				host, _, _ := net.SplitHostPort(p.Addr.String())
				ipAddr = host
			}
			if ipAddr != "" {
				cs.engine.discovery.UpsertPeer(PeerInfo{
					PubKeyBase64: req.SenderId,
					IPAddress:    ipAddr,
				})
			}
		}
	}

	return &chatpb.HandshakeResponse{
		RemotePubkey: cs.engine.crypto.GetPublicKeyBase64(),
		TeamMatch:    teamMatch,
		TeamReply:    teamReply,
	}, nil
}

// ConnectToPeer 执行到指定 IP 的三阶段连接测试。
func (e *Engine) ConnectToPeer(ipAddress string) (*PeerConnectResult, error) {
	result := &PeerConnectResult{IP: ipAddress}

	myPubKey := e.crypto.GetPublicKeyBase64()
	if myPubKey == "" {
		errStr := "未登录: 没有加载身份密钥，请先在 Identity 页面 Login"
		result.Phase1 = PhaseResult{Success: false, Label: "无法连接", Error: errStr}
		result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: "跳过 (依赖 Phase 1)"}
		result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: "跳过 (依赖 Phase 1)"}
		result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: "跳过 (依赖 Phase 1)"}
		return result, nil
	}

	e.mu.RLock()
	started := e.started
	e.mu.RUnlock()
	if !started {
		errStr := "网络引擎未启动: 请先 Join Team"
		result.Phase1 = PhaseResult{Success: false, Label: "无法连接", Error: errStr}
		result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: "跳过 (依赖 Phase 1)"}
		result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: "跳过 (依赖 Phase 1)"}
		result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: "跳过 (依赖 Phase 1)"}
		return result, nil
	}

	// ---- Phase 1: gRPC Dial + Handshake RPC ----
	target := fmt.Sprintf("%s:%d", ipAddress, e.controlPort)
	conn, err := grpc.Dial(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                15 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		result.Phase1 = PhaseResult{Success: false, Label: "无法连接", Error: fmt.Sprintf("gRPC Dial %s: %v", target, err)}
		result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: "跳过 (依赖 Phase 1)"}
		result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: "跳过 (依赖 Phase 1)"}
		result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: "跳过 (依赖 Phase 1)"}
		return result, nil
	}

	client := chatpb.NewChatServiceClient(conn)
	hsCtx, hsCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer hsCancel()

	resp, err := client.Handshake(hsCtx, &chatpb.HandshakeRequest{
		SenderId:      myPubKey,
		TeamChallenge: []byte{},
	})
	if err != nil {
		conn.Close()
		result.Phase1 = PhaseResult{Success: false, Label: "无法连接", Error: fmt.Sprintf("Handshake RPC %s: %v", ipAddress, err)}
		result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: "跳过 (依赖 Phase 1)"}
		result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: "跳过 (依赖 Phase 1)"}
		result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: "跳过 (依赖 Phase 1)"}
		return result, nil
	}

	if resp.RemotePubkey == "" {
		conn.Close()
		result.Phase1 = PhaseResult{Success: false, Label: "无法连接", Error: fmt.Sprintf("Handshake %s: 远程返回空公钥，可能不是 Parade 实例", ipAddress)}
		result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: "跳过 (依赖 Phase 1)"}
		result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: "跳过 (依赖 Phase 1)"}
		result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: "跳过 (依赖 Phase 1)"}
		return result, nil
	}

	result.PubKey = resp.RemotePubkey
	result.Phase1 = PhaseResult{Success: true, Label: "正常"}

	// ---- Phase 2: Team-key challenge exchange ----
	nonce := "parade-handshake-" + uuid.NewString()[:8]
	challenge, err := e.crypto.EncryptTeam([]byte(nonce))
	if err != nil {
		result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: fmt.Sprintf("本地加密挑战失败: %v", err)}
	} else {
		resp2, err := client.Handshake(hsCtx, &chatpb.HandshakeRequest{
			SenderId:      myPubKey,
			TeamChallenge: challenge,
		})
		if err != nil {
			result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: fmt.Sprintf("Handshake RPC (Phase 2) %s: %v", ipAddress, err)}
		} else if !resp2.TeamMatch {
			result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: "队伍密钥不匹配: 远程无法解密挑战 (两台设备使用了不同的 Team 口令)"}
		} else {
			plain, err := e.crypto.DecryptTeam(resp2.TeamReply)
			if err != nil {
				result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: fmt.Sprintf("本地解密回复失败: %v", err)}
			} else if string(plain) != nonce {
				result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: fmt.Sprintf("队伍验证失败: 解密结果不匹配 (期望 %s, 得到 %s)", nonce, string(plain))}
			} else {
				result.Phase2 = PhaseResult{Success: true, Label: "队伍相同"}
			}
		}
	}

	// Close the temporary handshake connection — Phase 3 uses the pooled connection
	conn.Close()

	// ---- Phase 3: Test message via persistent pooled connection ----
	pooledConn, poolErr := e.getOrDialPeer(result.PubKey, ipAddress)
	if poolErr != nil {
		result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: fmt.Sprintf("建立持久连接失败: %v", poolErr)}
		result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: "跳过 (持久连接未建立)"}
	} else {
		pooledClient := chatpb.NewChatServiceClient(pooledConn)
		streamCtx, streamCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer streamCancel()

		stream, err := pooledClient.StreamChat(streamCtx)
		if err != nil {
			result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: fmt.Sprintf("创建 StreamChat 失败: %v", err)}
			result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: "跳过 (StreamChat 未创建)"}
		} else {
			defer stream.CloseSend()

			msg := eventbus.MsgReceivedPayload{
				HLC:      fmt.Sprintf("test-%d", time.Now().UnixNano()),
				SenderID: myPubKey,
				Content:  []byte("__PARADE_TEST__"),
				Type:     99,
			}
			jsonBytes, err := json.Marshal(msg)
			if err != nil {
				result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: fmt.Sprintf("JSON 序列化失败: %v", err)}
			} else {
				encrypted, err := e.crypto.EncryptTeam(jsonBytes)
				if err != nil {
					result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: fmt.Sprintf("本地加密测试消息失败: %v", err)}
				} else {
					envelope := &chatpb.Envelope{
						SenderId: myPubKey,
						Payload:  encrypted,
					}
					if err := stream.Send(envelope); err != nil {
						result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: fmt.Sprintf("StreamChat.Send: %v", err)}
					} else {
						result.Phase3Send = PhaseResult{Success: true, Label: "消息已发送"}
					}
				}
			}

			ack, err := stream.Recv()
			if err != nil {
				result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: fmt.Sprintf("StreamChat.Recv ack: %v", err)}
			} else if ack == nil {
				result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: "收到空 ack"}
			} else {
				result.Phase3Recv = PhaseResult{Success: true, Label: "已收到对方确认"}
			}
		}
	}

	// 注册 peer 到 discovery (getOrDialPeer 已注册，此处确保 Discovery 更新)
	if result.PubKey != "" {
		e.discovery.UpsertPeer(PeerInfo{
			PubKeyBase64: result.PubKey,
			IPAddress:    ipAddress,
		})
	}

	return result, nil
}
