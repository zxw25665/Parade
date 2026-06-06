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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"parade/internal/core/crypto"
	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
	chatpb "parade/internal/network/pb/chatpb"
	pb "parade/internal/network/pb"
)

// Engine is the network control plane implementation satisfying app.NetworkEngine.
type Engine struct {
	mu      sync.RWMutex
	started bool

	bus    eventbus.EventBus
	crypto crypto.Engine

	filePlane  *FilePlane
	fileEngine FileTransferEngine

	// Control plane gRPC server fields (chat + file on single port 4327)
	controlListener net.Listener
	controlServer   *grpc.Server
	chatServiceImpl *ChatServiceImpl
	controlPort     int

	lifecycleWG sync.WaitGroup

	connMgr *ConnMgr

	logr logger.Logger
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
		filePlane:   NewFilePlane(bus),
		controlPort: 4327,
	}
	eng.chatServiceImpl = &ChatServiceImpl{engine: eng}
	eng.connMgr = NewConnMgr(bus, cry, eng.logr, eng.controlPort)
	return eng
}

func (e *Engine) WithLogger(l logger.Logger) *Engine {
	e.logr = l
	if e.connMgr != nil {
		e.connMgr.logr = l
	}
	return e
}

func (e *Engine) log(level logger.LogLevel, source, msg string) {
	if e.logr != nil {
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

	// Register file service on the same server (single port 4327 for both)
	if e.fileEngine != nil {
		fileSvc := NewFileService(e.fileEngine, e.crypto.GetPublicKeyBase64())
		pb.RegisterFileTransferServiceServer(e.controlServer, fileSvc)
		e.log(logger.Info, "grpc", fmt.Sprintf("file service registered on control port :%d", e.controlPort))
	}

	e.lifecycleWG.Add(1)
	go func() {
		defer e.lifecycleWG.Done()
		if err := e.controlServer.Serve(listener); err != nil && err != grpc.ErrServerStopped {
			e.log(logger.Error, "grpc", fmt.Sprintf("gRPC control server error: %v", err))
		}
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

	server := e.controlServer
	e.started = false
	e.controlServer = nil
	e.mu.Unlock()

	e.connMgr.CloseAll()

	if server != nil {
		server.GracefulStop()
	}

	e.lifecycleWG.Wait()
	return nil
}

func (e *Engine) BroadcastTeam(payload []byte) error {
	e.mu.RLock()
	started := e.started
	e.mu.RUnlock()
	if !started {
		return errors.New("network engine not started")
	}
	return e.connMgr.BroadcastTeam(e.crypto.GetPublicKeyBase64(), payload)
}

func (e *Engine) BroadcastChannel(channelID string, payload []byte) error {
	e.mu.RLock()
	started := e.started
	e.mu.RUnlock()
	if !started {
		return errors.New("network engine not started")
	}
	return e.connMgr.BroadcastChannel(e.crypto.GetPublicKeyBase64(), e.crypto.GetActiveTeam(), channelID, payload)
}

func (e *Engine) UnicastPrivate(targetPubKey string, payload []byte) error {
	if targetPubKey == "" {
		return errors.New("target pubkey is required")
	}
	e.mu.RLock()
	started := e.started
	e.mu.RUnlock()
	if !started {
		return errors.New("network engine not started")
	}
	wrapped, err := e.crypto.EncryptTeam(payload)
	if err != nil {
		return err
	}
	return e.connMgr.UnicastPrivate(e.crypto.GetPublicKeyBase64(), targetPubKey, e.crypto.GetActiveTeam(), wrapped)
}

// OnForeground is a no-op — mDNS discovery has been removed.
func (e *Engine) OnForeground() {}

// Peers 返回已发现节点的快照（用于 app.NetworkEngine 接口）。
func (e *Engine) Peers() []map[string]string {
	peers := e.connMgr.Peers()
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
	e.connMgr.AttachFileEngine(fe)
}

func (e *Engine) StartDownload(targetPubKey, virtualPath, localSavePath string) error {
	if targetPubKey == "" || virtualPath == "" || localSavePath == "" {
		return errors.New("targetPubKey, virtualPath and localSavePath are required")
	}
	if e.fileEngine == nil {
		return errors.New("file engine is not attached")
	}
	peer, exists := e.connMgr.GetPeer(targetPubKey)
	if !exists {
		return fmt.Errorf("peer %s not found in discovery", targetPubKey)
	}
	pc, err := e.connMgr.GetOrDial(peer.PubKeyBase64, peer.IPAddress)
	if err != nil {
		return err
	}
	client := pb.NewFileTransferServiceClient(pc.Conn())
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
	return e.connMgr.BrowseRemote(targetPubKey, path)
}

// StreamChat 实现双向流聊天 RPC。
func (cs *ChatServiceImpl) StreamChat(stream chatpb.ChatService_StreamChatServer) error {
	ctx := stream.Context()
	senderRegistered := false
	senderID := ""

	for {
		select {
		case <-ctx.Done():
			cs.engine.connMgr.UnregisterIncoming(senderID)
			return ctx.Err()
		default:
		}

		envelope, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				cs.engine.connMgr.UnregisterIncoming(senderID)
				return nil
			}
			cs.engine.connMgr.UnregisterIncoming(senderID)
			return fmt.Errorf("recv error: %w", err)
		}

		if envelope == nil {
			continue
		}

		if envelope.SenderId != "" {
			if !senderRegistered {
				senderRegistered = true
				senderID = envelope.SenderId
				cs.engine.connMgr.RegisterIncoming(senderID, stream)
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
					cs.engine.connMgr.UpsertPeer(PeerInfo{
						PubKeyBase64: envelope.SenderId,
						IPAddress:    ipAddr,
					})
				}
			}
		}

		cs.engine.connMgr.processReceivedEnvelope(envelope)

		response := &chatpb.Envelope{
			SenderId:  cs.engine.crypto.GetPublicKeyBase64(),
			Payload:   nil,
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
				cs.engine.connMgr.UpsertPeer(PeerInfo{
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
	conn, err := grpc.NewClient(target,
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
	pc, poolErr := e.connMgr.GetOrDial(result.PubKey, ipAddress)
	if poolErr != nil {
		result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: fmt.Sprintf("建立持久连接失败: %v", poolErr)}
		result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: "跳过 (持久连接未建立)"}
	} else {
		pooledConn := pc.Conn()
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
		e.connMgr.UpsertPeer(PeerInfo{
			PubKeyBase64: result.PubKey,
			IPAddress:    ipAddress,
		})
		if th := e.crypto.TeamKeyHash(); th != "" {
			e.connMgr.AddPeerTeam(result.PubKey, th)
		}
	}

	return result, nil
}
