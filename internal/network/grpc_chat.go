package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"github.com/hashicorp/mdns"
	"parade/internal/file"
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
	fileEngine *file.Engine

	// gRPC 服务器字段
	controlListener  net.Listener
	controlServer    *grpc.Server
	chatServiceImpl   *ChatServiceImpl
	controlPort      int

	// mDNS 服务器字段
	mdnsServer *mdns.Server

	// gRPC 客户端连接池
	clientConns map[string]*grpc.ClientConn // 按 pubKey 索引
	connMu      sync.RWMutex

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
		bus:         bus,
		crypto:      cry,
		discovery:   NewDiscovery(bus),
		filePlane:   NewFilePlane(bus),
		controlPort: 4327,
		clientConns: make(map[string]*grpc.ClientConn),
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

	// 覆盖默认端口
	if port > 0 {
		e.controlPort = port
	}

	// 启动 gRPC 服务器（控制面）
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", e.controlPort))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", e.controlPort, err)
	}

	e.controlListener = listener
	e.controlServer = grpc.NewServer()
	chatpb.RegisterChatServiceServer(e.controlServer, e.chatServiceImpl)

	// 启动 gRPC 服务器（异步）
	e.lifecycleWG.Add(1)
	go func() {
		defer e.lifecycleWG.Done()
		if err := e.controlServer.Serve(listener); err != nil && err != grpc.ErrServerStopped {
			// 记录错误但不中断启动
			fmt.Printf("gRPC server error: %v\n", err)
		}
	}()

	// 注册 mDNS 服务（将本地节点发布到网络）
	info := []string{fmt.Sprintf("pubkey=%s", e.crypto.GetPublicKeyBase64())}
	service, err := mdns.NewMDNSService(
		"Parade-"+e.crypto.GetPublicKeyBase64()[:8],
		"_parade._tcp",
		"local.",
		"",
		e.controlPort,
		nil,
		info,
	)
	if err != nil {
		fmt.Printf("failed to create mDNS service: %v\n", err)
		// 不中断启动，继续运行
	} else {
		server, err := mdns.NewServer(&mdns.Config{Zone: service})
		if err != nil {
			fmt.Printf("failed to start mDNS server: %v\n", err)
		} else {
			e.mdnsServer = server
		}
	}

	// 启动 Discovery 循环
	e.discovery.SetLocalPubKey(e.crypto.GetPublicKeyBase64())
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
	mdnsServer := e.mdnsServer
	e.started = false
	e.discoveryCtx = nil
	e.discoveryCancel = nil
	e.controlServer = nil
	e.mdnsServer = nil
	e.mu.Unlock()

	// 关闭 mDNS 服务器
	if mdnsServer != nil {
		mdnsServer.Shutdown()
	}

	// 关闭所有客户端连接
	e.connMu.Lock()
	for _, conn := range e.clientConns {
		conn.Close()
	}
	e.clientConns = make(map[string]*grpc.ClientConn)
	e.connMu.Unlock()

	// 关闭 gRPC 服务器
	if server != nil {
		server.GracefulStop()
	}

	// 取消 Discovery 循环
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

	// 获取所有对等节点
	peers := e.discovery.Snapshot()
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
			SenderId:  e.crypto.GetPublicKeyBase64(),
			Payload:   payload,
			Signature: "", // TODO: 填入签名
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
		envelope := &chatpb.Envelope{
			SenderId:  e.crypto.GetPublicKeyBase64(),
			Payload:   payload,
			Signature: "", // TODO: 填入签名
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

func (e *Engine) AttachFileEngine(fe *file.Engine) {
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
	conn, err := e.getOrDialPeer(peer.PubKeyBase64, peer.IPAddress)
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

// getOrDialPeer 获取或建立到对等节点的 gRPC 连接。
func (e *Engine) getOrDialPeer(pubKey, ipAddr string) (*grpc.ClientConn, error) {
	if pubKey == "" || ipAddr == "" {
		return nil, errors.New("pubKey and ipAddr are required")
	}

	// 尝试从池中获取
	e.connMu.RLock()
	conn, exists := e.clientConns[pubKey]
	e.connMu.RUnlock()

	if exists && conn != nil {
		return conn, nil
	}

	// 新建连接
	target := fmt.Sprintf("%s:%d", ipAddr, e.controlPort)
	conn, err := grpc.Dial(target, grpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", target, err)
	}

	// 存入池中
	e.connMu.Lock()
	e.clientConns[pubKey] = conn
	e.connMu.Unlock()

	return conn, nil
}

// removePeerConnection 移除到指定对等节点的连接。
func (e *Engine) removePeerConnection(pubKey string) {
	e.connMu.Lock()
	defer e.connMu.Unlock()

	if conn, exists := e.clientConns[pubKey]; exists {
		conn.Close()
		delete(e.clientConns, pubKey)
	}
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

		// 记录发送者
		if senderID == "" {
			senderID = envelope.SenderId
		}

		// 解密并验证消息
		plain, err := cs.engine.crypto.DecryptTeam(envelope.Payload)
		if err != nil {
			// 记录错误但继续接收
			fmt.Printf("decrypt error: %v\n", err)
			continue
		}

		// 构造消息事件
		var msg eventbus.MsgReceivedPayload
		if err := json.Unmarshal(plain, &msg); err != nil {
			fmt.Printf("unmarshal error: %v\n", err)
			continue
		}

		// 发布到事件总线
		cs.engine.bus.Publish(eventbus.TopicMsgReceived, msg)

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
			Hlc:      0, // TODO: 填入本地 HLC
			Payload:  []byte{}, // TODO: 填入需要同步的消息负载
		}
		if err := stream.Send(response); err != nil {
			return fmt.Errorf("send error: %w", err)
		}
	}
}
