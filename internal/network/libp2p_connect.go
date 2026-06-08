package network

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"parade/internal/core/eventbus"
	"parade/internal/core/logger"
)

const (
	protocolHandshake = "/parade/handshake/1.0.0"
	protocolTest      = "/parade/test/1.0.0"
)

func (e *libp2pEngine) connectToPeer(ipAddress string) (*PeerConnectResult, error) {
	result := &PeerConnectResult{IP: ipAddress}

	if e.crypto.GetPublicKeyBase64() == "" {
		e.log(logger.Warning, "connect", "ConnectToPeer: identity not loaded")
		fillSkipped(result, "未登录")
		return result, nil
	}
	if !e.started || e.host == nil {
		e.log(logger.Warning, "connect", fmt.Sprintf("ConnectToPeer: engine not started (started=%v hostNil=%v)", e.started, e.host == nil))
		fillSkipped(result, "未启动")
		return result, nil
	}

	identifyPort := e.port + 1
	peerID, remoteUUID, remotePubKey, err := e.discoverPeer(ipAddress, identifyPort)
	if err != nil {
		result.Phase1 = PhaseResult{Success: false, Label: "无法连接", Error: err.Error()}
		result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: "跳过"}
		result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: "跳过"}
		result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: "跳过"}
		e.log(logger.Warning, "connect", fmt.Sprintf("Phase 1 identify %s:%d failed: %v", ipAddress, identifyPort, err))
		return result, nil
	}

	result.PubKey = remotePubKey

	addr := fmt.Sprintf("/ip4/%s/tcp/%d", ipAddress, e.port)
	maddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		result.Phase1 = PhaseResult{Success: false, Label: "无法连接", Error: err.Error()}
		result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: "跳过"}
		result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: "跳过"}
		result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: "跳过"}
		return result, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := e.host.Connect(ctx, peer.AddrInfo{ID: peerID, Addrs: []multiaddr.Multiaddr{maddr}}); err != nil {
		result.Phase1 = PhaseResult{Success: false, Label: "无法连接", Error: err.Error()}
		result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: "跳过"}
		result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: "跳过"}
		result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: "跳过"}
		e.log(logger.Warning, "connect", fmt.Sprintf("Phase 1 libp2p dial %s failed: %v", addr, err))
		return result, nil
	}
	result.Phase1 = PhaseResult{Success: true, Label: "正常"}

	// Phase 2: Team-key challenge (fresh context, Phase 1 may have consumed time)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	nonce := "parade-handshake-" + uuid.NewString()[:8]
	challenge, err := e.crypto.EncryptTeam([]byte(nonce))
	if err != nil {
		result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: err.Error()}
	} else {
		stream, sErr := e.host.NewStream(ctx2, peerID, protocolHandshake)
		if sErr != nil {
			result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: sErr.Error()}
		} else {
			defer stream.Close()
			if _, sErr = stream.Write(challenge); sErr != nil {
				result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: sErr.Error()}
			} else {
				reply, rErr := io.ReadAll(stream)
				if rErr != nil {
					result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: rErr.Error()}
				} else if string(reply) == "TEAM_MISMATCH" {
					result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: "队伍密钥不匹配"}
				} else {
					plain, dErr := e.crypto.DecryptTeam(reply)
					if dErr != nil || string(plain) != nonce {
						result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: "队伍验证失败"}
					} else {
						result.Phase2 = PhaseResult{Success: true, Label: "队伍相同"}
					}
				}
			}
		}
	}

	// Phase 3: Test message (fresh context)
	ctx3, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()

	testStream, tErr := e.host.NewStream(ctx3, peerID, protocolTest)
	if tErr != nil {
		result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: tErr.Error()}
	} else {
		defer testStream.Close()
		if _, wErr := testStream.Write([]byte("test")); wErr != nil {
			result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: wErr.Error()}
		} else {
			result.Phase3Send = PhaseResult{Success: true, Label: "消息已发送"}
		}
		ack := make([]byte, 1)
		if _, rErr := io.ReadFull(testStream, ack); rErr != nil {
			result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: rErr.Error()}
		} else {
			result.Phase3Recv = PhaseResult{Success: true, Label: "已收到对方确认"}
		}
	}

	e.log(logger.Info, "connect", fmt.Sprintf("ConnectToPeer %s result: phase1=%v phase2=%v phase3send=%v phase3recv=%v",
		ipAddress, result.Phase1.Success, result.Phase2.Success, result.Phase3Send.Success, result.Phase3Recv.Success))

	if result.Phase1.Success && remoteUUID != "" {
		e.setPeer(peerID, remoteUUID, remotePubKey)
		e.savePeers()
		e.bus.Publish(eventbus.TopicPeerJoined, eventbus.PeerEventPayload{
			PeerUUID:  remoteUUID,
			IPAddress: result.IP,
		})
		e.log(logger.Info, "connect", fmt.Sprintf("peer %s identified as uuid=%s", peerID.ShortString(), remoteUUID[:16]))
	}

	return result, nil
}

func (e *libp2pEngine) discoverPeer(ip string, identifyPort int) (peer.ID, string, string, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, identifyPort), 5*time.Second)
	if err != nil {
		return "", "", "", fmt.Errorf("identify dial: %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	data, err := io.ReadAll(conn)
	if err != nil {
		return "", "", "", fmt.Errorf("identify read: %w", err)
	}

	var resp struct {
		PeerID string `json:"peer_id"`
		UUID   string `json:"uuid"`
		PubKey string `json:"pubkey"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", "", "", fmt.Errorf("identify parse: %w", err)
	}

	if resp.PeerID == "" || resp.UUID == "" {
		return "", "", "", fmt.Errorf("identify: empty response")
	}

	pid, err := peer.Decode(resp.PeerID)
	if err != nil {
		return "", "", "", fmt.Errorf("identify: invalid peer_id: %w", err)
	}

	return pid, resp.UUID, resp.PubKey, nil
}

func fillSkipped(result *PeerConnectResult, reason string) {
	result.Phase1 = PhaseResult{Success: false, Label: "无法连接", Error: reason}
	result.Phase2 = PhaseResult{Success: false, Label: "队伍相同", Error: "跳过"}
	result.Phase3Send = PhaseResult{Success: false, Label: "消息发送", Error: "跳过"}
	result.Phase3Recv = PhaseResult{Success: false, Label: "收到消息", Error: "跳过"}
}
